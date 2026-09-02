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

// This file is the spine of the GENERATE stage: a validated spec + conf resolve into a
// [program] (resolveProgram), and program.generate() runs the emit steps in order. The
// step methods (emitSchemas/emitCmdFile/…) live here; the generate_* files
// are the renderers and writers those steps call.

// GenerateFn is the signature of [Processor.Generate]. A command handler binds it
// under a registry key and fetches it as an injectable service, so tests substitute a
// double.
type GenerateFn = func(specPath, confPath string, watch bool, onGenerate func(result string, err error)) error

// Generated programs reference the rotini runtime package under this name in
// rendered literals (the Definition, BindMeta, …); the templates hardcode the
// matching import.
const rotiniPkgName = "rotini"

// runtimeImport is the import line the generated code carries for the rotini
// runtime, whose symbols it references under [rotiniPkgName]. The runtime is an
// ordinary library dependency (`go get github.com/go-rotini/rotini`), not emitted
// code, so the path is fixed and needs no alias — the package IS `rotini`.
const runtimeImport = `"github.com/go-rotini/rotini"`

// program is the rotini CLI fully resolved from a spec + conf and ready to emit: the
// command tree (its own inline commands plus any `$ref`-composed children), the OUTPUT
// LAYOUT (where each generated file goes), and the MODULE it is written into. It is the
// spine of the generate stage — resolveProgram builds it, and its generate() method runs
// the emit steps in order. The renderers (generate_literals.go, generate_schemagen.go,
// the templates) are the tools those steps call.
type program struct {
	// inputs — the validated spec + conf and where the spec was read from.
	spec     *Spec
	conf     *Conf
	specPath string

	// context — where the program lives and where its output goes.
	module module // the Go module the spec belongs to (root dir + import path)
	layout layout // resolved output locations (the cmd package, the runtime, the entrypoint)

	// resolved command tree — spec (+ $ref composition) → the model the renderers consume.
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

	root         genCommand               // the root command (own)
	own          []genCommand             // inline sub-commands, sorted by prefix
	composed     []composedCmd            // composed sub-commands, sorted by prefix
	tree         []rnode                  // full resolved tree (own + grafted), for the Definition
	childImports []templateHandlersImport // unique child cli imports for the rollup

	// resolved doc/completion features — embedded into the cmd file (featureBlocks) and
	// written as their own pages (featureOutputs).
	featureBlocks  []templateFeature
	featureOutputs []featureOutput
}

// module is the Go module a spec lives in: the filesystem root (the directory holding
// go.mod) and the module import path. Generated output paths resolve against it.
type module struct {
	root string // absolute dir containing go.mod
	path string // module import path (from go.mod)
}

// resolveProgram resolves a validated spec + conf into a program ready to emit: it finds
// the module, resolves the command tree (expanding any $ref children), resolves the
// output layout, and resolves the enabled doc/completion features — everything the
// program's generate() steps need.
func resolveProgram(spec *Spec, conf *Conf, specPath string) (*program, error) {
	root, name, err := findModule()
	if err != nil {
		return nil, err
	}
	p, err := resolveTree(spec, specPath, name)
	if err != nil {
		return nil, err
	}
	p.spec, p.conf, p.specPath = spec, conf, specPath
	p.module = module{root: root, path: name}
	p.layout = resolveLayout(conf, root, name)
	if err := p.resolveFeatures(); err != nil {
		return nil, err
	}
	return p, nil
}

// generate emits the program — the ordered codegen process. Each step is a method below;
// reading this list top to bottom IS reading what `rotini generate` does.
func (p *program) generate() error {
	steps := []struct {
		name string
		do   func() error
	}{
		{"emit schemas", p.emitSchemas},
		{"emit models file", p.emitModelsFile},
		{"emit cmd file", p.emitCmdFile},
		{"emit feature outputs", p.emitFeatures},
		{"emit handler stubs", p.emitStubs},
		{"emit entrypoint", p.emitEntrypoint},
		{"prune orphans", p.prune},
	}
	for _, s := range steps {
		if err := s.do(); err != nil {
			return fmt.Errorf("%s: %w", s.name, err)
		}
	}
	return nil
}

// ─── the generate steps ─────────────────────────────────────────────────────────.

// emitSchemas writes rotini's embedded JSON Schemas to the project (opt-in via
// generate.schemas), before codegen so an editor $schema= reference resolves even on a
// pass that later fails. These files are never pruned.
func (p *program) emitSchemas() error { return writeSchemas(p.conf, p.module.root) }

