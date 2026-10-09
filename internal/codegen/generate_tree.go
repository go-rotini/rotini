package codegen

import (
	"cmp"
	"errors"
	"fmt"
	"maps"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

// This file resolves the spec, with $ref children composed, into the program's command tree.

// ownCommands returns the commands this program emits types and stubs for: the root,
// then every own (inline) sub-command.
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
	help                  cmdHelp          // flattened help fields; for a composed root, from the child spec
	output                *Schema          // command's output type (own commands only; nil for composed)
	discovery             *PluginDiscovery // command's plugin discovery (nil = off)
	hidden                bool             // omit from the parent's generated Commands list
	group                 string           // group label that buckets this command in the parent's Commands list
	deprecated            string           // deprecation note for the parent's Commands list (help annotation)
	deprecatedIdentifiers []string         // deprecated aliases of this command (runtime Deprecations)
	passthrough           bool             // every token after this command is a raw positional
	composed              bool             // grafted from a $ref'd child (its types live in the child's cmd)
	plugins               []PluginSpec     // plugins declared on this command
	pluginHost            string           // program name plugin binaries are named after ("" = this program's)
	pluginPath            string           // extra directory searched for BOTH this command's kinds of plugin
	children              []rnode
}

// composedCmd is a command supplied by a composed child. The parent's rollup method
// (prefix) delegates to alias.method() when passthrough is set, otherwise to
// alias.Handlers().method() on the child's generated package.
type composedCmd struct {
	// inputFields are the command's declared inputs. They are emitted only when an own
	// command (`commands:` beside the `$ref`) is grafted beneath it, because that
	// command's <Prefix>Inputs names every ancestor; see inputBlocks.
	inputFields

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
	// pluginHost is the program name the subtree's plugin binaries are named after: the
	// composed spec's root name, so a plugin serves `child cmd` and `parent child cmd`
	// alike. "" outside a composed subtree.
	pluginHost string
}

// scopedConfigFile is one config_files entry tagged with the path of the command that
// declared it, so the input reader loads only the files along the invoked chain.
type scopedConfigFile struct {
	ConfigurationFile

	Scope string
}

// allScopedConfigFiles gathers every command's config_files, each tagged with its command path.
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

// resolveTree resolves a spec into the program codegen emits from.
//
// Derived env-var names depend on the env_prefix, but a composed child's prefix is known
// only after composing it. When the document declares no env_prefix and a child supplies
// one, the tree is resolved a second time with the adopted prefix.
func resolveTree(spec *Spec, specPath, moduleName string) (*program, error) {
	gp, err := resolveTreeWith(spec, specPath, moduleName, spec.Command.EnvPrefix)
	if err != nil {
		return nil, err
	}
	if adopted, err := gp.resolveEnvPrefix(); err != nil {
		return nil, err
	} else if adopted != "" {
		return resolveTreeWith(spec, specPath, moduleName, adopted)
	}
	return gp, nil
}

