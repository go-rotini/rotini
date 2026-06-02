package internal

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// genProgram is a parent spec resolved for code generation: its own command
// tree (inline commands — emit types, stubs, and a rollup method that returns a
// local stub) plus any statically composed commands pulled in via `$ref` (emit
// a rollup method that delegates to the child's rth package; no types or stubs).
type genProgram struct {
	rootName      string
	rootPascal    string
	rootInputs    *Inputs
	rootAliases   []string
	metadata      []MetadataEntry     // ldflag-settable vars emitted in rtg
	versionVar    string              // metadata var feeding Definition.Version (Var == "Version")
	rootRemotes   []RemoteCommandSpec // root-level remote/co-located sub-commands
	rootHelp      cmdHelp             // root command's flattened help fields
	rootOutput    *Schema             // root command's output type (nil when unset)
	rootDiscovery *RemoteDiscovery    // root command's plugin discovery (nil = off)
	schemas       map[string]Schema   // document-level named schemas (for output codegen)

	root         genCommand    // the root command (own)
	own          []genCommand  // inline sub-commands, sorted by prefix
	composed     []composedCmd // composed sub-commands, sorted by prefix
	tree         []rnode       // full resolved tree (own + grafted), for the Definition
	childImports []childImport // unique child rth imports for the rollup
}

// rnode is one node of the resolved command tree used to render the Definition.
type rnode struct {
	name       string
	prefix     string // ProgramHandlers method (the dispatch Handler), e.g. "MycliparentMyclichild1"
	aliases    []string
	inputs     *Inputs
	help       cmdHelp          // flattened help fields; for a composed root, from the child spec
	output     *Schema          // command's output type (own commands only; nil for composed)
	discovery  *RemoteDiscovery // command's plugin discovery (nil = off)
	hidden     bool             // omit from the parent's generated Commands list
	deprecated string           // deprecation note for the parent's Commands list
	children   []rnode
}

// composedCmd is a command supplied by a composed child: the parent's rollup
// method (prefix) delegates to delegateAlias.delegateMethod().
type composedCmd struct {
	prefix         string
	delegateAlias  string
	delegateMethod string
}

// childImport is a composed child's rth import for the rollup. Fields are
// exported because the rollup template ranges over them.
type childImport struct {
	Alias string
	Path  string
}

// composeCtx threads composition state down a composed subtree.
type composeCtx struct {
	composed    bool
	rootPath    string // underscore path of the composed subtree's root in the parent
	childPascal string // PascalCase of the composed child's own root name
	alias       string // import alias of the composed child's rth package
}

// resolveTree resolves spec into a genProgram, loading any `$ref`'d child specs
// (relative to specPath) and grafting them as composed subtrees.
func resolveTree(spec *Spec, specPath, moduleRoot, moduleName string) (*genProgram, error) {
	root := spec.Command
	if root.Ref != "" || root.Name == "" {
		return nil, fmt.Errorf("root command must have a name (the top-level \"command\" cannot use $ref)")
	}
	gp := &genProgram{
		rootName:      root.Name,
		rootPascal:    toPascalCase(root.Name),
		rootInputs:    root.Inputs,
		rootAliases:   root.Aliases,
		metadata:      spec.Metadata,
		rootRemotes:   root.RemoteCommands,
		rootHelp:      commandHelp(root),
		rootOutput:    root.Output,
		rootDiscovery: root.RemoteDiscovery,
		schemas:       spec.Schemas,
	}
	for _, m := range spec.Metadata {
		if m.Var == "Version" {
			gp.versionVar = m.Var
		}
	}
	gp.root = genCommand{
		prefix:    gp.rootPascal,
		handler:   lowerFirst(gp.rootPascal) + "Handlers",
		filename:  root.Name + ".go",
		flags:     flagFields(root.Inputs),
		args:      argFields(root.Inputs),
		env:       envFields(root.Inputs),
		config:    configFields(root.Inputs),
		stdinType: stdinTypeExpr(gp.rootPascal, root.Inputs),
		inputs:    []fieldDef{{Field: gp.rootPascal, GoType: gp.rootPascal + "CommandInputs"}},
	}

	specDir := filepath.Dir(specPath)
	seen := map[string]bool{}
	if abs, err := filepath.Abs(specPath); err == nil {
		seen[filepath.Clean(abs)] = true
	}

	tree, err := gp.walk(root.Commands, "", specDir, moduleRoot, moduleName, seen, composeCtx{})
	if err != nil {
		return nil, err
	}
	gp.tree = tree

	sort.Slice(gp.own, func(i, j int) bool { return gp.own[i].prefix < gp.own[j].prefix })
	sort.Slice(gp.composed, func(i, j int) bool { return gp.composed[i].prefix < gp.composed[j].prefix })
	return gp, nil
}

