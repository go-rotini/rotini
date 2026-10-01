package codegen

import (
	"errors"
	"fmt"
	"maps"
	"path"
	"path/filepath"
	"slices"
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
	remoteHost            string              // program name remote binaries are named after ("" = this program's)
	pluginPath            string              // extra directory searched for BOTH this command's remote kinds
	children              []rnode
}

// composedCmd is a command supplied by a composed child: the parent's rollup
// method (prefix) delegates to delegateAlias.delegateMethod(). When passthrough is
// set, the call is alias.method() — the package exports
// the constructor directly; otherwise it is alias.Handlers().method() (a generated cli).
type composedCmd struct {
	// The composed command's own declared inputs, kept so its typed structs can be emitted
	// when an OWN command sits beneath it. A composed node normally needs none — it
	// delegates to the child's handler, which uses the child package's types. But a
	// `commands:` authored beside a `$ref` is grafted in as an own command of the PARENT,
	// and its <Prefix>Inputs names every ancestor, composed ones included. Without these
	// the parent emits a field whose type nothing declares and the package does not build.
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
	// remoteHost is the program name the subtree's plugin binaries are named after: the
	// composed spec's own root name, so a plugin serves `child cmd` and `parent child cmd`
	// alike. "" outside a composed subtree (the program's own name).
	remoteHost string
}

// scopedConfigFile is one config_files entry with the command path that declared it, which the
// binder needs to load only the files along the invoked chain.
type scopedConfigFile struct {
	ConfigurationFile

	Scope string
}