func resolveTreeWith(spec *Spec, specPath, moduleName, envPrefix string) (*program, error) {
	root := spec.Command
	if root.Ref != "" {
		return nil, errors.New("the root command cannot use `$ref`; compose child specs as sub-commands instead")
	}
	if root.Name == "" {
		return nil, errors.New("the root command must have a `name` (it is the binary name)")
	}
	gp := &program{
		rootName:        root.Name,
		rootDisplay:     cmp.Or(root.DisplayName, root.Name),
		rootPascal:      toPascalCase(root.Name),
		rootInputs:      root.inputs(),
		rootPlugins:     root.Plugins,
		rootHelp:        commandHelp(root),
		rootOutput:      root.Output,
		rootDiscovery:   root.PluginDiscovery,
		rootPluginPath:  root.PluginPath,
		rootPassthrough: root.Passthrough,
		schemas:         spec.Command.Schemas,
		configFiles:     allScopedConfigFiles(spec),
		envPrefix:       envPrefix,
		adoptedPrefixes: map[string]string{},
	}
	gp.root = genCommand{
		prefix:         gp.rootPascal,
		invocation:     gp.rootName,
		handler:        lowerFirst(gp.rootPascal) + "Handler",
		filename:       commandStubFilename(root.Name, "", root.Filename),
		dashedFilename: dashedStubFilename(root.Name, "", root.Filename),
		inputFields:    gp.inputFieldsOf(root.inputs()),
		stdinType:      stdinTypeExpr(gp.rootPascal, root.inputs()),
		stdinFormat:    stdinFormatExpr(root.inputs()),
		inputs:         []fieldDef{{Field: gp.rootPascal, GoType: gp.rootPascal + "CommandInputs"}},
		exitCodes:      exitCodesOf(root.ExitStatus),
		secret:         hasSecretInput(root.inputs()),
	}

	absSpec := specPath
	if a, err := filepath.Abs(specPath); err == nil {
		absSpec = filepath.Clean(a)
	}
	base := filepath.Dir(absSpec) // base directory for relative $refs
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

// walk resolves cmds beneath parentPath (an underscore-joined command path) into rnodes,
// recording own commands in gp.own and, inside a composed subtree (ctx.composed), composed
// commands in gp.composed. $ref entries are composed; base resolves relative refs.
func (gp *program) walk(cmds []Command, parentPath, base, moduleName string, seen map[string]bool, ctx composeCtx) ([]rnode, error) {
	out := make([]rnode, 0, len(cmds))
	for _, c := range cmds {
		if c.Ref != "" {
			if ctx.composed {
				// Transitive $ref: the direct child already exposes handler methods for
				// the grandchild, so delegate to it; see composeNestedRef.
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
				inputFields:    gp.inputFieldsOf(c.inputs()),
			})
		} else {
			gc := genCommand{
				prefix:         prefix,
				invocation:     gp.rootName + " " + strings.ReplaceAll(path, "_", " "),
				handler:        lowerFirst(gp.rootPascal) + toPascalCase(path) + "Handler",
				filename:       commandStubFilename(gp.rootName, path, c.Filename),
				dashedFilename: dashedStubFilename(gp.rootName, path, c.Filename),
				inputFields:    gp.inputFieldsOf(c.inputs()),
				stdinType:      stdinTypeExpr(prefix, c.inputs()),
				stdinFormat:    stdinFormatExpr(c.inputs()),
				inputs:         inputsFields(gp.rootPascal, path),
				exitCodes:      exitCodesOf(c.ExitStatus),
				hasChildren:    len(c.Commands) > 0 || len(c.Plugins) > 0 || c.PluginDiscovery != nil,
				secret:         hasSecretInput(c.inputs()),
			}
			if c.Handler != nil {
				// Inline passthrough: an own command whose handler lives in another
				// package (this node only, not its subtree). Clearing the stub filename
				// lets prune remove a stub left from before the conversion.
				alias, importPath := parseAliasPath(c.Handler.Import)
				gp.addImport(alias, importPath)
				gp.noteHandlerImport(importPath)
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
			discovery:             c.PluginDiscovery,
			hidden:                c.Hidden,
			group:                 c.Group,
			deprecated:            c.Deprecated,
			deprecatedIdentifiers: c.DeprecatedIdentifiers,
			passthrough:           c.Passthrough,
			composed:              ctx.composed,
			plugins:               c.Plugins,
			pluginHost:            ctx.pluginHost,
			pluginPath:            c.PluginPath,
			children:              children,
		})
	}
	if err := checkCollisions(out); err != nil {
		return nil, err
	}
	return out, nil
}

// overlayCommand applies the $ref overlay model: the child command is the base, and every
// identity or presentation key the parent set on the `$ref` node overrides the child's.
// `commands` is merged additively by the caller; handler-coupled keys are rejected on a
// `$ref` node by validation.
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
	if len(parent.Groups) > 0 {
		m.Groups = parent.Groups
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
	if parent.PluginPath != "" {
		m.PluginPath = parent.PluginPath
	}
	return m
}

