package codegen

import (
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// ─────────────────────────────────────────────────────────────────────────────
// The resolved command tree — spec (+ $ref composition) → program.
// ─────────────────────────────────────────────────────────────────────────────.

// ownCommands returns the commands this program emits types and stubs for —
// the root, then every own (inline) sub-command.
func (gp *program) ownCommands() []genCommand {
	return append([]genCommand{gp.root}, gp.own...)
}

// methods returns the ProgramHandlers method names: the root, then every own
// and composed sub-command, sorted.
func (gp *program) methods() []string {
	out := make([]string, 0, 1+len(gp.own)+len(gp.composed))
	out = append(out, gp.root.prefix)
	for _, c := range gp.own {
		out = append(out, c.prefix)
	}
	for _, c := range gp.composed {
		out = append(out, c.prefix)
	}
	sort.Strings(out)
	return out
}

// rnode is one node of the resolved command tree used to render the Definition.
type rnode struct {
	name                  string
	prefix                string // ProgramHandlers method (the dispatch Handler), e.g. "MycliparentMyclichild1"
	aliases               []string
	inputs                *Inputs
	help                  cmdHelp             // flattened help fields; for a composed root, from the child spec
	output                *Schema             // command's output type (own commands only; nil for composed)
	discovery             *RemoteDiscovery    // command's plugin discovery (nil = off)
	hidden                bool                // omit from the parent's generated Commands list
	group                 string              // group label that buckets this command in the parent's Commands list
	deprecated            string              // deprecation note for the parent's Commands list (help annotation)
	deprecatedIdentifiers []string            // deprecated aliases of this command (runtime Deprecations)
	passthrough           bool                // every token after this command is a raw positional
	composed              bool                // grafted from a $ref'd child (its types live in the child's cmd)
	remotes               []RemoteCommandSpec // co-located remote sub-commands declared on this command
	children              []rnode
}

// composedCmd is a command supplied by a composed child: the parent's rollup
// method (prefix) delegates to delegateAlias.delegateMethod(). When passthrough is
// set, the call is alias.method() — the package exports
// the constructor directly; otherwise it is alias.Handlers().method() (a generated cli).
type composedCmd struct {
	prefix         string
	delegateAlias  string
	delegateMethod string
	passthrough    bool
}

// composeCtx threads composition state down a composed subtree.
type composeCtx struct {
	composed    bool
	rootPath    string // underscore path of the composed subtree's root in the parent
	childPascal string // root method name the subtree delegates under (child's name, or the handler convention)
	alias       string // import alias of the handler package (composed child cli, or a passthrough package)
	passthrough bool   // delegate via alias.method() (passthrough) instead of alias.Handlers().method()
}

// resolveTree resolves spec into a program, loading any `$ref`'d child specs relative to
// specPath and grafting them as composed subtrees.
type scopedConfigFile struct {
	ConfigurationFile

	Scope string
}

// allScopedConfigFiles gathers every command's config_files across the tree,
// tagging each with its command path. Replaces the Phase-1 flat allConfigFiles
// for the descriptor: the binder needs the scope to chain-scope loading.
func allScopedConfigFiles(spec *Spec) []scopedConfigFile {
	var out []scopedConfigFile
	walkCommands(spec, func(c *Command, path string) {
		if c.inputs() == nil {
			return
		}
		for _, cf := range c.inputs().ConfigFiles {
			out = append(out, scopedConfigFile{ConfigurationFile: cf, Scope: path})
		}
	})
	return out
}

func resolveTree(spec *Spec, specPath, moduleName string) (*program, error) {
	root := spec.Command
	if root.Ref != "" || root.Name == "" {
		return nil, errors.New("root command must have a name (the top-level \"command\" cannot use $ref)")
	}
	gp := &program{
		rootName:        root.Name,
		rootPascal:      toPascalCase(root.Name),
		rootInputs:      root.inputs(),
		rootRemotes:     root.RemoteCommands,
		rootHelp:        commandHelp(root),
		rootOutput:      root.Output,
		rootDiscovery:   root.RemoteDiscovery,
		rootPassthrough: root.Passthrough,
		schemas:         spec.Command.Schemas,
		configFiles:     allScopedConfigFiles(spec),
		envPrefix:       spec.Command.EnvPrefix,
	}
	gp.root = genCommand{
		prefix:      gp.rootPascal,
		invocation:  gp.rootName,
		handler:     lowerFirst(gp.rootPascal) + "Handlers",
		filename:    commandStubFilename(root.Name, "", root.Filename),
		flags:       flagFields(root.inputs()),
		args:        argFields(root.inputs()),
		env:         envFields(root.inputs(), gp.envPrefix),
		config:      configFields(root.inputs()),
		stdinType:   stdinTypeExpr(gp.rootPascal, root.inputs()),
		stdinFormat: stdinFormatExpr(root.inputs()),
		inputs:      []fieldDef{{Field: gp.rootPascal, GoType: gp.rootPascal + "CommandInputs"}},
	}

	absSpec := specPath
	if a, err := filepath.Abs(specPath); err == nil {
		absSpec = filepath.Clean(a)
	}
	base := filepath.Dir(absSpec) // the entry spec is always local; its dir is the base for relative refs
	seen := map[string]bool{absSpec: true}

	tree, err := gp.walk(root.Commands, "", base, moduleName, seen, composeCtx{})
	if err != nil {
		return nil, err
	}
	gp.tree = tree

	sort.Slice(gp.own, func(i, j int) bool { return gp.own[i].prefix < gp.own[j].prefix })
	sort.Slice(gp.composed, func(i, j int) bool { return gp.composed[i].prefix < gp.composed[j].prefix })
	return gp, nil
}

func (gp *program) walk(cmds []Command, parentPath, base, moduleName string, seen map[string]bool, ctx composeCtx) ([]rnode, error) {
	out := make([]rnode, 0, len(cmds))
	for _, c := range cmds {
		if c.Ref != "" {
			if ctx.composed {
				// Transitive $ref: the direct child already composed this grandchild
				// and exposes handler methods for it, so graft its tree here and
				// delegate to the child (no new import) — see composeNestedRef.
				nodes, err := gp.composeNestedRef(c, parentPath, base, moduleName, seen, ctx)
				if err != nil {
					return nil, err
				}
				out = append(out, nodes...)
				continue
			}
			node, err := gp.composeRef(c, parentPath, base, moduleName, seen)
			if err != nil {
				return nil, err
			}
			out = append(out, node)
			continue
		}
		if c.Name == "" {
			return nil, errors.New("command entry has neither a name nor a $ref")
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
				passthrough:    ctx.passthrough,
			})
		} else {
			gc := genCommand{
				prefix:      prefix,
				invocation:  gp.rootName + " " + strings.ReplaceAll(path, "_", " "),
				handler:     lowerFirst(gp.rootPascal) + toPascalCase(path) + "Handlers",
				filename:    commandStubFilename(gp.rootName, path, c.Filename),
				flags:       flagFields(c.inputs()),
				args:        argFields(c.inputs()),
				env:         envFields(c.inputs(), gp.envPrefix),
				config:      configFields(c.inputs()),
				stdinType:   stdinTypeExpr(prefix, c.inputs()),
				stdinFormat: stdinFormatExpr(c.inputs()),
				inputs:      inputsFields(gp.rootPascal, path),
			}
			if c.Handler != nil {
				// Inline-command passthrough: still an own command, but the handler
				// delegates to the package per node, with no subtree cascade. Clear the
				// stub filename so a converted command's old stub is pruned.
				alias, importPath := parseAliasPath(c.Handler.Import)
				gp.addImport(alias, importPath)
				gc.passthrough = true
				gc.delegateAlias = alias
				gc.delegateMethod = c.Handler.Convention
				gc.filename = ""
			}
			gp.own = append(gp.own, gc)
		}

		children, err := gp.walk(c.Commands, path, base, moduleName, seen, ctx)
		if err != nil {
			return nil, err
		}
		out = append(out, rnode{
			name:                  c.Name,
			prefix:                prefix,
			aliases:               c.Aliases,
			inputs:                c.inputs(),
			help:                  commandHelp(c),
			output:                c.Output,
			discovery:             c.RemoteDiscovery,
			hidden:                c.Hidden,
			group:                 c.Group,
			deprecated:            c.Deprecated,
			deprecatedIdentifiers: c.DeprecatedIdentifiers,
			passthrough:           c.Passthrough,
			composed:              ctx.composed,
			remotes:               c.RemoteCommands,
			children:              children,
		})
	}
	if err := checkCollisions(out); err != nil {
		return nil, err
	}
	return out, nil
}

