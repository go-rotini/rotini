package codegen

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// ─────────────────────────────────────────────────────────────────────────────
// The `rotini generate` workflow.
// ─────────────────────────────────────────────────────────────────────────────.

// GenerateFn is the signature of [Processor.Generate]. A command handler binds it
// under a registry key and fetches it as an injectable service, so tests substitute a
// double.
type GenerateFn = func(specPath, confPath string, watch bool, onGenerate func(result string, err error)) error

// Generate is a convenience over [Processor.Generate]: it builds a Processor for
// version and runs the generate workflow. The companion handlers drive the Processor
// directly; this serves internal callers (init and tests).
func Generate(specPath, confPath string, watch bool, version string, onGenerate func(result string, err error)) error {
	return NewProcessor(version).Generate(specPath, confPath, watch, onGenerate)
}

// generate runs the generator phase over a loaded session (call load — and, through
// the pass, validate — first: invalid input must never reach codegen). It applies the
// built-in conf defaults, then emits the cmd and cmdgen packages plus the enabled doc
// features.
func (s *session) generate() error {
	applyConfDefaults(s.conf.conf, s.spec.spec.Command.Name)
	return generateAll(s.spec.spec, s.conf.conf, s.spec.path, s.version)
}

// ─────────────────────────────────────────────────────────────────────────────
// Code generation — the cli/cligen program (framework, rollup, stubs, literals).
// ─────────────────────────────────────────────────────────────────────────────.

// Generated programs reference the rotini runtime package under this name in
// rendered literals (the Definition, BindMeta, …); the templates hardcode the
// matching import.
const rotiniPkgName = "rotini"

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
	handler     string // unexported handler struct name, e.g. "rotiniGenerateHandlers"
	filename    string // handler stub file name, e.g. "rotini_generate.go"
	flags       []fieldDef
	args        []fieldDef
	env         []fieldDef // <Prefix>Env fields (pure environment inputs)
	config      []fieldDef // <Prefix>Config fields (pure config-file inputs)
	stdinType   string     // Stdin field type, e.g. "*RotiniGenerateStdin"; "" when no stdin
	stdinFormat string     // stdin decode format, e.g. "yaml"; "" when no stdin
	inputs      []fieldDef // InputsFields for this command's <Prefix>Inputs

	// Inline-command passthrough (W9 / D-W9.7): the command's structure + inputs are
	// generated locally (this is still an own command), but its handler delegates to a
	// package instead of a generated stub. When passthrough is set the rollup emits
	// `return delegateAlias.delegateMethod()` and no stub file is seeded.
	passthrough    bool
	delegateAlias  string
	delegateMethod string
}

// layout holds the resolved package locations and import paths for a single
// generation pass. The cmd (handler) package and the cmdgen (framework) package
// may be the same package — even the same file — or two distinct packages:
//
//   - distinct packages (split): the rollup in the cmd package imports the
//     cmdgen package and refers to it qualified (cmdgen.ProgramHandlers);
//   - same package, distinct files: framework and rollup are two files in one
//     package, with unqualified references;
//   - same package and file (combined): framework and rollup are merged into a
//     single file, with unqualified references.
//
// The entrypoint package is optional: when the conf declares one, generate
// writes the binary's main.go there (create-once, never overwritten).
type layout struct {
	frameworkDir     string // absolute output dir for the framework (cmdgen) file
	frameworkPkgName string // cmdgen package name, e.g. "mycli" or "cmdgen"
	frameworkFile    string // framework file name, e.g. "zz_rotini.gen.go"
	frameworkImport  string // cmdgen import path; "" when cmd and cmdgen share a package
	frameworkQual    string // qualifier for the rollup's framework refs, e.g. "cmdgen."; "" when same package

	handlerDir     string // absolute output dir for stubs + rollup (cmd package)
	handlerPkgName string // cmd package name, e.g. "mycli"
	handlerImport  string // cmd package import path (the entrypoint's Program import)
	rollupFile     string // rollup file name, e.g. "zz_rotini.gen.go"

	entrypointDir  string // absolute output dir for the entrypoint main.go; "" when no entrypoint declared
	entrypointFile string // entrypoint file name, e.g. "main.go"; "" when no entrypoint declared

	runtimeImport   string // pre-rendered runtime import spec line, e.g. `rotini "…/internal/runtime"` or `"…/rotini"` (identifier always `rotini`)
	runtimeDir      string // module-relative dir the emitted runtime source is written into (slash path)
	skipRuntimeEmit bool   // true when runtimeDir IS rotini's own embed source (internal/runtime) — import in place, write nothing

	combined bool // same package AND same file → framework+rollup merged into one file
}