// loadComposedSpec locates and loads the spec a `$ref` names, relative to base, and marks
// it in seen so a cycle back to it is reported. The caller defers release, which unmarks
// it: a spec may appear twice in a tree, but not inside itself.
func loadComposedSpec(base, ref, moduleName string, seen map[string]bool) (rr resolvedRef, release func(), err error) {
	locator, err := locateRef(base, ref)
	if err != nil {
		return rr, nil, fmt.Errorf("compose %q: %w", ref, err)
	}
	if seen[locator] {
		return rr, nil, fmt.Errorf("cyclic $ref: %q", ref)
	}
	seen[locator] = true
	release = func() { delete(seen, locator) }
	if rr, err = loadRef(locator, moduleName); err != nil {
		release()
		return rr, nil, fmt.Errorf("compose %q: %w", ref, err)
	}
	if rr.spec.Command.Name == "" {
		release()
		return rr, nil, fmt.Errorf("composed spec %q has no name", ref)
	}
	return rr, release, nil
}

// composeRef loads a `$ref`'d child spec and grafts its command tree as a composed subtree,
// applying the overlay model and merging any `commands:` authored beside the `$ref`.
// Delegation targets the child's own handler methods regardless of overlay renames.
func (gp *program) composeRef(c Command, parentPath, base, moduleName string, seen map[string]bool) (rnode, error) {
	rr, release, err := loadComposedSpec(base, c.Ref, moduleName, seen)
	if err != nil {
		return rnode{}, err
	}
	defer release()
	childRoot := rr.spec.Command

	// An explicit `handler:` delegates to alias.<Convention>() in the declared package;
	// otherwise the child's generated package is used via alias.Handlers().<Name>().
	var alias, delegateRoot string
	var passthrough bool
	switch {
	case c.Handler != nil:
		a, p := parseAliasPath(c.Handler.Import)
		alias, delegateRoot, passthrough = a, c.Handler.Convention, true
		gp.addImport(a, p)
		gp.noteHandlerImport(p)
	default:
		alias = identAlias(childRoot.Name)
		delegateRoot = toPascalCase(childRoot.Name)
		imp := childCmdImport(rr.dir, rr.module)
		if err := checkImportableAcrossModules(imp, rr.module, moduleName, c.Ref); err != nil {
			return rnode{}, err
		}
		gp.addImport(alias, imp)
	}

	// A name override changes only the parent-side name and path, never the delegation target.
	merged := overlayCommand(childRoot, c)
	graftName := merged.Name
	composeRootPath := graftName
	if parentPath != "" {
		composeRootPath = parentPath + "_" + graftName
	}
	prefix := gp.rootPascal + toPascalCase(composeRootPath)

	// Carry the child's config_files and env_prefix into the parent's InputSettings, so
	// `parent child cmd` reads the same sources as `child cmd`.
	gp.adoptComposedMeta(rr.spec, childRoot.Name, composeRootPath)

	gp.composed = append(gp.composed, composedCmd{
		prefix: prefix, delegateAlias: alias, delegateMethod: delegateRoot, passthrough: passthrough,
		inputFields: gp.inputFieldsOf(childRoot.inputs()),
	})

	ctx := composeCtx{composed: true, rootPath: composeRootPath, childPascal: delegateRoot, alias: alias, passthrough: passthrough, pluginHost: childRoot.Name}
	children, err := gp.walk(childRoot.Commands, composeRootPath, rr.childBase, moduleName, seen, ctx)
	if err != nil {
		return rnode{}, err
	}
	// `commands:` authored beside the `$ref` belong to the parent: they resolve against the
	// parent's base in a non-composed context and are grafted beside the child's subtree.
	// Name or alias collisions across the merged set are errors.
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
	// The composed node is the child's root, so it also carries the root's dispatch
	// behavior: plugins, plugin discovery, and passthrough, named as the child names them.
	return rnode{
		name: graftName, prefix: prefix, aliases: merged.Aliases, inputs: childRoot.inputs(),
		help: commandHelp(merged), hidden: merged.Hidden, group: merged.Group,
		deprecated: merged.Deprecated, deprecatedIdentifiers: merged.DeprecatedIdentifiers,
		passthrough: childRoot.Passthrough, plugins: childRoot.Plugins,
		discovery: childRoot.PluginDiscovery, pluginPath: merged.PluginPath,
		pluginHost: childRoot.Name, composed: true, children: children,
	}, nil
}