// overlayCommand applies the $ref overlay model: the child command is the base, and every
// identity or presentation key the parent declared on the `$ref` node wins over the child's.
// That is how a parent tailors a child's tree — rename, re-summarize, regroup, hide — without
// forking it. Not overlaid here: `commands`, merged additively at the call site, and the
// handler-coupled keys, which validation rejects on a `$ref` node.
func overlayCommand(child, parent Command) Command {
	m := child
	if parent.Name != "" {
		m.Name = parent.Name
	}
	if len(parent.Aliases) > 0 {
		m.Aliases = parent.Aliases
	}
	if len(parent.DeprecatedIdentifiers) > 0 {
		m.DeprecatedIdentifiers = parent.DeprecatedIdentifiers
	}
	if parent.Hidden {
		m.Hidden = true
	}
	if parent.Group != "" {
		m.Group = parent.Group
	}
	if parent.Deprecated != "" {
		m.Deprecated = parent.Deprecated
	}
	if parent.Summary != "" {
		m.Summary = parent.Summary
	}
	if parent.Description != "" {
		m.Description = parent.Description
	}
	if parent.Usage != "" {
		m.Usage = parent.Usage
	}
	if parent.Header != "" {
		m.Header = parent.Header
	}
	if parent.Footer != "" {
		m.Footer = parent.Footer
	}
	if parent.Headings != nil {
		m.Headings = parent.Headings
	}
	if len(parent.Examples) > 0 {
		m.Examples = parent.Examples
	}
	if len(parent.ExitStatus) > 0 {
		m.ExitStatus = parent.ExitStatus
	}
	if len(parent.SeeAlso) > 0 {
		m.SeeAlso = parent.SeeAlso
	}
	if parent.Help != "" {
		m.Help = parent.Help
	}
	if parent.Man != "" {
		m.Man = parent.Man
	}
	if parent.Markdown != "" {
		m.Markdown = parent.Markdown
	}
	if parent.Filename != "" {
		m.Filename = parent.Filename
	}
	return m
}