// allScopedConfigFiles gathers every command's config_files across the tree, tagging each with
// its command path: the binder needs the scope to load only what the invoked chain declares.
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
// It runs the resolution TWICE when the document declares no env_prefix of its own and a
// composed child supplies one. Every generated env-var name is derived from the prefix while
// the tree is being built, but a child's prefix is only discovered by composing it — so the
// first pass finds the prefix and the second builds with it. A document that declares its own
// prefix, or composes nothing, resolves once.
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
		rootPascal:      toPascalCase(root.Name),
		rootInputs:      root.inputs(),
		rootRemotes:     root.RemoteCommands,
		rootHelp:        commandHelp(root),
		rootOutput:      root.Output,
		rootDiscovery:   root.RemoteDiscovery,
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
		handler:        lowerFirst(gp.rootPascal) + "Handlers",
		filename:       commandStubFilename(root.Name, "", root.Filename),
		dashedFilename: dashedStubFilename(root.Name, "", root.Filename),
		inputFields:    gp.inputFieldsOf(root.inputs()),
		stdinType:      stdinTypeExpr(gp.rootPascal, root.inputs()),
		stdinFormat:    stdinFormatExpr(root.inputs()),
		inputs:         []fieldDef{{Field: gp.rootPascal, GoType: gp.rootPascal + "CommandInputs"}},
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
				inputFields:    gp.inputFieldsOf(c.inputs()),
			})
		} else {
			gc := genCommand{
				prefix:         prefix,
				invocation:     gp.rootName + " " + strings.ReplaceAll(path, "_", " "),
				handler:        lowerFirst(gp.rootPascal) + toPascalCase(path) + "Handlers",
				filename:       commandStubFilename(gp.rootName, path, c.Filename),
				dashedFilename: dashedStubFilename(gp.rootName, path, c.Filename),
				inputFields:    gp.inputFieldsOf(c.inputs()),
				stdinType:      stdinTypeExpr(prefix, c.inputs()),
				stdinFormat:    stdinFormatExpr(c.inputs()),
				inputs:         inputsFields(gp.rootPascal, path),
			}
			if c.Handler != nil {
				// Inline-command passthrough: still an own command, but the handler
				// delegates to the package per node, with no subtree cascade. Clear the
				// stub filename so a converted command's old stub is pruned.
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
			discovery:             c.RemoteDiscovery,
			hidden:                c.Hidden,
			group:                 c.Group,
			deprecated:            c.Deprecated,
			deprecatedIdentifiers: c.DeprecatedIdentifiers,
			passthrough:           c.Passthrough,
			composed:              ctx.composed,
			remotes:               c.RemoteCommands,
			remoteHost:            ctx.remoteHost,
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
	// Where the grafted command's plugins are installed is the parent's call to make.
	if parent.PluginPath != "" {
		m.PluginPath = parent.PluginPath
	}
	return m
}

// loadComposedSpec locates and loads the spec a `$ref` command names, relative to base, and
// marks it in seen so a cycle back to it is reported. The caller defers release, which unmarks
// it once the subtree is resolved: a spec may appear twice in a tree, just not inside itself.
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
// applying the overlay model and additively merging any `commands:` the parent authored next
// to the `$ref`. Delegation always targets the child's real handler methods. Transitive refs
// are handled by composeNestedRef during the walk.
func (gp *program) composeRef(c Command, parentPath, base, moduleName string, seen map[string]bool) (rnode, error) {
	rr, release, err := loadComposedSpec(base, c.Ref, moduleName, seen)
	if err != nil {
		return rnode{}, err
	}
	defer release()
	childRoot := rr.spec.Command

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

	// A composed child's BindMeta travels WITH its command tree. Without this the umbrella's
	// descriptor is built from its own document alone, so a child's config_files and
	// env_prefix are silently dropped: `child cmd` reads the configuration file and
	// `parent child cmd` — the same handler, the same generated package — does not.
	gp.adoptComposedMeta(rr.spec, childRoot.Name, composeRootPath)

	gp.composed = append(gp.composed, composedCmd{
		prefix: prefix, delegateAlias: alias, delegateMethod: delegateRoot, passthrough: passthrough,
		inputFields: gp.inputFieldsOf(childRoot.inputs()),
	})

	ctx := composeCtx{composed: true, rootPath: composeRootPath, childPascal: delegateRoot, alias: alias, passthrough: passthrough, remoteHost: childRoot.Name}
	children, err := gp.walk(childRoot.Commands, composeRootPath, rr.childBase, moduleName, seen, ctx)
	if err != nil {
		return rnode{}, err
	}
	// Merge the `commands:` the parent authored next to the `$ref` (a parent adding its own
	// sub-commands under a composed child):
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
	// The composed node is the child's root, so it carries what the child's root dispatches —
	// its remote commands, plugin discovery and passthrough — named as the child names them.
	// Building it from the presentation keys alone once dropped all of these: the child's own
	// binary ran its plugins and `parent child <plugin>` was "takes no arguments".
	return rnode{
		name: graftName, prefix: prefix, aliases: merged.Aliases, inputs: childRoot.inputs(),
		help: commandHelp(merged), hidden: merged.Hidden, group: merged.Group,
		deprecated: merged.Deprecated, deprecatedIdentifiers: merged.DeprecatedIdentifiers,
		passthrough: childRoot.Passthrough, remotes: childRoot.RemoteCommands,
		discovery: childRoot.RemoteDiscovery, pluginPath: merged.PluginPath,
		remoteHost: childRoot.Name, composed: true, children: children,
	}, nil
}

// adoptComposedMeta folds a composed child's document-level binding metadata into the parent's:
// the config_files it declares, re-scoped to where the child now sits, and the env_prefix its
// env inputs are named under.
//
// Config sources carry a Scope — the slash-joined command path they are declared on — and the
// binder matches that against the invoked chain to cascade nearest-wins. A child declares its
// own as "musak-songs", but under the umbrella that command is reached as "musak/songs", so
// the child's root segment is swapped for the path the graft actually occupies. An overlay
// rename is exactly why this cannot be a straight copy.
//
// It also records the child's env_prefix; resolveEnvPrefix decides which one the document
// adopts.
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
// children, or "" when there is nothing to adopt.
//
// A document's own declaration wins — an umbrella that names a prefix has made a choice. A
// disagreement between two children is reported rather than silently resolved: one descriptor
// carries one prefix, and picking a winner would quietly rename the other child's variables.
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

// composeNestedRef handles a `$ref` inside an already-composed subtree — a transitive
// parent → child → grandchild ref. The direct child already composed the grandchild and
// exposes handler methods for it, so the parent grafts the grandchild's tree here and lets the
// normal composed walk delegate each node back to the direct child. Parent overlay keys win,
// and any `commands:` next to the nested ref are merged additively.
func (gp *program) composeNestedRef(c Command, parentPath, base, moduleName string, seen map[string]bool, ctx composeCtx) ([]rnode, error) {
	rr, release, err := loadComposedSpec(base, c.Ref, moduleName, seen)
	if err != nil {
		return nil, err
	}
	defer release()
	gc := rr.spec.Command

	// Graft the grandchild as a named command in the current composed subtree: overlay
	// the parent's $ref-node keys, then run it (and its descendants) through the normal
	// walk so the standard composed delegation applies and the rnodes are marked composed
	// (no types emitted here). Its own subtree resolves against the grandchild's dir.
	synth := overlayCommand(gc, c)
	synth.Ref = ""
	synth.Commands = gc.Commands // overlayCommand left Commands == gc's; siblings merge below
	// The grandchild's plugins are named after the grandchild, as its own binary names them.
	gcCtx := ctx
	gcCtx.remoteHost = gc.Name
	nodes, err := gp.walk([]Command{synth}, parentPath, rr.childBase, moduleName, seen, gcCtx)
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

// checkImportableAcrossModules rejects a composed child whose generated package the consumer
// could never import — a cross-module path with an `internal/` element.
//
// Go's internal rule makes such a package importable only from inside the dependency's own
// tree, and a consuming module is never inside it. Left to the compiler, the failure lands as
//
//	use of internal package example.com/specsuite/internal/cmd/scan not allowed
//
// pointing at a line in a GENERATED file, after a successful validate and a successful
// generate, in a project the author did not write that line into.
//
// It matters more than it looks: rotini's own scaffold puts the cmd package at
// internal/cmd/<name>, which is the right default for an application and exactly wrong for a
// module meant to be composed. A module author following the defaults publishes a CLI nobody
// can graft, and finds out from someone else's build.
//
// Same-module (local $ref) composition is unaffected — internal/ is the right place there.
func checkImportableAcrossModules(importPath, childModule, consumingModule, ref string) error {
	if childModule == "" || childModule == consumingModule {
		return nil // a local ref shares the consumer's module; internal/ is fine
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
	EnvVar  string // the environment variable an env field reads — explicit (schema.variable) or derived (envVarFor); "" for other fields
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

type genCommand struct {
	inputFields // the generated fields of the command's declared inputs

	prefix     string // PascalCase type prefix, e.g. "RotiniGenerate"
	invocation string // how a user types it, e.g. "rotini generate"
	handler    string // unexported handler struct name, e.g. "rotiniGenerateHandlers"
	filename   string // handler stub file name, e.g. "rotini_generate.go"
	// dashedFilename is the stub's pre-underscore name ("app_get-thing.go"), "" when it has
	// none; a stub already seeded under it stays the command's stub (see stubFileFor).
	dashedFilename string
	stdinType      string     // Stdin field type, e.g. "*RotiniGenerateStdin"; "" when no stdin
	stdinFormat    string     // stdin decode format, e.g. "yaml"; "" when no stdin
	inputs         []fieldDef // InputsFields for this command's <Prefix>Inputs

	// Inline-command passthrough: the command's structure + inputs are
	// generated locally (this is still an own command), but its handler delegates to a
	// package instead of a generated stub. When passthrough is set the rollup emits
	// `return delegateAlias.delegateMethod()` and no stub file is seeded.
	passthrough    bool
	delegateAlias  string
	delegateMethod string
}
