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

// ─────────────────────────────────────────────────────────────────────────────
// Code generation — the cli package (framework + rollup + stubs + literals).
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
// generation pass. The cli package (the conf's single `cmd` target) holds the
// editable handler stubs AND the one generated file — the framework (Definition,
// NewProgram, ProgramHandlers, the typed inputs) and the rollup (the handlers
// struct + Program + the command→handler wiring) merged into it, all referencing
// each other unqualified since they share the package. The runtime is the only
// separate, imported package. The entrypoint package is optional: when the conf
// declares one, generate writes the binary's main.go there (create-once).
type layout struct {
	cliDir     string // absolute output dir for the cli package (editable stubs + the generated file)
	cliPkgName string // cli package name, e.g. "mycli"
	cliFile    string // basename of the single generated file (framework + rollup), e.g. "zz_rotini.gen.go"
	cliImport  string // cli package import path (the entrypoint's Program import)

	entrypointDir  string // absolute output dir for the entrypoint main.go; "" when no entrypoint declared
	entrypointFile string // entrypoint file name, e.g. "main.go"; "" when no entrypoint declared

	runtimeImport   string // pre-rendered runtime import spec line, e.g. `rotini "…/internal/runtime"` or `"…/rotini"` (identifier always `rotini`)
	runtimeDir      string // module-relative dir the emitted runtime is written into (slash path) — its package directory
	runtimeFile     string // basename of the single file the ENTIRE runtime merges into, e.g. "zz_runtime.gen.go"
	runtimePkgName  string // Go package name written atop the merged runtime file, e.g. "rotini"
	skipRuntimeEmit bool   // true when runtimeDir IS rotini's own embed source (internal/runtime) — import in place, write nothing
}

// runtimeSourceDir is the module-relative directory holding the runtime embed
// source. A conf pointing the runtime target here imports it in place; emission
// is skipped (rotini's embed source is its own runtime).
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

