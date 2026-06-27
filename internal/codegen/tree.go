package codegen

import (
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/go-rotini/jsonschema"
)

// ─────────────────────────────────────────────────────────────────────────────
// The resolved command tree — spec (+ $ref composition) → genProgram.
// ─────────────────────────────────────────────────────────────────────────────.

// genProgram is a parent spec resolved for code generation: its own command
// tree (inline commands — emit types, stubs, and a rollup method that returns a
// local stub) plus any statically composed commands pulled in via `$ref` (emit
// a rollup method that delegates to the child's cli package; no types or stubs).
type genProgram struct {
	rootName        string
	rootPascal      string
	rootInputs      *Inputs
	rootRemotes     []RemoteCommandSpec // root-level remote/co-located sub-commands
	rootHelp        cmdHelp             // root command's flattened help fields
	rootOutput      *Schema             // root command's output type (nil when unset)
	rootDiscovery   *RemoteDiscovery    // root command's plugin discovery (nil = off)
	rootPassthrough bool                // root command's passthrough (raw positionals)
	schemas         map[string]Schema   // document-level named schemas (for output codegen)
	configFiles     []scopedConfigFile  // per-command config-file sources, tagged with their command path (for the binder's cascade)
	envPrefix       string              // document-level env_prefix for DERIVED env-var names
	version         string              // running rotini version, for the cross-tree version guard on composed specs ("" → skipped)

	root         genCommand               // the root command (own)
	own          []genCommand             // inline sub-commands, sorted by prefix
	composed     []composedCmd            // composed sub-commands, sorted by prefix
	tree         []rnode                  // full resolved tree (own + grafted), for the Definition
	childImports []templateHandlersImport // unique child cli imports for the rollup
}

// ownCommands returns the commands this program emits types and stubs for —
// the root, then every own (inline) sub-command.
func (gp *genProgram) ownCommands() []genCommand {
	return append([]genCommand{gp.root}, gp.own...)
}

// methods returns the ProgramHandlers method names: the root, then every own
// and composed sub-command, sorted.
func (gp *genProgram) methods() []string {
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
	composed              bool                // grafted from a $ref'd child (its types live in the child's cligen)
	remotes               []RemoteCommandSpec // co-located remote sub-commands declared on this command
	children              []rnode
}

// composedCmd is a command supplied by a composed child: the parent's rollup
// method (prefix) delegates to delegateAlias.delegateMethod(). When passthrough is
// set (W9 handler-code composition), the call is alias.method() — the package exports
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
	alias       string // import alias of the handler package (composed child cli, or a W9 passthrough package)
	passthrough bool   // delegate via alias.method() (W9 passthrough) instead of alias.Handlers().method()
}

// resolveTree resolves spec into a genProgram, loading any `$ref`'d child specs
// (relative to specPath) and grafting them as composed subtrees.
// scopedConfigFile is a config_files source paired with the command path it is
// declared on — the Scope the binder matches against the resolved chain to honor
// the cascade (D-W3.1). The path is name-joined ("root", "root/sub", …), the same
// form Binder.chainConfigFiles computes from the chain.
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