// emitModelsFile writes the typed input/output structs to their own package when the
// conf declares a `models` target. Without one this is a no-op and the types stay in
// the cmd file, which is the default and the common case.
func (p *program) emitModelsFile() error {
	if !p.layout.splitModels {
		return nil
	}
	blocks, imports := inputBlocks(p)
	outputTypes, err := buildOutputTypes(p, p.layout.modelsPkgName)
	if err != nil {
		return err
	}
	content, err := renderModelsFile(templateModelsData{
		Package:     p.layout.modelsPkgName,
		Imports:     renderImports(imports),
		Blocks:      blocks,
		OutputTypes: outputTypes,
	})
	if err != nil {
		return err
	}
	return writeGeneratedFile(filepath.Join(p.layout.modelsDir, p.layout.modelsFile), content)
}

// emitCmdFile renders + writes the one generated cmd file (framework + rollup + typed
// inputs + feature embeds), beside the editable handler stubs.
func (p *program) emitCmdFile() error {
	content, err := renderCmdFile(p, p.layout, p.featureBlocks)
	if err != nil {
		return err
	}
	return writeGeneratedFile(filepath.Join(p.layout.cmdDir, p.layout.cmdFile), content)
}

// emitFeatures writes each EMBED-mode feature's per-command pages (help/man/completion);
// inline features carry their content in the cmd file, so they write nothing here.
func (p *program) emitFeatures() error {
	for _, o := range p.featureOutputs {
		if !o.embed {
			continue
		}
		if err := writeFeatureOutputs(o.absEmbedDir, o.nodes, o.contents, o.desc); err != nil {
			return err
		}
	}
	return nil
}

// emitStubs creates a missing handler stub per command (create-once — the end-user wires
// them); an existing stub is never overwritten.
func (p *program) emitStubs() error { return writeHandlerStubs(p, p.layout) }

// emitEntrypoint writes the binary's main.go when the conf declares an entrypoint
// (create-once). The spec/conf extension is baked into its //go:generate directive.
func (p *program) emitEntrypoint() error {
	return writeEntrypoint(p.layout, string(detectFileFormat(p.specPath)))
}

// prune drops orphaned generated files — stubs and feature pages no longer in the spec —
// sparing each package's `keep` paths (and test files + editable templates). When the
// entrypoint shares the cmd directory its keep list folds into that one pass.
func (p *program) prune() error {
	var mainKeep []string
	if m := p.conf.Generate.mainPkg(); m != nil {
		mainKeep = m.Keep
	}
	cmdKeep := p.conf.Generate.cmdTarget().Keep
	if p.layout.entrypointDir != "" && p.layout.entrypointDir == p.layout.cmdDir {
		cmdKeep = append(append([]string{}, cmdKeep...), mainKeep...)
	}
	if err := pruneStubs(p, p.layout, cmdKeep); err != nil {
		return err
	}
	if err := pruneFeatureOutputs(p.layout, p.conf.Generate.cmdTarget().Keep, p.featureOutputs); err != nil {
		return err
	}
	return pruneEntrypoint(p.layout, mainKeep)
}

