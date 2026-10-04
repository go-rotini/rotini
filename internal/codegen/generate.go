package codegen

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// This file drives the generate stage: resolveProgram turns a validated spec and conf
// into a [program], and program.generate runs the emit steps in order. The other
// generate_* files hold the renderers and writers those steps call.

// GenerateFn is the signature of [Processor.Generate]. Command handlers fetch it as an
// injectable service so tests can substitute a double.
type GenerateFn = func(specPath, confPath string, watch bool, onGenerate func(result string, err error), onNotices func(notices []error)) error

// rotiniPkgName is the package name rendered literals use to reference the rotini
// runtime. The templates hardcode the matching import.
const rotiniPkgName = "rotini"

// runtimeImport is the generated code's import line for the rotini runtime. The
// package name matches [rotiniPkgName], so no alias is needed.
const runtimeImport = `"github.com/go-rotini/rotini"`

// program is the CLI fully resolved from a spec and conf and ready to emit: the command
// tree, the output layout, and the module it is written into. resolveProgram builds it;
// generate runs the emit steps.
type program struct {
	// pruned names the orphaned generated files this pass removed, reported as notices.
	pruned []string

	// skipPrune leaves orphaned files in place. `rotini init` sets it: init never deletes a file.
	skipPrune bool

	// auditWarnings are auditHooks findings in user-owned handler files: a method that
	// resembles a lifecycle hook but is not one, or a call acquiring another command's
	// inputs type. Reported as notices, never errors, since both are legal Go.
	auditWarnings []error

	// handlerImports are the import paths named by the spec's `handler:` blocks. auditHooks
	// reads the ones inside this module.
	handlerImports map[string]bool

	// Inputs: the validated spec and conf, and the spec's path.
	spec     *Spec
	conf     *Conf
	specPath string

	// Context: the owning module and the resolved output locations.
	module module // the Go module the spec belongs to (root dir + import path)
	layout layout // resolved output locations (the cmd package, the models file, the entrypoint)

	// Resolved command tree: the spec, with $ref children composed, as the renderers consume it.
	rootName        string
	rootDisplay     string // the name rendered pages show: display_name, else rootName
	rootPascal      string
	rootInputs      *Inputs
	rootPlugins     []PluginSpec       // root-level declared plugins
	rootHelp        cmdHelp            // root command's flattened help fields
	rootOutput      *Schema            // root command's output type (nil when unset)
	rootDiscovery   *PluginDiscovery   // root command's plugin discovery (nil = off)
	rootPluginPath  string             // root command's extra plugin directory (both kinds of plugin)
	rootPassthrough bool               // root command's passthrough (raw positionals)
	schemas         map[string]Schema  // document-level named schemas (for output codegen)
	configFiles     []scopedConfigFile // per-command config-file sources, tagged with their command path (for the input reader's cascade)
	envPrefix       string             // document-level env_prefix for DERIVED env-var names
	adoptedPrefixes map[string]string  // env_prefix → child root name, from composed children (see resolveEnvPrefix)

	root         genCommand               // the root command (own)
	own          []genCommand             // inline sub-commands, sorted by prefix
	composed     []composedCmd            // composed sub-commands, sorted by prefix
	tree         []rnode                  // full resolved tree (own + grafted), for the Definition
	childImports []templateHandlersImport // unique child cli imports for the rollup

	// Resolved doc/completion features: blocks embedded in the cmd file (featureBlocks) and
	// per-command pages written to disk (featureOutputs).
	featureBlocks  []templateFeature
	featureOutputs []featureOutput
}

// module is the Go module a spec lives in: the filesystem root (the directory holding
// go.mod) and the module import path. Generated output paths resolve against it.
type module struct {
	root string // absolute dir containing go.mod
	path string // module import path (from go.mod)
}

// resolveProgram resolves a validated spec and conf into a program ready to emit: it
// finds the module, resolves the command tree (expanding $ref children), the output
// layout, and the enabled doc/completion features.
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

// generate runs the emit steps of `rotini generate` in order, stopping at the first error.
func (p *program) generate() error {
	steps := []struct {
		name string
		do   func() error
	}{
		{"emit schemas", p.emitSchemas},
		{"emit contract", p.emitContract},
		{"emit models file", p.emitModelsFile},
		{"emit cmd file", p.emitCmdFile},
		{"emit feature outputs", p.emitFeatures},
		{"emit handler stubs", p.emitStubs},
		{"emit entrypoint", p.emitEntrypoint},
		{"prune orphans", p.prune},
		{"audit handler hooks", p.auditHooks},
	}
	for _, s := range steps {
		if err := s.do(); err != nil {
			return fmt.Errorf("%s: %w", s.name, err)
		}
	}
	return nil
}