// runtimeSourceDir is the module-relative directory holding the runtime embed
// source. A conf pointing runtime_required here (rotini's own dogfood) imports
// it in place; emission is skipped.
const runtimeSourceDir = "internal/runtime"

// runtimeImportSpec renders the Go import line for the emitted runtime package at
// the module-relative dir. The identifier is ALWAYS `rotini` (so the templates'
// `rotini.` qualifier resolves): a dir already named "rotini" imports plain (the
// package is `rotini`), any other dir is aliased — avoiding a redundant alias.
func runtimeImportSpec(moduleName, dir string) string {
	importPath := moduleName + "/" + dir
	if path.Base(dir) == "rotini" {
		return strconv.Quote(importPath)
	}
	return "rotini " + strconv.Quote(importPath)
}

// generateAll runs a single generation pass: it resolves the spec (expanding
// any composed $ref children), writes the framework file, creates missing
// handler stubs (an empty stub per command — the end-user wires them), writes
// the entrypoint main.go when the conf declares one, (re)writes the handler
// rollup, and prunes orphaned stubs. specPath is needed to resolve $ref paths
// relative to the spec.
func generateAll(spec *Spec, conf *Conf, specPath, version string) error {
	moduleRoot, moduleName, err := findModule()
	if err != nil {
		return err
	}
	lay := resolveLayout(conf, moduleRoot, moduleName)
	gp, err := resolveTree(spec, specPath, moduleName, version)
	if err != nil {
		return err
	}

	// Write rotini's embedded JSON Schemas to the project (opt-in via generate.schemas)
	// before codegen, so the editor `$schema=` references resolve even on a pass that
	// later fails. These files are never pruned.
	if err := writeSchemas(conf, moduleRoot); err != nil {
		return err
	}

	// Emit the rotini runtime SOURCE into the conf's runtime package (so the built
	// CLI carries its own runtime and never imports go-rotini/rotini). Skipped for
	// rotini's own dogfood, which points runtime_required at the embed source and
	// imports it in place.
	var runtimeKeep []string
	if rt := conf.Generate.runtimePkg(); rt != nil {
		runtimeKeep = rt.Keep
	}
	if err := writeEmittedRuntime(lay, moduleRoot, runtimeKeep); err != nil {
		return err
	}

	// For each enabled doc feature (help/man), the cligen file gains
	// embedded "<Prefix>" vars + a resolver, and each command's page is (re)written
	// under that feature's dir — rendered from the command's doc-fields, or written
	// verbatim when the command sets the feature's spec string. The conf's feature
	// dir is module-relative; the absolute dir is where files are written/pruned and
	// the cligen-package-relative path is what //go:embed references.
	feats := enabledFeatures(conf)
	frameworks := make([]templateFeature, 0, len(feats))
	outputs := make([]featureOutput, 0, len(feats))
	for _, f := range feats {
		var nodes []helpNode
		if f.desc.perShell {
			nodes = completionNodes() // completion: per shell, not per command
		} else {
			nodes = flattenFeature(gp, f.desc)
		}
		absEmbedDir := filepath.Join(moduleRoot, filepath.FromSlash(f.cfg.EmbedDir))
		absTemplateDir := filepath.Join(moduleRoot, filepath.FromSlash(f.cfg.TemplateDir))

		// The //go:embed path only matters in embed mode, and ONLY then must the
		// embed_dir resolve under the cmdgen package (embed can't reach outside
		// it). An inline feature writes no embedded file, so embed_dir is
		// unconstrained; template_dir is never embedded, so it always is.
		embedRel := ""
		if f.cfg.Embed {
			rel, err := filepath.Rel(lay.frameworkDir, absEmbedDir)
			if err != nil {
				return fmt.Errorf("feature %s embed_dir %q is not under the cmdgen package: %w", f.desc.name, f.cfg.EmbedDir, err)
			}
			if strings.HasPrefix(rel, "..") {
				return fmt.Errorf("generate.features.%s.embed_dir %q must resolve under the framework package %q so //go:embed can reach it", f.desc.name, f.cfg.EmbedDir, path.Dir(filepath.ToSlash(conf.Generate.frameworkPkg().File)))
			}
			embedRel = filepath.ToSlash(rel)
		}

		var contents []string
		var err error
		if f.desc.perShell {
			contents, err = completionContents(gp.rootName, nodes)
		} else {
			contents, err = docFeatureContents(absTemplateDir, nodes, f.desc, f.cfg.Template)
		}
		if err != nil {
			return err
		}

		frameworks = append(frameworks, buildFeatureFramework(nodes, embedRel, f.desc, f.cfg.Embed, contents))
		outputs = append(outputs, featureOutput{desc: f.desc, absEmbedDir: absEmbedDir, nodes: nodes, contents: contents, embed: f.cfg.Embed})
	}

	// Render the framework (cmdgen) and the handler rollup (cmd). When cmd and
	// cmdgen resolve to the same package AND file, the two are merged into a single
	// file (rollup body first, then framework body); otherwise they are written to
	// their own files (two files in one package, or two packages).
	fwContent, err := renderFrameworkFile(gp, lay, frameworks)
	if err != nil {
		return err
	}
	rollupContent, err := renderHandlerRollup(gp, lay)
	if err != nil {
		return err
	}
	if lay.combined {
		merged, err := mergeGenFile(lay.handlerPkgName, rollupContent, fwContent)
		if err != nil {
			return err
		}
		if err := writeGeneratedFile(filepath.Join(lay.handlerDir, lay.rollupFile), merged); err != nil {
			return err
		}
	} else {
		if err := writeGeneratedFile(filepath.Join(lay.frameworkDir, lay.frameworkFile), fwContent); err != nil {
			return err
		}
		if err := writeGeneratedFile(filepath.Join(lay.handlerDir, lay.rollupFile), rollupContent); err != nil {
			return err
		}
	}

	for _, o := range outputs {
		// Inline features write no output files — their content is in the .go.
		// (pruneCligen removes any stale files left from a previous embed:true.)
		if !o.embed {
			continue
		}
		if err := writeFeatureOutputs(o.absEmbedDir, o.nodes, o.contents, o.desc); err != nil {
			return err
		}
	}
	if err := writeHandlerStubs(gp, lay); err != nil {
		return err
	}
	if err := writeEntrypoint(lay, string(detectFileFormat(specPath))); err != nil {
		return err
	}
	// Pruning is implicit (always-on): drop orphaned cmd stubs, orphaned cmdgen
	// feature outputs, and orphaned entrypoint .go files, sparing only the
	// per-package `keep` paths (and test files and the editable feature
	// templates). When the entrypoint shares the cmd directory its keep list is
	// merged into that single prune pass.
	var mainKeep []string
	if m := conf.Generate.mainPkg(); m != nil {
		mainKeep = m.Keep
	}
	cmdKeep := conf.Generate.handlersPkg().Keep
	if lay.entrypointDir != "" && lay.entrypointDir == lay.handlerDir {
		cmdKeep = append(append([]string{}, cmdKeep...), mainKeep...)
	}
	if err := pruneStubs(gp, lay, cmdKeep); err != nil {
		return err
	}
	if err := pruneCligen(lay, conf.Generate.frameworkPkg().Keep, outputs); err != nil {
		return err
	}
	if err := pruneEntrypoint(lay, mainKeep); err != nil {
		return err
	}
	return nil
}