// resolveFeatures resolves the enabled doc/completion features into the blocks embedded
// in the cmd file (featureBlocks) and the per-command output pages (featureOutputs).
func (p *program) resolveFeatures() error {
	feats := enabledFeatures(p.conf)
	p.featureBlocks = make([]templateFeature, 0, len(feats))
	p.featureOutputs = make([]featureOutput, 0, len(feats))
	for _, f := range feats {
		var nodes []helpNode
		if f.desc.perShell {
			nodes = completionNodes()
		} else {
			nodes = flattenFeature(p, f.desc)
		}
		absEmbedDir := filepath.Join(p.module.root, filepath.FromSlash(f.cfg.EmbedDir))
		absTemplateDir := filepath.Join(p.module.root, filepath.FromSlash(f.cfg.TemplateDir))

		embedRel := ""
		if f.cfg.Embed {
			rel, err := filepath.Rel(p.layout.cmdDir, absEmbedDir)
			if err != nil {
				return fmt.Errorf("feature %s embed_dir %q is not under the cmd package: %w", f.desc.name, f.cfg.EmbedDir, err)
			}
			if strings.HasPrefix(rel, "..") {
				return fmt.Errorf("generate.features.%s.embed_dir %q must resolve under the cmd package %q so //go:embed can reach it", f.desc.name, f.cfg.EmbedDir, path.Dir(filepath.ToSlash(p.conf.Generate.cmdTarget().File)))
			}
			embedRel = filepath.ToSlash(rel)
		}

		var contents []string
		var err error
		if f.desc.perShell {
			contents, err = completionContents(p.rootName, nodes)
		} else {
			contents, err = docFeatureContents(absTemplateDir, nodes, f.desc, f.cfg.Template)
		}
		if err != nil {
			return err
		}

		p.featureBlocks = append(p.featureBlocks, buildFeatureBlock(nodes, embedRel, f.desc, f.cfg.Embed, contents))
		p.featureOutputs = append(p.featureOutputs, featureOutput{desc: f.desc, absEmbedDir: absEmbedDir, nodes: nodes, contents: contents, embed: f.cfg.Embed})
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
		if sc == nil || sc.File == "" {
			return nil
		}
		rel := filepath.FromSlash(sc.File)
		if filepath.IsAbs(rel) {
			return fmt.Errorf("generate.schemas.%s.path %q must be module-root-relative, not absolute", label, sc.File)
		}
		abs := filepath.Join(moduleRoot, rel)
		if r, err := filepath.Rel(moduleRoot, abs); err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
			return fmt.Errorf("generate.schemas.%s.path %q must resolve under the module root", label, sc.File)
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

// renderCmdFile renders the single generated cli file: the framework (the
// ProgramHandlers aggregate interface, the typed input structs, the Definition,
// NewProgram, BindMeta) AND the rollup (the handlers struct, Program, Handlers(),
// and the per-command handler wiring) — one package, all unqualified. It is fully
// generated and carries a DO NOT EDIT banner; the editable handler stubs are
// separate create-once files in the same package.
func renderCmdFile(gp *program, lay layout, features []templateFeature) ([]byte, error) {
	blocks, imports := inputBlocks(gp)

	// When a models target moved the typed structs to their own package, this file
	// carries neither them nor their imports — only aliases re-exporting them, so
	// handler code in this package reads identically either way.
	var modelsImport string
	var aliases []string
	outputTypes := ""
	if lay.splitModels {
		modelsImport = "models " + strconv.Quote(lay.modelsImport)
		aliases = modelAliases(gp, blocks)
		blocks, imports = nil, nil
	} else {
		var err error
		if outputTypes, err = buildOutputTypes(gp, lay.cmdPkgName); err != nil {
			return nil, err
		}
	}

	return renderRotiniFile(templateRotiniData{
		Package:       lay.cmdPkgName,
		RuntimeImport: runtimeImport,
		Imports:       renderImports(imports),
		ChildImports:  gp.childImports,
		Methods:       gp.methods(),
		RollupMethods: rollupMethods(gp),
		Definition:    renderDefinition(gp),
		ModelsImport:  modelsImport,
		ModelAliases:  aliases,
		Blocks:        blocks,
		OutputTypes:   outputTypes,
		BindMeta:      renderBindMeta(gp),
		Features:      features,
		EmbedImport:   anyEmbed(features),
	})
}

// inputBlocks assembles the per-command typed-struct blocks and the set of imports
// their field types need. Shared by the cmd and models files so the two cannot drift.
func inputBlocks(gp *program) ([]templateInputBlock, map[string]bool) {
	own := gp.ownCommands()
	blocks := make([]templateInputBlock, 0, len(own))
	imports := map[string]bool{}
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
				if f.Import != "" {
					imports[f.Import] = true
				}
			}
		}
	}
	return blocks, imports
}

// modelAliases lists every type the models package declares, so the cmd package can
// re-export them. The names mirror the models template exactly: the four per-channel
// structs the template always emits, plus the two aggregates.
func modelAliases(gp *program, blocks []templateInputBlock) []string {
	var out []string
	for _, b := range blocks {
		out = append(out, b.Prefix+"Flags", b.Prefix+"Arguments")
		if len(b.Env) > 0 {
			out = append(out, b.Prefix+"Env")
		}
		if len(b.Config) > 0 {
			out = append(out, b.Prefix+"Config")
		}
		out = append(out, b.Prefix+"CommandInputs", b.Prefix+"Inputs")
	}
	out = append(out, outputTypeNames(gp)...)
	return out
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
func writeHandlerStubs(gp *program, lay layout) error {
	for _, c := range gp.ownCommands() {
		if c.passthrough {
			continue // inline-passthrough: the package owns the handler, no stub seeded
		}
		path := filepath.Join(lay.cmdDir, c.filename)
		if _, err := os.Stat(path); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("stat %s: %w", path, err)
		}
		content, err := renderHandlerStubFile(lay.cmdPkgName, c.handler, c.prefix+"Inputs", c.invocation, runtimeImport)
		if err != nil {
			return err
		}
		if err := writeGeneratedFile(path, content); err != nil {
			return err
		}
	}
	return nil
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
	content, err := renderMainFile(lay.cmdImport, "cmd", extension)
	if err != nil {
		return err
	}
	return writeGeneratedFile(path, content)
}

// rollupMethods builds the rollup's per-command wiring (one method each, sorted):
// own commands return a local handler stub, composed commands delegate to the
// child's cmd package. renderCmdFile folds these into the generated file's handlers
// struct — unqualified, since the rollup shares the cmd package with the framework.
func rollupMethods(gp *program) []templateHandlersMethod {
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