// ─── the generate steps ─────────────────────────────────────────────────────────.

// emitSchemas writes rotini's embedded JSON Schemas when generate.schemas is set. It runs
// first so an editor's $schema reference resolves even if a later step fails.
func (p *program) emitSchemas() error { return writeSchemas(p.conf, p.module.root) }

// emitModelsFile writes the typed input/output structs to their own package when the
// conf declares a `models` target. Otherwise it is a no-op and the types stay in the
// cmd file.
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
		Header:      p.layout.modelsHeader,
	})
	if err != nil {
		return err
	}
	return writeGeneratedFile(filepath.Join(p.layout.modelsDir, p.layout.modelsFile), content)
}

// emitCmdFile renders and writes the generated cmd file.
func (p *program) emitCmdFile() error {
	content, err := renderCmdFile(p, p.layout, p.featureBlocks)
	if err != nil {
		return err
	}
	return writeGeneratedFile(filepath.Join(p.layout.cmdDir, p.layout.cmdFile), content)
}

// emitFeatures writes the per-command pages of each embed-mode feature. Inline features
// carry their content in the cmd file and write nothing here.
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

// emitStubs creates each missing handler stub. Existing stubs are never overwritten.
func (p *program) emitStubs() error { return writeHandlerStubs(p, p.layout) }

// emitEntrypoint writes the binary's main.go, create-once, when the conf declares an
// entrypoint.
func (p *program) emitEntrypoint() error {
	return writeEntrypoint(p.layout, string(detectFileFormat(p.specPath)))
}

// prune deletes generated stubs and feature pages the spec no longer declares, sparing
// each package's `keep` paths. When the entrypoint shares the cmd directory, its keep
// list is merged into the cmd pass.
func (p *program) prune() error {
	if p.skipPrune {
		return nil
	}
	var mainKeep []string
	if m := p.conf.Generate.mainPkg(); m != nil {
		mainKeep = m.Keep
	}
	cmdKeep := p.conf.Generate.cmdTarget().Keep
	if p.layout.entrypointDir != "" && p.layout.entrypointDir == p.layout.cmdDir {
		cmdKeep = append(append([]string{}, cmdKeep...), mainKeep...)
	}
	note := func(name string) { p.pruned = append(p.pruned, name) }
	if err := pruneStubs(p, p.layout, cmdKeep, note); err != nil {
		return err
	}
	if err := pruneFeatureOutputs(p.layout, p.conf.Generate.cmdTarget().Keep, p.featureOutputs); err != nil {
		return err
	}
	return pruneEntrypoint(p.layout, mainKeep, note)
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
		if f.desc.manPages {
			// validate checks a single spec; this also covers commands composed from other
			// specs, which exist only once the tree is assembled.
			if problems := manPageCollisions(nodes); len(problems) > 0 {
				return errors.Join(problems...)
			}
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

// writeSchemas writes rotini's embedded JSON Schemas verbatim to the module-root-relative
// paths under generate.schemas, so an editor's `$schema` reference resolves locally. These
// files are never pruned.
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
			return fmt.Errorf("generate.schemas.%s.file %q must be module-root-relative, not absolute", label, sc.File)
		}
		abs := filepath.Join(moduleRoot, rel)
		if r, err := filepath.Rel(moduleRoot, abs); err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
			return fmt.Errorf("generate.schemas.%s.file %q must resolve under the module root", label, sc.File)
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

// featureOutput is one enabled feature's resolved output directory and per-command
// content, used to write and prune that directory.
type featureOutput struct {
	desc        docFeature
	absEmbedDir string // where rendered output files are written (embed mode)
	nodes       []helpNode
	contents    []string // final per-node content (parallel to nodes), rendered/verbatim/stripped
	embed       bool     // true: write contents to files (//go:embed); false: inline in the .go, write no output files
}

// featureConfigs pairs every doc feature with its conf entry (nil when unset). It returns
// nil when conf.Generate is nil.
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

// enabledFeatures returns the enabled doc features in help, man, markdown, completion order.
func enabledFeatures(conf *Conf) []confFeature {
	var out []confFeature
	for _, f := range featureConfigs(conf) {
		if f.cfg != nil && f.cfg.Enabled {
			out = append(out, f)
		}
	}
	return out
}

// renderCmdFile renders the generated cmd file: the ProgramHandlers interface, the typed
// input structs, the Definition, NewProgram, InputSettings, and the rollup (the handlers
// struct, Program, Handlers, and per-command wiring). Handler stubs are separate
// create-once files in the same package.
func renderCmdFile(gp *program, lay layout, features []templateFeature) ([]byte, error) {
	blocks, imports := inputBlocks(gp)

	// With a models target the typed structs live in their own package; this file carries
	// only aliases re-exporting them, so handler code reads the same either way.
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
	if declaresOutput(gp) { // the Definition records each output's Go type
		if imports == nil {
			imports = map[string]bool{}
		}
		imports["reflect"] = true
	}

	return renderRotiniFile(templateRotiniData{
		Package:       lay.cmdPkgName,
		PathResolvers: hasPathResolver(features),
		HelpResolver:  helpResolver(features),
		Header:        lay.cmdHeader,
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
		InputSettings: renderInputSettings(gp),
		Features:      features,
		EmbedImport:   anyEmbed(features),
	})
}

// inputBlocks assembles the per-command typed-struct blocks and the imports their field
// types need. The cmd and models files share it so they cannot drift.
func inputBlocks(gp *program) ([]templateInputBlock, map[string]bool) {
	own := gp.ownCommands()
	blocks := make([]templateInputBlock, 0, len(own))
	imports := map[string]bool{}
	emitted := make(map[string]bool, len(own)) // prefixes whose structs this file declares
	wanted := map[string]bool{}                // prefixes some <Prefix>Inputs names as a field type
	for _, c := range own {
		emitted[c.prefix] = true
		for _, f := range c.inputs {
			wanted[strings.TrimSuffix(f.GoType, "CommandInputs")] = true
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
		c.addImports(imports)
	}

	// A composed node's handler uses the child package's types, so it needs no local
	// block. But an own command grafted beneath it (`commands:` beside the `$ref`) names
	// every ancestor in its <Prefix>Inputs, so the composed ancestor's
	// <Prefix>CommandInputs must exist here. Only composed prefixes actually named are emitted.
	for _, c := range gp.composed {
		if !wanted[c.prefix] || emitted[c.prefix] {
			continue
		}
		emitted[c.prefix] = true
		blocks = append(blocks, templateInputBlock{
			Prefix:    c.prefix,
			Flags:     toTemplateFields(c.flags),
			Arguments: toTemplateFields(c.args),
			Env:       toTemplateFields(c.env),
			Config:    toTemplateFields(c.config),
			// No InputsFields: the composed command's handler reads the child package's type.
		})
		c.addImports(imports)
	}

	return blocks, imports
}

// modelAliases lists every type the models package declares, so the cmd package can
// re-export them. The names must mirror the models template exactly.
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
		out = append(out, b.Prefix+"CommandInputs")
		// A composed ancestor's block declares no <Prefix>Inputs, so there is nothing to alias.
		if len(b.InputsFields) > 0 {
			out = append(out, b.Prefix+"Inputs")
		}
	}
	out = append(out, outputTypeNames(gp)...)
	return out
}