// composeRef loads a `$ref`'d child spec and grafts its command tree as a composed subtree,
// applying the overlay model and additively merging any `commands:` the parent authored next
// to the `$ref`. Delegation always targets the child's real handler methods. Transitive refs
// are handled by composeNestedRef during the walk.
func (gp *program) composeRef(c Command, parentPath, base, moduleName string, seen map[string]bool) (rnode, error) {
	locator, err := locateRef(base, c.Ref)
	if err != nil {
		return rnode{}, fmt.Errorf("compose %q: %w", c.Ref, err)
	}
	if seen[locator] {
		return rnode{}, fmt.Errorf("cyclic $ref: %q", c.Ref)
	}
	seen[locator] = true
	defer delete(seen, locator)

	rr, err := loadRef(locator, moduleName)
	if err != nil {
		return rnode{}, fmt.Errorf("compose %q: %w", c.Ref, err)
	}
	childRoot := rr.spec.Command
	if childRoot.Name == "" {
		return rnode{}, fmt.Errorf("composed spec %q has no name", c.Ref)
	}

	// Resolve the handler source for the composed subtree. An explicit `handler:`
	// (the package-import passthrough) wins: handlers come from the declared package via
	// alias.<Convention>(). Otherwise a local/mod:// child auto-delegates to its own
	// generated cli (alias.Handlers().<Name>()).
	var alias, delegateRoot string
	var passthrough bool
	switch {
	case c.Handler != nil:
		a, p := parseAliasPath(c.Handler.Import)
		alias, delegateRoot, passthrough = a, c.Handler.Convention, true
		gp.addImport(a, p)
	default:
		alias = identAlias(childRoot.Name)
		delegateRoot = toPascalCase(childRoot.Name)
		gp.addImport(alias, childCmdImport(rr.dir, rr.module))
	}

	// Overlay the parent's $ref-node keys onto the child (parent wins when present).
	// The grafted command is named after the child's root unless the ref overrides it;
	// the override changes only the parent-side name/path, never the delegation target.
	merged := overlayCommand(childRoot, c)
	graftName := merged.Name
	composeRootPath := graftName
	if parentPath != "" {
		composeRootPath = parentPath + "_" + graftName
	}
	prefix := gp.rootPascal + toPascalCase(composeRootPath)

	gp.composed = append(gp.composed, composedCmd{prefix: prefix, delegateAlias: alias, delegateMethod: delegateRoot, passthrough: passthrough})

	ctx := composeCtx{composed: true, rootPath: composeRootPath, childPascal: delegateRoot, alias: alias, passthrough: passthrough}
	children, err := gp.walk(childRoot.Commands, composeRootPath, rr.childBase, moduleName, seen, ctx)
	if err != nil {
		return rnode{}, err
	}
	// Merge the `commands:` the parent authored next to the `$ref` (the croot/c3 case):
	// they are NOT the child's — they resolve against the PARENT base and are own
	// commands / new compositions (the outer, non-composed context), grafted alongside
	// the child's own subtree. A name/alias collision across the merged set is an error.
	if len(c.Commands) > 0 {
		authored, err := gp.walk(c.Commands, composeRootPath, base, moduleName, seen, composeCtx{})
		if err != nil {
			return rnode{}, err
		}
		children = append(children, authored...)
		if err := checkCollisions(children); err != nil {
			return rnode{}, err
		}
	}
	return rnode{name: graftName, prefix: prefix, aliases: merged.Aliases, inputs: childRoot.inputs(), help: commandHelp(merged), hidden: merged.Hidden, group: merged.Group, deprecated: merged.Deprecated, deprecatedIdentifiers: merged.DeprecatedIdentifiers, composed: true, children: children}, nil
}