// writeSchemas writes rotini's embedded JSON Schemas to the project paths declared
// under generate.schemas (opt-in). Each path is module-root-relative; the embedded bytes
// are written verbatim — unchanged on a no-op pass (stable mtime), overwritten otherwise —
// so an editor `# yaml-language-server: $schema=<path>` reference can resolve the schema
// locally instead of fetching a remote URL. These files are codegen output but are NOT
// pruned (they are not command-derived; the .json suffix never matches a stub/feature
// prune set either). An absent block, or an absent conf/spec entry, writes nothing.
func writeSchemas(conf *Conf, moduleRoot string) error {
	if conf.Generate == nil || conf.Generate.Schemas == nil {
		return nil
	}
	write := func(label string, sc *SchemaConfig, content []byte) error {
		if sc == nil || sc.Path == "" {
			return nil
		}
		rel := filepath.FromSlash(sc.Path)
		if filepath.IsAbs(rel) {
			return fmt.Errorf("generate.schemas.%s.path %q must be module-root-relative, not absolute", label, sc.Path)
		}
		abs := filepath.Join(moduleRoot, rel)
		if r, err := filepath.Rel(moduleRoot, abs); err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
			return fmt.Errorf("generate.schemas.%s.path %q must resolve under the module root", label, sc.Path)
		}
		return writeGeneratedFile(abs, content)
	}
	s := conf.Generate.Schemas
	if err := write("conf", s.Conf, schemaConfFileBytes); err != nil {
		return err
	}
	return write("spec", s.Spec, schemaSpecFileBytes)
}