// anyEmbed reports whether any feature emits a //go:embed-backed var, which requires the
// generated file to import embed.
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

// writeHandlerStubs creates a handler stub for the root and every own sub-command whose
// stub file does not exist yet; stubs are user-owned and never overwritten. Composed and
// passthrough commands get no stub, since their handlers live in another package. The
// body is seeded by stubBody.
func writeHandlerStubs(gp *program, lay layout) error {
	helpOn := featureEnabled(gp.conf, "help")
	for _, c := range gp.ownCommands() {
		if c.passthrough {
			continue
		}
		path := filepath.Join(lay.cmdDir, stubFileFor(lay.cmdDir, c))
		if _, err := os.Stat(path); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("stat %s: %w", path, err)
		}
		content, err := renderHandlerStubFile(stubBody(gp, c, lay.cmdPkgName, lay.cmdHeader, helpOn))
		if err != nil {
			return err
		}
		if err := writeGeneratedFile(path, content); err != nil {
			return err
		}
	}
	return nil
}

// helpResolver returns the help feature's resolver name, or "" when the help feature is off.
func helpResolver(features []templateFeature) string {
	for _, f := range features {
		if f.Noun == "help" {
			return f.Resolver
		}
	}
	return ""
}

// hasPathResolver reports whether any enabled feature emits a resolver keyed by command
// path (help, man, markdown) rather than by shell (completion). Only those require the
// generated file to import "strings".
func hasPathResolver(features []templateFeature) bool {
	for _, f := range features {
		if !f.PerShell {
			return true
		}
	}
	return false
}

// featureEnabled reports whether the conf turns on the named generate feature.
func featureEnabled(conf *Conf, kind string) bool {
	for _, f := range enabledFeatures(conf) {
		if f.desc.name == kind {
			return true
		}
	}
	return false
}