// adoptComposedMeta folds a composed child's config_files and env_prefix into the parent.
//
// Each config file's Scope (the slash-joined declaring command path, which the input reader
// matches against the invoked chain) has the child's root segment replaced by the path the
// graft occupies in the parent, which may differ after an overlay rename. The child's
// env_prefix is recorded for resolveEnvPrefix.
func (gp *program) adoptComposedMeta(child *Spec, childRootName, composeRootPath string) {
	scope := gp.rootName + "/" + strings.ReplaceAll(composeRootPath, "_", "/")
	for _, cf := range allScopedConfigFiles(child) {
		// "musak-songs" → "musak/songs"; "musak-songs/search" → "musak/songs/search".
		cf.Scope = scope + strings.TrimPrefix(cf.Scope, childRootName)
		gp.configFiles = append(gp.configFiles, cf)
	}
	if p := child.Command.EnvPrefix; p != "" {
		gp.adoptedPrefixes[p] = childRootName
	}
}

// resolveEnvPrefix reports the env_prefix this document should adopt from its composed
// children, or "" when it declares its own or no child supplies one. Children declaring
// different prefixes is an error, since the program carries a single prefix.
func (gp *program) resolveEnvPrefix() (string, error) {
	if gp.envPrefix != "" || len(gp.adoptedPrefixes) == 0 {
		return "", nil
	}
	if len(gp.adoptedPrefixes) > 1 {
		prefixes := slices.Sorted(maps.Keys(gp.adoptedPrefixes))
		parts := make([]string, 0, len(prefixes))
		for _, p := range prefixes {
			parts = append(parts, fmt.Sprintf("%q (from %s)", p, gp.adoptedPrefixes[p]))
		}
		return "", fmt.Errorf("composed children declare different env_prefix values; %s; "+
			"one descriptor carries one prefix, so declare on %q the env_prefix it should use",
			strings.Join(parts, " and "), gp.rootName)
	}
	for p := range gp.adoptedPrefixes {
		return p, nil
	}
	return "", nil
}