// confFeature pairs a doc-feature descriptor with its conf entry.
type confFeature struct {
	desc docFeature
	cfg  *Feature
}

// featureOutput is one enabled feature's resolved absolute output dir +
// per-command nodes, used for writing and pruning its output dir.
type featureOutput struct {
	desc        docFeature
	absEmbedDir string // where rendered output files are written (embed mode)
	nodes       []helpNode
	contents    []string // final per-node content (parallel to nodes), rendered/verbatim/stripped
	embed       bool     // true: write contents to files (//go:embed); false: inline in the .go, write no output files
}

// featureConfigs pairs every doc feature with its conf entry (nil when unset).
// Requires conf.Generate to be non-nil (guaranteed after applyConfDefaults).
func featureConfigs(conf *Conf) []confFeature {
	g := conf.Generate
	if g == nil {
		return nil
	}
	return []confFeature{
		{helpFeatureDesc, g.featureOf("help")},
		{manFeatureDesc, g.featureOf("man")},
		{markdownFeatureDesc, g.featureOf("markdown")},
		{completionFeatureDesc, g.featureOf("completion")},
	}
}

// enabledFeatures returns the doc features toggled on, in help→man order.
func enabledFeatures(conf *Conf) []confFeature {
	var out []confFeature
	for _, f := range featureConfigs(conf) {
		if f.cfg != nil && f.cfg.Enabled {
			out = append(out, f)
		}
	}
	return out
}

// renderFrameworkFile renders the framework file: the ProgramHandlers aggregate
// interface plus the typed input structs for every command. The caller writes it
// (to its own file, or merged with the rollup when cli and cligen are combined).
// It is fully generated and carries a DO NOT EDIT banner.
func renderFrameworkFile(gp *genProgram, lay layout, features []templateFeature) ([]byte, error) {
	own := gp.ownCommands()

	blocks := make([]templateInputBlock, 0, len(own))
	imports := map[string]bool{}
	noteImport := func(imp string) {
		if imp != "" {
			imports[imp] = true
		}
	}
	for _, c := range own {
		blocks = append(blocks, templateInputBlock{
			Prefix:       c.prefix,
			Flags:        toTemplateFields(c.flags),
			Arguments:    toTemplateFields(c.args),
			Env:          toTemplateFields(c.env),
			Config:       toTemplateFields(c.config),
			StdinType:    c.stdinType,
			StdinFormat:  c.stdinFormat,
			InputsFields: toTemplateFields(c.inputs),
		})
		for _, fs := range [][]fieldDef{c.flags, c.args, c.env, c.config} {
			for _, f := range fs {
				noteImport(f.Import)
			}
		}
	}

	outputTypes, err := buildOutputTypes(gp, lay.frameworkPkgName)
	if err != nil {
		return nil, err
	}

	return renderRotiniFile(templateRotiniData{
		Package:       lay.frameworkPkgName,
		RuntimeImport: lay.runtimeImport,
		Imports:       renderImports(imports),
		Methods:       gp.methods(),
		Definition:    renderDefinition(gp),
		Blocks:        blocks,
		OutputTypes:   outputTypes,
		BindMeta:      renderBindMeta(gp),
		Features:      features,
		EmbedImport:   anyEmbed(features),
	})
}

// anyEmbed reports whether any feature emits a //go:embed-backed var (so the
// generated file must import the embed package). Inline-only features need no
// embed import.
func anyEmbed(features []templateFeature) bool {
	for _, f := range features {
		for _, v := range f.Vars {
			if v.Embed != "" {
				return true
			}
		}
	}
	return false
}

// writeHandlerStubs creates a per-command handler stub for the root command and
// every OWN sub-command, but only when the file does not already exist — stubs
// are user-editable, so an existing stub is never overwritten. Composed
// commands have no stub here; their handlers live in the child's package.
//
// initStyle (only `rotini initialize` sets it) seeds the root command and the
// top-level help/version commands from the wired init templates instead of the
// empty stub, so a fresh CLI ships with working -h/--help, -v/--version, and
// help/version commands. To opt out, delete those handler files and run a
// normal `rotini generate` — the empty stubs are seeded in their place.
func writeHandlerStubs(gp *genProgram, lay layout) error {
	for _, c := range gp.ownCommands() {
		if c.passthrough {
			continue // inline-passthrough: the package owns the handler, no stub seeded
		}
		path := filepath.Join(lay.handlerDir, c.filename)
		if _, err := os.Stat(path); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("stat %s: %w", path, err)
		}
		content, err := renderHandlerStubFile(lay.handlerPkgName, c.handler, lay.runtimeImport)
		if err != nil {
			return err
		}
		if err := writeGeneratedFile(path, content); err != nil {
			return err
		}
	}
	return nil
}