// composeNestedRef handles a `$ref` inside an already-composed subtree — a transitive
// parent → child → grandchild ref. The direct child already composed the grandchild and
// exposes handler methods for it, so the parent grafts the grandchild's tree here and lets the
// normal composed walk delegate each node back to the direct child. Parent overlay keys win,
// and any `commands:` next to the nested ref are merged additively.
func (gp *program) composeNestedRef(c Command, parentPath, base, moduleName string, seen map[string]bool, ctx composeCtx) ([]rnode, error) {
	locator, err := locateRef(base, c.Ref)
	if err != nil {
		return nil, fmt.Errorf("compose %q: %w", c.Ref, err)
	}
	if seen[locator] {
		return nil, fmt.Errorf("cyclic $ref: %q", c.Ref)
	}
	seen[locator] = true
	defer delete(seen, locator)

	rr, err := loadRef(locator, moduleName)
	if err != nil {
		return nil, fmt.Errorf("compose %q: %w", c.Ref, err)
	}
	gc := rr.spec.Command
	if gc.Name == "" {
		return nil, fmt.Errorf("composed spec %q has no name", c.Ref)
	}

	// Graft the grandchild as a named command in the current composed subtree: overlay
	// the parent's $ref-node keys, then run it (and its descendants) through the normal
	// walk so the standard composed delegation applies and the rnodes are marked composed
	// (no types emitted here). Its own subtree resolves against the grandchild's dir.
	synth := overlayCommand(gc, c)
	synth.Ref = ""
	synth.Commands = gc.Commands // overlayCommand left Commands == gc's; siblings merge below
	nodes, err := gp.walk([]Command{synth}, parentPath, rr.childBase, moduleName, seen, ctx)
	if err != nil {
		return nil, err
	}
	// Merge `commands:` authored next to the nested `$ref`. Unlike the grandchild's own
	// subtree, these resolve against THIS spec's base and delegate to the same direct
	// child (the current composed ctx). Graft them as children of the grandchild.
	if len(c.Commands) > 0 && len(nodes) == 1 {
		siblingParent := synth.Name
		if parentPath != "" {
			siblingParent = parentPath + "_" + synth.Name
		}
		authored, err := gp.walk(c.Commands, siblingParent, base, moduleName, seen, ctx)
		if err != nil {
			return nil, err
		}
		nodes[0].children = append(nodes[0].children, authored...)
		if err := checkCollisions(nodes[0].children); err != nil {
			return nil, err
		}
	}
	return nodes, nil
}