// generateAll runs a single generation pass: it resolves the spec (expanding any
// composed $ref children), writes the one generated cli file (framework + rollup),
// creates missing handler stubs (an empty stub per command — the end-user wires
// them), emits the runtime, writes the entrypoint main.go when the conf declares
// one, and prunes orphans. specPath resolves $ref paths relative to the spec.
func generateAll(spec *Spec, conf *Conf, specPath string) error {
	moduleRoot, moduleName, err := findModule()
	if err != nil {
		return err
	}
	lay := resolveLayout(conf, moduleRoot, moduleName)
	gp, err := resolveTree(spec, specPath, moduleName)
	if err != nil {
		return err
	}

	// Write rotini's embedded JSON Schemas to the project (opt-in via generate.schemas)
	// before codegen, so the editor `$schema=` references resolve even on a pass that
	// later fails. These files are never pruned.
	if err := writeSchemas(conf, moduleRoot); err != nil {
		return err
	}

	// Emit the rotini runtime into the conf's single runtime file (so the built CLI
	// carries its own runtime and never imports go-rotini/rotini). Skipped only when
	// the runtime target points at the embed source, which is imported in place.
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
		// embed_dir resolve under the cli package (embed can't reach outside
		// it). An inline feature writes no embedded file, so embed_dir is
		// unconstrained; template_dir is never embedded, so it always is.
		embedRel := ""
		if f.cfg.Embed {
			rel, err := filepath.Rel(lay.cliDir, absEmbedDir)
			if err != nil {
				return fmt.Errorf("feature %s embed_dir %q is not under the cli package: %w", f.desc.name, f.cfg.EmbedDir, err)
			}
			if strings.HasPrefix(rel, "..") {
				return fmt.Errorf("generate.features.%s.embed_dir %q must resolve under the framework package %q so //go:embed can reach it", f.desc.name, f.cfg.EmbedDir, path.Dir(filepath.ToSlash(conf.Generate.cmdPkg().File)))
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

	// Render the one cli file — the framework (Definition, NewProgram,
	// ProgramHandlers, the typed inputs) and the rollup (the handlers struct +
	// Program + command→handler wiring) together, all unqualified since they share
	// the cli package — and write it beside the editable handler stubs.
	cliContent, err := renderCliFile(gp, lay, frameworks)
	if err != nil {
		return err
	}
	if err := writeGeneratedFile(filepath.Join(lay.cliDir, lay.cliFile), cliContent); err != nil {
		return err
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
	// Pruning is implicit (always-on): drop orphaned cmd stubs, orphaned cli
	// feature outputs, and orphaned entrypoint .go files, sparing only the
	// per-package `keep` paths (and test files and the editable feature
	// templates). When the entrypoint shares the cmd directory its keep list is
	// merged into that single prune pass.
	var mainKeep []string
	if m := conf.Generate.mainPkg(); m != nil {
		mainKeep = m.Keep
	}
	cmdKeep := conf.Generate.cmdPkg().Keep
	if lay.entrypointDir != "" && lay.entrypointDir == lay.cliDir {
		cmdKeep = append(append([]string{}, cmdKeep...), mainKeep...)
	}
	if err := pruneStubs(gp, lay, cmdKeep); err != nil {
		return err
	}
	if err := pruneCligen(lay, conf.Generate.cmdPkg().Keep, outputs); err != nil {
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

// renderCliFile renders the single generated cli file: the framework (the
// ProgramHandlers aggregate interface, the typed input structs, the Definition,
// NewProgram, BindMeta) AND the rollup (the handlers struct, Program, Handlers(),
// and the per-command handler wiring) — one package, all unqualified. It is fully
// generated and carries a DO NOT EDIT banner; the editable handler stubs are
// separate create-once files in the same package.
func renderCliFile(gp *genProgram, lay layout, features []templateFeature) ([]byte, error) {
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

	outputTypes, err := buildOutputTypes(gp, lay.cliPkgName)
	if err != nil {
		return nil, err
	}

	return renderRotiniFile(templateRotiniData{
		Package:       lay.cliPkgName,
		RuntimeImport: lay.runtimeImport,
		Imports:       renderImports(imports),
		ChildImports:  gp.childImports,
		Methods:       gp.methods(),
		RollupMethods: rollupMethods(gp),
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
		path := filepath.Join(lay.cliDir, c.filename)
		if _, err := os.Stat(path); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("stat %s: %w", path, err)
		}
		content, err := renderHandlerStubFile(lay.cliPkgName, c.handler, lay.runtimeImport)
		if err != nil {
			return err
		}
		if err := writeGeneratedFile(path, content); err != nil {
			return err
		}
	}
	return nil
}

// writeEmittedRuntime merges the ENTIRE rotini runtime into the conf's single
// runtime 'file' (lay.runtimeDir/lay.runtimeFile) as one self-contained package
// (lay.runtimePkgName), then prunes any other .go in that directory — so a previous
// per-file emit or a stale layout leaves no orphan behind, and the runtime is exactly
// one generated file. It is a no-op when lay.skipRuntimeEmit is set — rotini's own
// dogfood may point runtime at the embed source (internal/runtime) and import it in
// place rather than emitting a copy. runtimeKeep spares package-relative paths from
// pruning; the dir is otherwise fully rotini-managed (test files are always kept by
// pruneGoDir).
func writeEmittedRuntime(lay layout, moduleRoot string, runtimeKeep []string) error {
	if lay.skipRuntimeEmit || lay.runtimeDir == "" {
		return nil
	}
	merged, err := mergeRuntime(lay.runtimePkgName)
	if err != nil {
		return err
	}
	dir := filepath.Join(moduleRoot, filepath.FromSlash(lay.runtimeDir))
	if err := writeGeneratedFile(filepath.Join(dir, lay.runtimeFile), merged); err != nil {
		return err
	}
	protected := make(map[string]bool, len(runtimeKeep)+1)
	protected[lay.runtimeFile] = true
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
	content, err := renderMainFile(lay.cliImport, "cmd", extension)
	if err != nil {
		return err
	}
	return writeGeneratedFile(path, content)
}

// rollupMethods builds the rollup's per-command wiring (one method each, sorted):
// own commands return a local handler stub, composed commands delegate to the
// child's cli package. renderCliFile folds these into the generated file's handlers
// struct — unqualified, since the rollup shares the cli package with the framework.
func rollupMethods(gp *genProgram) []templateHandlersMethod {
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
	return methods
}