// commandName returns the command's own name — the last token of its invocation.
func commandName(invocation string) string {
	if _, name, ok := strings.CutLast(invocation, " "); ok {
		return name
	}
	return invocation
}

// boolFlagField returns the Go field name of c's bool flag with the given logical name
// (fieldDef.Tag), or "" when it declares none.
func boolFlagField(c genCommand, logical string) string {
	for _, f := range c.flags {
		if f.GoType == "bool" && f.Tag == logical {
			return f.Field
		}
	}
	return ""
}

// variadicStringArgField returns the Go field name of this command's single []string
// argument, or "" unless it has exactly one argument and that argument is []string.
func variadicStringArgField(c genCommand) string {
	if len(c.args) != 1 || c.args[0].GoType != "[]string" {
		return ""
	}
	return c.args[0].Field
}

// helpFlagFor finds the `help` flag that asks command c for its page: c's own, else the
// nearest ancestor's, since flags resolve up the command chain (`app sub --help` sets the
// root's flag when sub declares none). It returns the flag's Go field and the inputs frame
// (type prefix) holding it, or "" and "".
func helpFlagFor(gp *program, c genCommand) (field, frame string) {
	if f := boolFlagField(c, "help"); f != "" {
		return f, c.prefix
	}
	byPrefix := map[string]genCommand{}
	for _, oc := range gp.ownCommands() {
		byPrefix[oc.prefix] = oc
	}
	for i := len(c.inputs) - 2; i >= 0; i-- {
		if anc, ok := byPrefix[c.inputs[i].Field]; ok {
			if f := boolFlagField(anc, "help"); f != "" {
				return f, anc.prefix
			}
		}
	}
	return "", ""
}

// stubBody assembles the handler stub's template data, choosing the seeded body from the
// command's declarations. A --help flag is wired only when the help feature is enabled. A
// non-root `help` command with help enabled prints the page for its variadic path argument;
// a non-root `version` command with no arguments prints the version; a dispatch-only root
// prints its help when invoked bare. Every other command gets the default body.
func stubBody(gp *program, c genCommand, pkg, cmdHeader string, helpOn bool) templateHandlerData {
	d := templateHandlerData{
		Package:       pkg,
		HandlerType:   c.handler,
		InputsType:    c.prefix + "Inputs",
		Invocation:    c.invocation,
		Prefix:        c.prefix,
		RuntimeImport: runtimeImport,
		Header:        cmdHeader,
	}
	if helpOn {
		d.HelpFlag, d.HelpFrame = helpFlagFor(gp, c)
	}
	d.VersionFlag = boolFlagField(c, "version")

	isRoot := c.prefix == gp.root.prefix
	switch name := commandName(c.invocation); {
	case name == "help" && !isRoot && helpOn:
		d.HelpPathArg = variadicStringArgField(c)
	case name == "version" && !isRoot && len(c.args) == 0:
		d.VersionOnly = true
	case isRoot && helpOn && len(c.args) == 0 && len(gp.own)+len(gp.composed) > 0:
		// A dispatch-only root shows its help when invoked bare.
		d.PrintHelpWhenBare = true
	}

	// NeedsInputs: the body calls rtx.Inputs, which also reports unknown flags.
	// UsesInputs: the body reads the result; otherwise it is discarded to `_`.
	d.NeedsInputs = d.HelpFlag != "" || d.VersionFlag != "" || d.HelpPathArg != "" ||
		(!d.VersionOnly && !d.PrintHelpWhenBare)
	d.UsesInputs = d.HelpPathArg != "" || (!d.VersionOnly && !d.PrintHelpWhenBare)
	return d
}

// writeEntrypoint writes the binary's main.go to the conf-declared entrypoint package. It
// is create-once: an existing main.go is never overwritten. extension is the spec/conf
// file extension used in the //go:generate directive.
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
	// Alias the generated package as "cmd" so it cannot collide with the rotini runtime.
	content, err := renderMainFile(lay.mainHeader, lay.cmdImport, "cmd", extension)
	if err != nil {
		return err
	}
	return writeGeneratedFile(path, content)
}

// rollupMethods builds the handlers struct's per-command methods, sorted by name: own
// commands return their local stub, passthrough and composed commands delegate to
// another package's handler.
func rollupMethods(gp *program) []templateHandlersMethod {
	methods := make([]templateHandlersMethod, 0, 1+len(gp.own)+len(gp.composed))
	for _, c := range gp.ownCommands() {
		if c.passthrough {
			// Own command (types generated locally) whose handler lives in another package.
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