// composeNestedRef handles a `$ref` inside an already-composed subtree (parent → child →
// grandchild). The direct child already exposes handler methods for the grandchild, so the
// grandchild's tree is grafted here and the composed walk delegates each node back to the
// direct child. Overlay keys apply and `commands:` beside the ref are merged.
func (gp *program) composeNestedRef(c Command, parentPath, base, moduleName string, seen map[string]bool, ctx composeCtx) ([]rnode, error) {
	rr, release, err := loadComposedSpec(base, c.Ref, moduleName, seen)
	if err != nil {
		return nil, err
	}
	defer release()
	gc := rr.spec.Command

	// Walk the overlaid grandchild in the current composed context so its nodes delegate
	// to the direct child. Its subtree resolves against the grandchild's directory.
	synth := overlayCommand(gc, c)
	synth.Ref = ""
	synth.Commands = gc.Commands // authored siblings merge below
	gcCtx := ctx
	gcCtx.pluginHost = gc.Name
	nodes, err := gp.walk([]Command{synth}, parentPath, rr.childBase, moduleName, seen, gcCtx)
	if err != nil {
		return nil, err
	}
	// `commands:` beside the nested `$ref` resolve against this spec's base, delegate to the
	// direct child, and are grafted as children of the grandchild.
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

// childCmdImport resolves the import path of a composed child's cmd package (the one
// exposing Handlers) from the child's conf, falling back to <module>/internal/cmd/<dir>.
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

// checkImportableAcrossModules rejects a composed child from another module whose generated
// package path contains an `internal` element, which Go forbids the consumer to import.
// Reporting it here avoids a compile error inside generated code. Same-module composition
// is unaffected.
func checkImportableAcrossModules(importPath, childModule, consumingModule, ref string) error {
	if childModule == "" || childModule == consumingModule {
		return nil
	}
	for seg := range strings.SplitSeq(importPath, "/") {
		if seg != "internal" {
			continue
		}
		return fmt.Errorf(
			"compose %q: the composed CLI's package %q is internal to %q, so this module cannot import it"+
				"; a spec published for others to compose must put its generated package outside internal/,"+
				" so set the cmd package's `file:` in that project's .rotini.conf.yaml to a non-internal path"+
				" (e.g. scancli/zz_rotini.go) and release it again",
			ref, importPath, childModule)
	}
	return nil
}

// identAlias derives the Go import alias for a composed child's cmd package from its
// command name (e.g. "my-tool" → "myToolcli"), keeping it distinct from the rotini runtime.
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

// fieldDef is one generated struct field: a Go identifier, its type, and its `rotini`
// struct-tag content (the input's logical name; empty for the per-command fields of an
// <Cmd>Inputs struct).
type fieldDef struct {
	Field   string
	GoType  string
	Tag     string
	Import  string // Go import path backing GoType ("" for builtins); aliased form "alias path"
	Recon   string // recon struct-tag body for env/config fields (key + default/required/secret); "" otherwise
	EnvVar  string // environment variable an env field reads, explicit (schema.variable) or derived (envVarFor); "" otherwise
	EnvNest string // "<BASE>,<sep>" for a nested env input (schema.nesting): the var-family prefix and separator
	CfgFile string // config input's pinned source file (schema.file); the value is read from that file only
	Comment string // trailing line comment on the generated field, e.g. the TextUnmarshaler note; "" for none
	// Constraint is the space-separated validation struct tags for an env/config field
	// (e.g. `min:"1" max:"65535" pattern:"^x$"`), enforced by the input reader on the
	// reconciled value; "" when the input declares no constraints.
	Constraint string
}

// inputFields are the generated struct fields of a command's declared inputs, one set per
// channel.
type inputFields struct {
	flags  []fieldDef
	args   []fieldDef
	env    []fieldDef // <Prefix>Env fields (pure environment inputs)
	config []fieldDef // <Prefix>Config fields (pure config-file inputs)
}

// inputFieldsOf derives in's generated struct fields under the program's env prefix.
func (gp *program) inputFieldsOf(in *Inputs) inputFields {
	return inputFields{
		flags:  flagFields(in, gp.envPrefix),
		args:   argFields(in),
		env:    envFields(in, gp.envPrefix),
		config: configFields(in),
	}
}

// addImports records in set every import a field of f needs.
func (f inputFields) addImports(set map[string]bool) {
	for _, fs := range [][]fieldDef{f.flags, f.args, f.env, f.config} {
		for _, fd := range fs {
			if fd.Import != "" {
				set[fd.Import] = true
			}
		}
	}
}

// genCommand is the resolved description of one own command (root or sub-command) that
// the renderers consume.
type genCommand struct {
	inputFields // the generated fields of the command's declared inputs

	prefix     string // PascalCase type prefix, e.g. "RotiniGenerate"
	invocation string // how a user types it, e.g. "rotini generate"
	handler    string // unexported handler struct name, e.g. "rotiniGenerateHandler"
	filename   string // handler stub file name, e.g. "rotini_generate.go"
	// dashedFilename is the stub's legacy dashed name ("app_get-thing.go"), "" when it has
	// none; an existing stub under it remains the command's stub (see stubFileFor).
	dashedFilename string
	stdinType      string     // Stdin field type, e.g. "*RotiniGenerateStdin"; "" when no stdin
	stdinFormat    string     // stdin decode format, e.g. "yaml"; "" when no stdin
	inputs         []fieldDef // InputsFields for this command's <Prefix>Inputs
	exitCodes      []int      // the codes its spec's exit_status lists, for the handler audit
	hasChildren    bool       // it declares sub-commands, plugins or plugin discovery
	secret         bool       // one of its own inputs is secret, so its stub never prints inputs

	// Inline passthrough: the command's types are generated locally, but the rollup
	// returns delegateAlias.delegateMethod() instead of a stub, and no stub is seeded.
	passthrough    bool
	delegateAlias  string
	delegateMethod string
}