func resolveTree(spec *Spec, specPath, moduleName, version string) (*genProgram, error) {
	root := spec.Command
	if root.Ref != "" || root.Name == "" {
		return nil, errors.New("root command must have a name (the top-level \"command\" cannot use $ref)")
	}
	gp := &genProgram{
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
		version:         version,
	}
	gp.root = genCommand{
		prefix:      gp.rootPascal,
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

func (gp *genProgram) walk(cmds []Command, parentPath, base, moduleName string, seen map[string]bool, ctx composeCtx) ([]rnode, error) {
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
				// Inline-command passthrough (D-W9.7/.9): still an own command (inputs
				// generated above), but the handler delegates to the package per-node —
				// no subtree cascade, so a child without its own handler: still stubs.
				// Clear the stub filename so a converted command's old stub is pruned
				// (the package owns the handler now), matching inline→$ref conversion.
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

// overlayCommand applies the $ref OVERLAY model (D-W8.2): the `$ref`'d child command
// is the base, and every identity/presentation key the parent author declared on the
// `$ref` node wins over the child's when present (else the child's is kept). This is
// how a parent consuming a child tailors its tree (rename, re-summarize, regroup,
// hide, deprecate) without forking the child. NOT overlaid here: `commands` (merged
// additively at the call site) and the handler-coupled keys (inputs/output/remote_*),
// which a composed command cannot honor — validation rejects those on a `$ref` node.
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

// composeRef loads a `$ref`'d child spec and grafts its command tree as a composed
// subtree, applying the overlay model (parent keys on the `$ref` node win) and merging
// any `commands:` the parent authored next to the `$ref` (additive — the child's own
// commands delegate to the child; the authored siblings are own/new compositions). The
// delegation always targets the child's real handler methods. Transitive $refs (a
// composed child that itself $refs) are handled by composeNestedRef during the walk.
func (gp *genProgram) composeRef(c Command, parentPath, base, moduleName string, seen map[string]bool) (rnode, error) {
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

	// Resolve the handler source for the composed subtree (W9). An explicit `handler:`
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
		gp.addImport(alias, childCliImport(rr.dir, rr.module))
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

// composeNestedRef handles a `$ref` encountered *inside* an already-composed subtree
// (a transitive ref: parent → child → grandchild). The direct child already composed
// the grandchild and exposes handler methods for it, so the parent does not import the
// grandchild's cli — it grafts the grandchild's command tree here and lets the normal
// composed-walk delegate each node to the direct child (delegateMethod =
// ctx.childPascal + the node's relative path, which matches the child's method names).
// Parent overlay keys win, mirroring composeRef; any `commands:` authored next to the
// nested `$ref` are merged additively (they resolve against the spec that holds the
// nested ref and delegate to the same direct child, which already composed them).
func (gp *genProgram) composeNestedRef(c Command, parentPath, base, moduleName string, seen map[string]bool, ctx composeCtx) ([]rnode, error) {
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

func (gp *genProgram) addImport(alias, path string) {
	for _, ci := range gp.childImports {
		if ci.Path == path {
			return
		}
	}
	gp.childImports = append(gp.childImports, templateHandlersImport{Alias: alias, Path: path})
}

// childCliImport resolves the import path of a composed child's cmd package — the
// handler package that exposes Handlers() — within the module the child belongs to
// (the consuming module for a local ref, the external module for a mod:// ref). It
// reads the child's conf (in childDir) for the cmd package, falling back to the default
// internal/cmd/<child> convention (named after the child's directory).
func childCliImport(childDir, module string) string {
	if confPath, err := discoverConf(childDir); err == nil {
		if cc, err := readConf(confPath); err == nil {
			if h := cc.Generate.handlersPkg(); h != nil && h.File != "" {
				return module + "/" + path.Dir(filepath.ToSlash(h.File))
			}
		}
	}
	return module + "/internal/cmd/" + filepath.Base(childDir)
}

// identAlias derives a valid, reasonably unique Go import alias from a command
// name. Each composed child's cli package is named after the child (internal/cmd/
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

// ─────────────────────────────────────────────────────────────────────────────
// Output & stdin schema types.
// ─────────────────────────────────────────────────────────────────────────────.

// outputRootSentinel is the throwaway root type GenerateGo always emits for the
// assembled output-types document; it carries no data and is stripped, leaving
// only the document-level named schemas and the per-command <Prefix>Output types.
const outputRootSentinel = "rotiniGeneratedOutputsRoot"

// buildOutputTypes generates the Go type declarations for a program's output
// types and named schemas as a formatted source fragment (no package clause, no
// root type) ready to inject into the framework file. It returns "" when the
// program declares no schemas and no command outputs.
//
// Every document-level schema becomes a named type, and every command (root + own
// sub-commands) that declares `output` gets a "<Prefix>Output" type — an alias-like
// named type when the output is a bare `$ref`, or a struct for an inline shape.
// Generation reuses jsonschema.GenerateGo (the same engine behind the spec/conf
// types), so refs, nesting, arrays, and allOf embedding all work.
func buildOutputTypes(gp *genProgram, pkg string) (string, error) {
	defs := collectOutputDefs(gp)
	if len(defs) == 0 {
		return "", nil
	}
	doc, err := json.Marshal(map[string]any{
		"$schema":     "http://json-schema.org/draft-07/schema#",
		"type":        "object",
		"definitions": defs,
	})
	if err != nil {
		return "", fmt.Errorf("marshal output schema document: %w", err)
	}
	src, err := jsonschema.GenerateGo(doc,
		jsonschema.WithGoPackage(pkg),
		jsonschema.WithGoRootType(outputRootSentinel))
	if err != nil {
		return "", fmt.Errorf("generate output types: %w", err)
	}
	return stripGenerated(string(src), outputRootSentinel), nil
}

// eachOwnNode visits every non-composed command node in the resolved tree
// depth-first (pre-order), skipping composed subtrees entirely — their output and
// stdin types live in the child's cligen. Shared by the output- and stdin-type
// collectors.
func eachOwnNode(nodes []rnode, visit func(n *rnode)) {
	for i := range nodes {
		if nodes[i].composed {
			continue
		}
		visit(&nodes[i])
		eachOwnNode(nodes[i].children, visit)
	}
}

// collectOutputDefs assembles the JSON-schema `definitions` for the output-types
// document: each document-level named schema, plus one "<Prefix>Output" per
// command that declares an output. Refs are rewritten from the spec's
// "#/schemas/" space to the document's "#/definitions/" space.
func collectOutputDefs(gp *genProgram) map[string]any {
	defs := map[string]any{}
	for name, sch := range gp.schemas {
		defs[name] = schemaToDoc(sch)
	}
	add := func(prefix string, out *Schema) {
		if out != nil {
			defs[prefix+"Output"] = schemaToDoc(*out)
		}
	}
	// A command's stdin payload type "<Prefix>Stdin" comes from the schema-shape of
	// its stdin InputSchema (only the BaseSchema part — required/default/etc. are
	// input metadata, not JSON-schema type structure).
	addStdin := func(prefix string, in *Inputs) {
		if in != nil && in.Stdin != nil && in.Stdin.Schema != nil {
			defs[prefix+"Stdin"] = schemaToDoc(Schema{BaseSchema: in.Stdin.Schema.BaseSchema})
		}
	}
	add(gp.rootPascal, gp.rootOutput)
	addStdin(gp.rootPascal, gp.rootInputs)
	eachOwnNode(gp.tree, func(n *rnode) {
		add(n.prefix, n.output)
		addStdin(n.prefix, n.inputs)
	})
	return defs
}

// pathFromClaim accumulates the config_source inputs claiming one
// configuration_files entry: a flag's logical name and/or an env input's
// variable.
type pathFromClaim struct {
	flag string
	env  string
}

// collectPathFrom maps each configuration_files name to the inputs that supply
// its path (spec config_source), across the whole command tree: a flag claims
// by logical name; an env input by its variable (explicit `variable:`, else
// the SNAKE_UPPER projection of its name). Validation guarantees single
// claims per channel and that the named entry exists.
func collectPathFrom(gp *genProgram) map[string]pathFromClaim {
	out := map[string]pathFromClaim{}
	add := func(in *Inputs) {
		if in == nil {
			return
		}
		for _, f := range in.Flags {
			if f.Schema != nil && f.Schema.ConfigSource != "" {
				c := out[f.Schema.ConfigSource]
				c.flag = f.Name
				out[f.Schema.ConfigSource] = c
			}
		}
		for _, e := range in.Env {
			if e.Schema != nil && e.Schema.ConfigSource != "" {
				c := out[e.Schema.ConfigSource]
				c.env = envVarName(e, gp.envPrefix)
				out[e.Schema.ConfigSource] = c
			}
		}
	}
	add(gp.rootInputs)
	eachOwnNode(gp.tree, func(n *rnode) { add(n.inputs) })
	return out
}

// contractComment is the TextUnmarshaler nudge emitted as a trailing comment
// on flag/argument fields whose type comes from an explicit spec `import:` —
// the contract is enforced by the argv coerce path, and this puts it where the
// user reads their own generated code. Builtin aliases (duration/time) have
// their own parsing and get no comment; env/config fields decode via recon, a
// different path, so they get none either.
func contractComment(schema *InputSchema) string {
	if schema == nil || strings.TrimSpace(schema.Import) == "" {
		return ""
	}
	return "// parsed via its encoding.TextUnmarshaler (see the spec schema's `type` docs)"
}

// envVarName is an env input's environment variable: the explicit `variable:`
// when declared, else the SNAKE_UPPER projection of its name (recon's default).
func envVarName(e EnvInput, envPrefix string) string {
	if v := envVarOf(e.Schema); v != "" {
		return v // explicit variable: exempt from env_prefix — already exact
	}
	derived := strings.ToUpper(strings.NewReplacer("-", "_", ".", "_").Replace(e.Name))
	if envPrefix != "" {
		return envPrefix + "_" + derived
	}
	return derived
}

// collectStdinSchemas builds the per-command stdin validation schemas for BindMeta:
// each non-composed command that declares a stdin payload maps its "<Prefix>Stdin"
// type name to a self-contained JSON Schema (the payload schema, plus the document's
// named schemas as definitions so any "#/schemas/X" refs resolve). The binder
// validates the decoded payload against it. Returns nil when no command has stdin.
func collectStdinSchemas(gp *genProgram) map[string]string {
	out := map[string]string{}
	add := func(prefix string, in *Inputs) {
		if in == nil || in.Stdin == nil || in.Stdin.Schema == nil {
			return
		}
		if js := stdinValidationSchema(in.Stdin.Schema, gp.schemas); js != "" {
			out[prefix+"Stdin"] = js
		}
	}
	add(gp.rootPascal, gp.rootInputs)
	eachOwnNode(gp.tree, func(n *rnode) { add(n.prefix, n.inputs) })
	if len(out) == 0 {
		return nil
	}
	return out
}

// stdinValidationSchema renders the stdin payload's load-time validation
// schema. It uses only the schema-shape of the InputSchema (the BaseSchema),
// matching the generated type.
func stdinValidationSchema(stdin *InputSchema, docSchemas map[string]Schema) string {
	return validationSchema(Schema{BaseSchema: stdin.BaseSchema}, docSchemas)
}

// validationSchema renders a self-contained JSON Schema (as a JSON string) for
// load-time document validation — the stdin payload and configuration_files
// entries share it: the declared type/properties/constraints, plus the
// document's named schemas as `definitions` (so any "#/schemas/X" refs
// resolve).
func validationSchema(schema Schema, docSchemas map[string]Schema) string {
	body, ok := schemaToDoc(schema).(map[string]any)
	if !ok {
		return ""
	}
	body["$schema"] = "http://json-schema.org/draft-07/schema#"
	if len(docSchemas) > 0 {
		defs := map[string]any{}
		for name, s := range docSchemas {
			defs[name] = schemaToDoc(s)
		}
		body["definitions"] = defs
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return ""
	}
	return string(raw)
}

// schemaToDoc marshals a spec Schema to a generic JSON-schema value and rewrites
// its "#/schemas/" refs to "#/definitions/".
func schemaToDoc(s Schema) any {
	raw, err := json.Marshal(s)
	if err != nil {
		return map[string]any{}
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return map[string]any{}
	}
	rewriteSchemaRefs(v)
	return v
}

// rewriteSchemaRefs deep-walks v, rewriting every {"$ref": "#/schemas/X"} to
// "#/definitions/X" so the assembled document (which uses `definitions`) resolves.
func rewriteSchemaRefs(v any) {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			if k == "$ref" {
				if s, ok := val.(string); ok {
					t[k] = strings.Replace(s, "#/schemas/", "#/definitions/", 1)
				}
				continue
			}
			rewriteSchemaRefs(val)
		}
	case []any:
		for _, e := range t {
			rewriteSchemaRefs(e)
		}
	}
}

// stripGenerated reduces a generated Go file to its type declarations: it drops
// the leading "// Code generated …" banner and the "package …" clause, then
// removes the throwaway sentinel root type. The result is gofmt-clean type decls
// the framework template injects and the whole file is re-formatted.
func stripGenerated(src, sentinel string) string {
	// Header banner, package clause, then the body are blank-line separated.
	if parts := strings.SplitN(src, "\n\n", 3); len(parts) == 3 {
		src = parts[2]
	}
	src = strings.ReplaceAll(src, "type "+sentinel+" map[string]any\n", "")
	return strings.TrimSpace(src)
}