func (gp *genProgram) walk(cmds []Command, parentPath, specDir, moduleRoot, moduleName string, seen map[string]bool, ctx composeCtx) ([]rnode, error) {
	out := make([]rnode, 0, len(cmds))
	for _, c := range cmds {
		if c.Ref != "" {
			if ctx.composed {
				return nil, fmt.Errorf("nested $ref inside a composed spec is not yet supported (%q)", c.Ref)
			}
			node, err := gp.composeRef(c, parentPath, specDir, moduleRoot, moduleName, seen)
			if err != nil {
				return nil, err
			}
			out = append(out, node)
			continue
		}
		if c.Name == "" {
			return nil, fmt.Errorf("command entry has neither a name nor a $ref")
		}

		path := c.Name
		if parentPath != "" {
			path = parentPath + "_" + c.Name
		}
		prefix := gp.rootPascal + toPascalCase(path)

		if ctx.composed {
			rel := strings.TrimPrefix(strings.TrimPrefix(path, ctx.rootPath), "_")
			gp.composed = append(gp.composed, composedCmd{
				prefix:         prefix,
				delegateAlias:  ctx.alias,
				delegateMethod: ctx.childPascal + toPascalCase(rel),
			})
		} else {
			gp.own = append(gp.own, genCommand{
				prefix:    prefix,
				handler:   lowerFirst(gp.rootPascal) + toPascalCase(path) + "Handlers",
				filename:  gp.rootName + "_" + path + ".go",
				flags:     flagFields(c.Inputs),
				args:      argFields(c.Inputs),
				env:       envFields(c.Inputs),
				config:    configFields(c.Inputs),
				stdinType: stdinTypeExpr(prefix, c.Inputs),
				inputs:    inputsFields(gp.rootPascal, path),
			})
		}

		children, err := gp.walk(c.Commands, path, specDir, moduleRoot, moduleName, seen, ctx)
		if err != nil {
			return nil, err
		}
		out = append(out, rnode{
			name:       c.Name,
			prefix:     prefix,
			aliases:    c.Aliases,
			inputs:     c.Inputs,
			help:       commandHelp(c),
			output:     c.Output,
			discovery:  c.RemoteDiscovery,
			hidden:     c.Hidden,
			deprecated: c.Deprecated,
			children:   children,
		})
	}
	if err := checkCollisions(out); err != nil {
		return nil, err
	}
	return out, nil
}

// composeRef loads a `$ref`'d child spec and grafts its command tree as a
// composed subtree rooted at the child's own name. (A name override and
// transitive $refs are not yet supported.)
func (gp *genProgram) composeRef(c Command, parentPath, specDir, moduleRoot, moduleName string, seen map[string]bool) (rnode, error) {
	childSpecPath := filepath.Clean(filepath.Join(specDir, filepath.FromSlash(c.Ref)))
	abs := childSpecPath
	if a, err := filepath.Abs(childSpecPath); err == nil {
		abs = filepath.Clean(a)
	}
	if seen[abs] {
		return rnode{}, fmt.Errorf("cyclic $ref: %q", c.Ref)
	}
	seen[abs] = true
	defer delete(seen, abs)

	childSpec, err := ReadSpec(childSpecPath)
	if err != nil {
		return rnode{}, fmt.Errorf("compose %q: %w", c.Ref, err)
	}
	childRoot := childSpec.Command
	if childRoot.Name == "" {
		return rnode{}, fmt.Errorf("composed spec %q has no name", c.Ref)
	}

	imp, err := childRthImport(childSpecPath, moduleRoot, moduleName)
	if err != nil {
		return rnode{}, err
	}
	childPascal := toPascalCase(childRoot.Name)
	alias := identAlias(childRoot.Name)
	gp.addImport(alias, imp)

	composeRootPath := childRoot.Name
	if parentPath != "" {
		composeRootPath = parentPath + "_" + childRoot.Name
	}
	prefix := gp.rootPascal + toPascalCase(composeRootPath)

	// The composed subtree root delegates to the child's own root handler.
	gp.composed = append(gp.composed, composedCmd{prefix: prefix, delegateAlias: alias, delegateMethod: childPascal})

	ctx := composeCtx{composed: true, rootPath: composeRootPath, childPascal: childPascal, alias: alias}
	children, err := gp.walk(childRoot.Commands, composeRootPath, filepath.Dir(childSpecPath), moduleRoot, moduleName, seen, ctx)
	if err != nil {
		return rnode{}, err
	}
	return rnode{name: childRoot.Name, prefix: prefix, aliases: c.Aliases, inputs: childRoot.Inputs, help: commandHelp(childRoot), hidden: c.Hidden, deprecated: c.Deprecated, children: children}, nil
}