func (gp *program) addImport(alias, path string) {
	for _, ci := range gp.childImports {
		if ci.Path == path {
			return
		}
	}
	gp.childImports = append(gp.childImports, templateHandlersImport{Alias: alias, Path: path})
}

// childCmdImport resolves the import path of a composed child's cmd package — the one
// exposing Handlers — within the module the child belongs to. It reads the child's conf for
// the cmd package, falling back to the internal/cmd/<child> convention.
func childCmdImport(childDir, module string) string {
	if confPath, err := discoverConf(childDir); err == nil {
		if cc, err := readConf(confPath); err == nil {
			if h := cc.Generate.cmdPkg(); h != nil && h.File != "" {
				return module + "/" + path.Dir(filepath.ToSlash(h.File))
			}
		}
	}
	return module + "/internal/cmd/" + filepath.Base(childDir)
}

// identAlias derives a valid, reasonably unique Go import alias from a command
// name. Each composed child's cmd package is named after the child (internal/cmd/
// <child>), so an explicit alias keeps the rollup's references unambiguous and
// clear of the rotini runtime package (also a bare package name).
func identAlias(name string) string {
	return lowerFirst(toPascalCase(name)) + "cli"
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

// fieldDef is one generated struct field: a Go identifier, its type, and its
// `rotini` struct-tag content — a flag/argument logical name (empty for the
// per-command fields of an <Cmd>Inputs struct, which the binder maps by position).
type fieldDef struct {
	Field   string
	GoType  string
	Tag     string
	Import  string // Go import path backing GoType ("" for builtins); aliased form "alias path"
	Recon   string // recon struct-tag body for env/config fields (key + default/required/secret); "" otherwise
	EnvVar  string // explicit environment variable name for an env field (schema.variable); "" = snake-upper default
	EnvNest string // "<BASE>,<sep>" for a nested env input (schema.nesting): the var-family prefix and separator
	CfgFile string // a config input's pinned source file (schema.file): the value is read from that configuration_files entry ONLY
	Comment string // trailing line-comment on the generated field ("" for none) — e.g. the TextUnmarshaler contract nudge on explicitly-imported argv types
	// Constraint is the space-separated validation struct-tags for an env/config field
	// (e.g. `min:"1" max:"65535" pattern:"^x$"`), which the binder enforces over the
	// reconciled value; "" when the input declares no numeric/string/array constraints.
	Constraint string
}

// genCommand is the fully resolved description of one command node (root or
// sub-command) that the renderers consume.
type genCommand struct {
	prefix      string // PascalCase type prefix, e.g. "RotiniGenerate"
	invocation  string // how a user types it, e.g. "rotini generate"
	handler     string // unexported handler struct name, e.g. "rotiniGenerateHandlers"
	filename    string // handler stub file name, e.g. "rotini_generate.go"
	flags       []fieldDef
	args        []fieldDef
	env         []fieldDef // <Prefix>Env fields (pure environment inputs)
	config      []fieldDef // <Prefix>Config fields (pure config-file inputs)
	stdinType   string     // Stdin field type, e.g. "*RotiniGenerateStdin"; "" when no stdin
	stdinFormat string     // stdin decode format, e.g. "yaml"; "" when no stdin
	inputs      []fieldDef // InputsFields for this command's <Prefix>Inputs

	// Inline-command passthrough: the command's structure + inputs are
	// generated locally (this is still an own command), but its handler delegates to a
	// package instead of a generated stub. When passthrough is set the rollup emits
	// `return delegateAlias.delegateMethod()` and no stub file is seeded.
	passthrough    bool
	delegateAlias  string
	delegateMethod string
}