// writeEmittedRuntime writes the rotini runtime source into the conf's runtime
// package directory (lay.runtimeDir), each file's `package rotini` clause rewritten
// to the runtime package, then prunes any stale .go left from a previous emit (so a
// runtime that loses a file does not leave an orphan behind). It is a no-op when
// lay.skipRuntimeEmit is set — rotini's own dogfood points runtime_required at the
// embed source (internal/runtime) and imports it in place rather than emitting a copy.
// runtimeKeep spares package-relative paths from pruning; the dir is otherwise fully
// rotini-managed (test files are always kept by pruneGoDir).
func writeEmittedRuntime(lay layout, moduleRoot string, runtimeKeep []string) error {
	if lay.skipRuntimeEmit || lay.runtimeDir == "" {
		return nil
	}
	files, err := emitRuntime("rotini")
	if err != nil {
		return err
	}
	dir := filepath.Join(moduleRoot, filepath.FromSlash(lay.runtimeDir))
	protected := make(map[string]bool, len(files)+len(runtimeKeep))
	for name, content := range files {
		if err := writeGeneratedFile(filepath.Join(dir, name), content); err != nil {
			return err
		}
		protected[name] = true
	}
	for _, k := range runtimeKeep {
		protected[filepath.Base(filepath.ToSlash(k))] = true
	}
	return pruneGoDir(dir, protected)
}

// writeEntrypoint writes the binary's main.go to the conf-declared entrypoint
// package — create-once: the file binds user-owned build metadata (version/
// commit/date), so an existing main.go is never overwritten. A conf without an
// entrypoint writes nothing. extension is the spec/conf file extension (e.g.
// "yaml") baked into the //go:generate directive so it points at the seeded files.
func writeEntrypoint(lay layout, extension string) error {
	if lay.entrypointDir == "" {
		return nil
	}
	path := filepath.Join(lay.entrypointDir, lay.entrypointFile)
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat %s: %w", path, err)
	}
	// The generated package is imported aliased as "cmd" so the reference never
	// collides with the rotini runtime package (also named "rotini").
	content, err := renderMainFile(lay.handlerImport, "cmd", extension)
	if err != nil {
		return err
	}
	return writeGeneratedFile(path, content)
}

// renderHandlerRollup renders the handler rollup: the unexported handlers
// struct, the ProgramHandlers assertion, the Program var, the Handlers accessor,
// and one method per command — own commands return a local stub, composed
// commands delegate to the child's cli package. References to the framework
// (ProgramHandlers, NewProgram) are unqualified when cli and cligen share a
// package, else qualified with the cligen package name. The caller writes it (to
// its own file, or merged with the framework when combined).
func renderHandlerRollup(gp *genProgram, lay layout) ([]byte, error) {
	methods := make([]templateHandlersMethod, 0, 1+len(gp.own)+len(gp.composed))
	for _, c := range gp.ownCommands() {
		if c.passthrough {
			// Inline-command passthrough (D-W9.7): own command (types generated
			// locally) whose handler delegates to a package instead of a stub.
			methods = append(methods, templateHandlersMethod{
				Method:         c.prefix,
				Passthrough:    true,
				DelegateAlias:  c.delegateAlias,
				DelegateMethod: c.delegateMethod,
			})
			continue
		}
		methods = append(methods, templateHandlersMethod{Method: c.prefix, HandlerType: c.handler})
	}
	for _, c := range gp.composed {
		methods = append(methods, templateHandlersMethod{
			Method:         c.prefix,
			Composed:       true,
			Passthrough:    c.passthrough,
			DelegateAlias:  c.delegateAlias,
			DelegateMethod: c.delegateMethod,
		})
	}
	sort.Slice(methods, func(i, j int) bool { return methods[i].Method < methods[j].Method })

	return renderHandlersFile(templateHandlersData{
		Package:         lay.handlerPkgName,
		RuntimeImport:   lay.runtimeImport,
		FrameworkImport: lay.frameworkImport, // "" when cmd and cmdgen share a package
		FrameworkQual:   lay.frameworkQual,   // e.g. "cmdgen."; "" when same package
		ChildImports:    gp.childImports,
		Methods:         methods,
	})
}