func (gp *genProgram) addImport(alias, path string) {
	for _, ci := range gp.childImports {
		if ci.Path == path {
			return
		}
	}
	gp.childImports = append(gp.childImports, childImport{Alias: alias, Path: path})
}

// childRthImport resolves the import path of a composed child's rth package,
// reading the child's conf when present and falling back to the cmd/<dir>/rth
// convention.
func childRthImport(childSpecPath, moduleRoot, moduleName string) (string, error) {
	childDir := filepath.Dir(childSpecPath)
	for _, ext := range []string{"yaml", "yml", "jsonc", "json"} {
		confPath := filepath.Join(childDir, ".rotini.conf."+ext)
		if _, err := os.Stat(confPath); err != nil {
			continue
		}
		cc, err := ReadConf(confPath)
		if err == nil && cc.Generate != nil && cc.Generate.Rth != nil && cc.Generate.Rth.Package != "" {
			return moduleName + "/" + filepath.ToSlash(cc.Generate.Rth.Package), nil
		}
	}
	rel, err := filepath.Rel(moduleRoot, filepath.Join(childDir, "rth"))
	if err != nil {
		return "", fmt.Errorf("locate composed child rth package: %w", err)
	}
	return moduleName + "/" + filepath.ToSlash(rel), nil
}

// identAlias derives a valid, reasonably unique Go import alias from a command
// name (every child rth package is named "rth", so aliases are required).
func identAlias(name string) string {
	return lowerFirst(toPascalCase(name)) + "rth"
}

// checkCollisions errors when sibling commands share a name or alias.
func checkCollisions(nodes []rnode) error {
	seen := map[string]bool{}
	for _, n := range nodes {
		for _, id := range append([]string{n.name}, n.aliases...) {
			if seen[id] {
				return fmt.Errorf("duplicate command name or alias %q among siblings", id)
			}
			seen[id] = true
		}
	}
	return nil
}

// rnodesLiteral renders the []rotini.CommandDef literal for a resolved tree. host
// is the root binary name, used for the default plugin-discovery prefix.
func rnodesLiteral(host string, nodes []rnode) string {
	if len(nodes) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("[]" + rotiniPkgName + ".CommandDef{\n")
	for _, n := range nodes {
		b.WriteString("{Name: " + strconv.Quote(n.name) + ",\n")
		b.WriteString("Handler: " + strconv.Quote(n.prefix) + ",\n")
		if len(n.aliases) > 0 {
			b.WriteString("Aliases: " + goStringSlice(n.aliases) + ",\n")
		}
		if fl := flagDefsLiteral(n.inputs); fl != "" {
			b.WriteString("Flags: " + fl + ",\n")
		}
		if al := argDefsLiteral(n.inputs); al != "" {
			b.WriteString("Arguments: " + al + ",\n")
		}
		if cl := rnodesLiteral(host, n.children); cl != "" {
			b.WriteString("Commands: " + cl + ",\n")
		}
		if dl := discoveryLiteral(host, n.discovery); dl != "" {
			b.WriteString("Discovery: " + dl + ",\n")
		}
		b.WriteString("},\n")
	}
	b.WriteString("}")
	return b.String()
}
