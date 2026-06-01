package internal

import (
	"bytes"
	"embed"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"text/template"
	"time"
	"unicode"
)

// Generated programs import the rotini runtime under this path. The framework
// file references rotini.CommandHandlers; the handler rollup references
// rotini.NewProgram and rotini.CommandHandlers.
const (
	rotiniImportPath = "github.com/go-rotini/rotini"
	rotiniPkgName    = "rotini"
)

//go:embed templates/rotini.go.tmpl templates/handler.go.tmpl templates/handlers.go.tmpl templates/main.go.tmpl templates/help.txt.tmpl templates/man.txt.tmpl templates/markdown.md.tmpl
var templateFS embed.FS

// fieldDef is one generated struct field: a Go identifier, its type, and its
// `rotini` struct-tag content — a flag/argument logical name (empty for the
// per-command fields of an <Cmd>Inputs struct, which the binder maps by position).
type fieldDef struct {
	Field  string
	GoType string
	Tag    string
}

// inputBlock is the set of generated input types for a single command. The
// framework file emits four types per block: <Prefix>Flags, <Prefix>Arguments,
// <Prefix>CommandInputs and <Prefix>Inputs.
type inputBlock struct {
	Prefix       string     // PascalCase type prefix, e.g. "RotiniGenerate"
	Flags        []fieldDef // fields of <Prefix>Flags (flags + env variables)
	Arguments    []fieldDef // fields of <Prefix>Arguments
	InputsFields []fieldDef // root + ancestor + self CommandInputs fields of <Prefix>Inputs
}

// genCommand is the fully resolved description of one command node (root or
// sub-command) that the renderers consume.
type genCommand struct {
	prefix   string // PascalCase type prefix, e.g. "RotiniGenerate"
	handler  string // unexported handler struct name, e.g. "rotiniGenerateHandlers"
	filename string // handler stub file name, e.g. "rotini_generate.go"
	flags    []fieldDef
	args     []fieldDef
	inputs   []fieldDef // InputsFields for this command's <Prefix>Inputs
}

// layout holds the resolved package locations and import paths for a single
// generation pass. The framework package and the handler package are kept
// separate (the supported case): the rollup in the handler package imports the
// framework package to reference rtg.ProgramHandlers.
type layout struct {
	frameworkDir     string // absolute output dir for the framework file
	frameworkPkgName string // package name, e.g. "rtg"
	frameworkFile    string // file name, e.g. "rotini.go"
	frameworkImport  string // import path, e.g. "github.com/.../cmd/rotini/rtg"

	handlerDir     string // absolute output dir for stubs + rollup
	handlerPkgName string // package name, e.g. "rth"
	rollupFile     string // rollup file name, e.g. "handlers.go"
}

// generateAll runs a single generation pass: it resolves the spec (expanding
// any composed $ref children), writes the framework file, creates missing
// handler stubs, (re)writes the handler rollup, and prunes orphaned stubs when
// configured. specPath is needed to resolve $ref paths relative to the spec.
func generateAll(spec *Spec, conf *Conf, specPath string) error {
	moduleRoot, moduleName, err := findModule()
	if err != nil {
		return err
	}
	lay, err := resolveLayout(conf, moduleRoot, moduleName)
	if err != nil {
		return err
	}
	gp, err := resolveTree(spec, specPath, moduleRoot, moduleName)
	if err != nil {
		return err
	}

	// For each enabled doc feature (help/man/markdown), the framework file gains
	// embedded "<Prefix>" vars + a resolver, and each command's page is (re)written
	// under that feature's rtg dir — rendered from the command's doc-fields, or
	// written verbatim when the command sets the feature's spec string.
	feats := enabledFeatures(conf)
	frameworks := make([]*helpFramework, 0, len(feats))
	outputs := make([]featureOutput, 0, len(feats))
	for _, f := range feats {
		var nodes []helpNode
		if f.desc.perShell {
			nodes = completionNodes() // completion: per shell, not per command
		} else {
			nodes = flattenFeature(gp, f.desc)
		}
		frameworks = append(frameworks, buildFeatureFramework(nodes, f.cfg.Dir, f.desc))
		outputs = append(outputs, featureOutput{desc: f.desc, dir: f.cfg.Dir, nodes: nodes})
	}

	if err := writeFrameworkFile(gp, lay, frameworks); err != nil {
		return err
	}
	for _, o := range outputs {
		if o.desc.perShell {
			if err := writeCompletionFiles(lay, o.dir, gp.rootName, o.nodes); err != nil {
				return err
			}
			continue
		}
		if err := writeFeatureFiles(lay, o.dir, o.nodes, o.desc); err != nil {
			return err
		}
	}
	if err := writeHandlerStubs(gp, lay); err != nil {
		return err
	}
	if err := writeHandlerRollup(gp, lay); err != nil {
		return err
	}
	// Pruning is implicit (always-on): drop orphaned rth stubs and orphaned rtg
	// feature outputs, sparing only the per-package `keep` paths (and test files
	// and the editable feature templates).
	if err := pruneStubs(gp, lay, conf.Generate.Rth.Keep); err != nil {
		return err
	}
	if err := pruneRtg(lay, conf.Generate.Rtg.Keep, outputs); err != nil {
		return err
	}
	return nil
}

// confFeature pairs a doc-feature descriptor with its conf entry.
type confFeature struct {
	desc docFeature
	cfg  *Feature
}

// featureOutput is one enabled feature's resolved dir + per-command nodes, used
// for writing and pruning its rtg output dir.
type featureOutput struct {
	desc  docFeature
	dir   string
	nodes []helpNode
}

// featureConfigs pairs every doc feature with its conf entry (nil when unset).
// Requires conf.Generate.Rtg to be non-nil (guaranteed after applyConfDefaults).
func featureConfigs(conf *Conf) []confFeature {
	feats := conf.Generate.Rtg.Features
	if feats == nil {
		return nil
	}
	return []confFeature{
		{helpFeatureDesc, feats.Help},
		{manFeatureDesc, feats.Man},
		{markdownFeatureDesc, feats.Markdown},
		{completionFeatureDesc, feats.Completion},
	}
}

// enabledFeatures returns the doc features toggled on, in help→man→markdown order.
func enabledFeatures(conf *Conf) []confFeature {
	var out []confFeature
	for _, f := range featureConfigs(conf) {
		if f.cfg != nil && f.cfg.Enabled {
			out = append(out, f)
		}
	}
	return out
}

// methods returns the ProgramHandlers method names: the root, then every own
// and composed sub-command, sorted.
func (gp *genProgram) methods() []string {
	out := []string{gp.root.prefix}
	for _, c := range gp.own {
		out = append(out, c.prefix)
	}
	for _, c := range gp.composed {
		out = append(out, c.prefix)
	}
	sort.Strings(out)
	return out
}

// inputsFields returns the fields of a command's <Prefix>Inputs struct: one per
// ancestor command (root first, then each intermediate) plus the command itself,
// in root→leaf order. Each field is named after the command's PascalCase prefix
// and typed as that prefix's CommandInputs. There is deliberately no struct tag:
// the binder maps fields to resolved-chain frames by position (aligned at the
// leaf), so command names can never collide along a path.
func inputsFields(rootPascal, path string) []fieldDef {
	fields := []fieldDef{{Field: rootPascal, GoType: rootPascal + "CommandInputs"}}
	segments := strings.Split(path, "_")
	for i := 1; i <= len(segments); i++ {
		prefix := rootPascal + toPascalCase(strings.Join(segments[:i], "_"))
		fields = append(fields, fieldDef{Field: prefix, GoType: prefix + "CommandInputs"})
	}
	return fields
}

// flagFields returns the <Prefix>Flags struct fields for a command's inputs:
// declared flags followed by environment-variable inputs (both are flag-shaped
// in the inputs model). Config-file values and stdin are not yet projected.
func flagFields(in *Inputs) []fieldDef {
	if in == nil {
		return nil
	}
	fields := make([]fieldDef, 0, len(in.Flags)+len(in.Env))
	for _, f := range in.Flags {
		fields = append(fields, fieldDef{Field: toPascalCase(f.Name), GoType: goFieldType(f.Schema), Tag: f.Name})
	}
	for _, v := range in.Env {
		fields = append(fields, fieldDef{Field: toPascalCase(v.Name), GoType: goFieldType(v.Schema), Tag: v.Name})
	}
	return fields
}

// argFields returns the <Prefix>Arguments struct fields for a command's inputs.
func argFields(in *Inputs) []fieldDef {
	if in == nil {
		return nil
	}
	fields := make([]fieldDef, 0, len(in.Arguments))
	for _, a := range in.Arguments {
		fields = append(fields, fieldDef{Field: toPascalCase(a.Name), GoType: goFieldType(a.Schema), Tag: a.Name})
	}
	return fields
}

// goFieldType resolves an input schema to a Go type expression, defaulting to
// string and applying a pointer for nullable inputs.
func goFieldType(schema *InputSchema) string {
	t := "string"
	nullable := false
	if schema != nil {
		if schema.Type != "" {
			t = jsonSchemaTypeToGo(schema.Type)
		}
		nullable = schema.Nullable
	}
	if nullable {
		return "*" + t
	}
	return t
}

// jsonSchemaTypeToGo maps a schema type name to a Go type expression. It
// accepts both JSON Schema standard names and Go names (the schema permits
// both); unknown values pass through unchanged so custom types are usable.
func jsonSchemaTypeToGo(t string) string {
	switch t {
	case "boolean", "bool":
		return "bool"
	case "integer", "int":
		return "int"
	case "number", "float64":
		return "float64"
	case "array", "[]string":
		return "[]string"
	case "object", "map":
		return "map[string]any"
	case "duration":
		return "time.Duration"
	case "time", "datetime", "date":
		return "time.Time"
	default:
		return t
	}
}

// renderDefinition renders the `var Definition = rotini.Definition{…}` literal —
// the compiled command tree the runtime parses against. It is emitted into the
// framework file and gofmt-formatted with the rest of it, so the produced text
// only needs to be valid Go, not pretty.
func renderDefinition(gp *genProgram) string {
	var b strings.Builder
	b.WriteString("var Definition = " + rotiniPkgName + ".Definition{\n")
	b.WriteString("Name: " + strconv.Quote(gp.rootName) + ",\n")
	b.WriteString("Handler: " + strconv.Quote(gp.rootPascal) + ",\n")
	if len(gp.rootAliases) > 0 {
		b.WriteString("Aliases: " + goStringSlice(gp.rootAliases) + ",\n")
	}
	if fl := flagDefsLiteral(gp.rootInputs); fl != "" {
		b.WriteString("Flags: " + fl + ",\n")
	}
	if al := argDefsLiteral(gp.rootInputs); al != "" {
		b.WriteString("Arguments: " + al + ",\n")
	}
	if gp.versionVar != "" {
		b.WriteString("Version: " + gp.versionVar + ",\n")
	}
	if cl := rnodesLiteral(gp.tree); cl != "" {
		b.WriteString("Commands: " + cl + ",\n")
	}
	if rl := remoteDefsLiteral(gp.rootName, gp.rootRemotes); rl != "" {
		b.WriteString("RemoteCommands: " + rl + ",\n")
	}
	b.WriteString("}\n")
	return b.String()
}

// remoteDefsLiteral renders the []rotini.RemoteDef literal for a command's
// remote/co-located sub-commands. The expected binary is "<host>-<name>".
func remoteDefsLiteral(host string, rcs []RemoteCommandSpec) string {
	if len(rcs) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("[]" + rotiniPkgName + ".RemoteDef{\n")
	for _, rc := range rcs {
		b.WriteString("{Name: " + strconv.Quote(rc.Name))
		b.WriteString(", Binary: " + strconv.Quote(host+"-"+rc.Name))
		if len(rc.Aliases) > 0 {
			b.WriteString(", Aliases: " + goStringSlice(rc.Aliases))
		}
		if rc.Timeout != "" {
			if d, err := time.ParseDuration(rc.Timeout); err == nil && d > 0 {
				b.WriteString(fmt.Sprintf(", Timeout: %d", int64(d)))
			}
		}
		b.WriteString("},\n")
	}
	b.WriteString("}")
	return b.String()
}

func flagDefsLiteral(in *Inputs) string {
	if in == nil || len(in.Flags) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("[]" + rotiniPkgName + ".FlagDef{\n")
	for _, f := range in.Flags {
		ids := f.Identifiers
		if len(ids) == 0 {
			ids = []string{"--" + strings.ReplaceAll(f.Name, "_", "-")}
		}
		b.WriteString("{Name: " + strconv.Quote(f.Name) + ", Identifiers: " + goStringSlice(ids))
		b.WriteString(", Type: " + strconv.Quote(schemaType(f.Schema)))
		writeSchemaCommon(&b, f.Schema)
		b.WriteString("},\n")
	}
	b.WriteString("}")
	return b.String()
}

func argDefsLiteral(in *Inputs) string {
	if in == nil || len(in.Arguments) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("[]" + rotiniPkgName + ".ArgDef{\n")
	for _, a := range in.Arguments {
		typ := schemaType(a.Schema)
		b.WriteString("{Name: " + strconv.Quote(a.Name) + ", Type: " + strconv.Quote(typ))
		if strings.HasPrefix(typ, "[]") {
			b.WriteString(", Variadic: true")
		}
		writeSchemaCommon(&b, a.Schema)
		b.WriteString("},\n")
	}
	b.WriteString("}")
	return b.String()
}

// writeSchemaCommon appends the Required/Default/Enum fields shared by FlagDef
// and ArgDef literals, omitting zero values.
func writeSchemaCommon(b *strings.Builder, schema *InputSchema) {
	if schema == nil {
		return
	}
	if schema.Required {
		b.WriteString(", Required: true")
	}
	if d := defaultString(schema.Default); d != "" {
		b.WriteString(", Default: " + strconv.Quote(d))
	}
	if len(schema.Enum) > 0 {
		b.WriteString(", Enum: " + goStringSlice(schema.Enum))
	}
}

// schemaType resolves an input schema to the Definition's type string,
// defaulting to "string".
func schemaType(schema *InputSchema) string {
	if schema != nil && schema.Type != "" {
		return jsonSchemaTypeToGo(schema.Type)
	}
	return "string"
}

// goStringSlice renders a []string{…} literal.
func goStringSlice(ss []string) string {
	quoted := make([]string, len(ss))
	for i, s := range ss {
		quoted[i] = strconv.Quote(s)
	}
	return "[]string{" + strings.Join(quoted, ", ") + "}"
}

// defaultString renders an input's decoded default value as a string.
func defaultString(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case bool:
		return strconv.FormatBool(x)
	case float64:
		return strconv.FormatFloat(x, 'g', -1, 64)
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	default:
		return fmt.Sprintf("%v", x)
	}
}

// writeFrameworkFile renders and writes the framework file: the ProgramHandlers
// aggregate interface plus the typed input structs for every command. It is
// always (over)written — it is fully generated and carries a DO NOT EDIT
// banner.
func writeFrameworkFile(gp *genProgram, lay layout, features []*helpFramework) error {
	own := append([]genCommand{gp.root}, gp.own...)

	blocks := make([]inputBlock, 0, len(own))
	stdImports := map[string]bool{}
	for _, c := range own {
		blocks = append(blocks, inputBlock{
			Prefix:       c.prefix,
			Flags:        c.flags,
			Arguments:    c.args,
			InputsFields: c.inputs,
		})
		for _, f := range c.flags {
			noteStdImport(stdImports, f.GoType)
		}
		for _, a := range c.args {
			noteStdImport(stdImports, a.GoType)
		}
	}

	outputTypes, err := buildOutputTypes(gp, lay.frameworkPkgName)
	if err != nil {
		return err
	}

	data := map[string]any{
		"Package":      lay.frameworkPkgName,
		"RotiniImport": rotiniImportPath,
		"RotiniPkg":    rotiniPkgName,
		"StdImports":   sortedKeys(stdImports),
		"Methods":      gp.methods(),
		"Blocks":       blocks,
		"Definition":   renderDefinition(gp),
		"Metadata":     gp.metadata,
		"Features":     features,
		"OutputTypes":  outputTypes,
	}
	content, err := renderGo("framework", "templates/rotini.go.tmpl", data)
	if err != nil {
		return err
	}
	return writeGeneratedFile(filepath.Join(lay.frameworkDir, lay.frameworkFile), content)
}

// writeHandlerStubs creates a per-command handler stub for the root command and
// every OWN sub-command, but only when the file does not already exist — stubs
// are user-editable, so an existing stub is never overwritten. Composed
// commands have no stub here; their handlers live in the child's package.
func writeHandlerStubs(gp *genProgram, lay layout) error {
	for _, c := range append([]genCommand{gp.root}, gp.own...) {
		path := filepath.Join(lay.handlerDir, c.filename)
		if _, err := os.Stat(path); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("stat %s: %w", path, err)
		}
		data := map[string]any{
			"Package":      lay.handlerPkgName,
			"RotiniImport": rotiniImportPath,
			"RotiniPkg":    rotiniPkgName,
			"HandlerType":  c.handler,
		}
		content, err := renderGo("handler-"+c.handler, "templates/handler.go.tmpl", data)
		if err != nil {
			return err
		}
		if err := writeGeneratedFile(path, content); err != nil {
			return err
		}
	}
	return nil
}

// writeHandlerRollup renders and writes the handler rollup file: the unexported
// handlers struct, the ProgramHandlers assertion, the Program var, the Handlers
// accessor, and one method per command — own commands return a local stub,
// composed commands delegate to the child's rth. It is always (over)written.
func writeHandlerRollup(gp *genProgram, lay layout) error {
	type rollupMethod struct {
		Method         string
		Composed       bool
		HandlerType    string
		DelegateAlias  string
		DelegateMethod string
	}
	var methods []rollupMethod
	for _, c := range append([]genCommand{gp.root}, gp.own...) {
		methods = append(methods, rollupMethod{Method: c.prefix, HandlerType: c.handler})
	}
	for _, c := range gp.composed {
		methods = append(methods, rollupMethod{
			Method:         c.prefix,
			Composed:       true,
			DelegateAlias:  c.delegateAlias,
			DelegateMethod: c.delegateMethod,
		})
	}
	sort.Slice(methods, func(i, j int) bool { return methods[i].Method < methods[j].Method })

	data := map[string]any{
		"Package":         lay.handlerPkgName,
		"RotiniImport":    rotiniImportPath,
		"RotiniPkg":       rotiniPkgName,
		"FrameworkImport": lay.frameworkImport,
		"FrameworkPkg":    lay.frameworkPkgName,
		"ChildImports":    gp.childImports,
		"Methods":         methods,
	}
	content, err := renderGo("rollup", "templates/handlers.go.tmpl", data)
	if err != nil {
		return err
	}
	return writeGeneratedFile(filepath.Join(lay.handlerDir, lay.rollupFile), content)
}

// pruneStubs removes handler .go files that no longer correspond to an own
// command, preserving the rollup file, the keep list, and any test files.
// keepList entries are package-relative paths; rth is a flat package, so for
// its top-level stubs a path is just the file name.
func pruneStubs(gp *genProgram, lay layout, keepList []string) error {
	protected := map[string]bool{
		gp.root.filename: true,
		lay.rollupFile:   true,
	}
	for _, c := range gp.own {
		protected[c.filename] = true
	}
	for _, k := range keepList {
		protected[filepath.ToSlash(k)] = true
	}

	entries, err := os.ReadDir(lay.handlerDir)
	if err != nil {
		return fmt.Errorf("read handler dir %s: %w", lay.handlerDir, err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		if protected[name] {
			continue
		}
		if err := os.Remove(filepath.Join(lay.handlerDir, name)); err != nil {
			return fmt.Errorf("prune %s: %w", name, err)
		}
	}
	return nil
}

// pruneRtg removes orphaned rotini-managed outputs in each enabled feature's rtg
// dir — the per-command pages (matching the feature's extension) for commands no
// longer in the spec. The editable per-feature template, test files, and any
// keep-listed (package-relative) path are preserved. Top-level rtg files (the gen
// file) are never auto-removed. keepList entries are package-relative to rtg.
func pruneRtg(lay layout, keepList []string, outputs []featureOutput) error {
	keep := make(map[string]bool, len(keepList))
	for _, k := range keepList {
		keep[filepath.ToSlash(k)] = true
	}
	for _, o := range outputs {
		// The current command set's pages and the editable template are protected.
		protected := map[string]bool{o.desc.tmplFile: true}
		for _, n := range o.nodes {
			protected[n.file] = true
		}

		dir := filepath.Join(lay.frameworkDir, filepath.FromSlash(o.dir))
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return fmt.Errorf("read %s dir %s: %w", o.desc.name, dir, err)
		}
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || !strings.HasSuffix(name, o.desc.ext) || strings.HasSuffix(name, "_test"+o.desc.ext) {
				continue
			}
			if protected[name] {
				continue
			}
			rel := filepath.ToSlash(filepath.Join(o.dir, name))
			if keep[rel] {
				continue
			}
			if err := os.Remove(filepath.Join(dir, name)); err != nil {
				return fmt.Errorf("prune %s: %w", rel, err)
			}
		}
	}
	return nil
}

// resolveLayout turns the (defaulted) conf package settings into absolute
// output directories, package names, and the framework import path.
func resolveLayout(conf *Conf, moduleRoot, moduleName string) (layout, error) {
	rtg := conf.Generate.Rtg
	rth := conf.Generate.Rth

	rtgPkgDir := filepath.ToSlash(rtg.Package)
	rthPkgDir := filepath.ToSlash(rth.Package)
	if rtgPkgDir == rthPkgDir {
		return layout{}, fmt.Errorf("generate.rtg.package and generate.rth.package must differ (both %q); the merged-package layout is not yet supported", rtgPkgDir)
	}

	return layout{
		frameworkDir:     filepath.Join(moduleRoot, filepath.FromSlash(rtgPkgDir)),
		frameworkPkgName: filepath.Base(rtgPkgDir),
		frameworkFile:    rtg.File,
		frameworkImport:  moduleName + "/" + rtgPkgDir,

		handlerDir:     filepath.Join(moduleRoot, filepath.FromSlash(rthPkgDir)),
		handlerPkgName: filepath.Base(rthPkgDir),
		rollupFile:     rth.File,
	}, nil
}

// applyConfDefaults fills in the sane rotini conf defaults for any unset
// generation settings, so a missing or partial conf still produces the
// companion-CLI layout: framework package "rtg"/rotini.go and handler package
// "rth"/handlers.go.
func applyConfDefaults(conf *Conf) {
	if conf.Generate == nil {
		conf.Generate = &GenerateConfig{}
	}
	if conf.Generate.Rtg == nil {
		conf.Generate.Rtg = &GenerateRtgConfig{}
	}
	if conf.Generate.Rth == nil {
		conf.Generate.Rth = &GenerateRthConfig{}
	}
	rtg := conf.Generate.Rtg
	if rtg.Package == "" {
		rtg.Package = "rtg"
	}
	if rtg.File == "" {
		rtg.File = "rotini.go"
	}
	rth := conf.Generate.Rth
	if rth.Package == "" {
		rth.Package = "rth"
	}
	if rth.File == "" {
		rth.File = "handlers.go"
	}
	// Each present feature defaults its output dir to the feature name.
	for _, f := range featureConfigs(conf) {
		if f.cfg != nil && f.cfg.Dir == "" {
			f.cfg.Dir = f.desc.name
		}
	}
}

// renderGo parses the named embedded template, executes it against data, and
// gofmt-formats the result. A formatting failure includes the unformatted
// source to make template bugs diagnosable.
func renderGo(name, templatePath string, data any) ([]byte, error) {
	src, err := templateFS.ReadFile(templatePath)
	if err != nil {
		return nil, fmt.Errorf("read template %s: %w", templatePath, err)
	}
	tmpl, err := template.New(name).Parse(string(src))
	if err != nil {
		return nil, fmt.Errorf("parse template %s: %w", templatePath, err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("execute template %s: %w", templatePath, err)
	}
	formatted, err := format.Source(buf.Bytes())
	if err != nil {
		return nil, fmt.Errorf("gofmt %s: %w\n--- generated source ---\n%s", name, err, buf.String())
	}
	return formatted, nil
}

// writeGeneratedFile creates dir as needed and writes the generated file.
func writeGeneratedFile(path string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create dir for %s: %w", path, err)
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// findModule walks up from the working directory to the nearest go.mod and
// returns the module root directory and the module path declared in it.
func findModule() (root, name string, err error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", "", fmt.Errorf("get working directory: %w", err)
	}
	for {
		goMod := filepath.Join(dir, "go.mod")
		if data, statErr := os.ReadFile(goMod); statErr == nil {
			for _, line := range strings.Split(string(data), "\n") {
				line = strings.TrimSpace(line)
				if after, ok := strings.CutPrefix(line, "module "); ok {
					return dir, strings.TrimSpace(after), nil
				}
			}
			return "", "", fmt.Errorf("no module path in %s", goMod)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", "", fmt.Errorf("go.mod not found in any parent of working directory")
		}
		dir = parent
	}
}

// noteStdImport records the standard-library import a Go type expression needs
// (currently only the time package, for time.Duration / time.Time fields).
func noteStdImport(set map[string]bool, goType string) {
	if strings.Contains(goType, "time.") {
		set["time"] = true
	}
}

// sortedKeys returns the keys of set in sorted order.
func sortedKeys(set map[string]bool) []string {
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// toPascalCase converts a name to PascalCase, treating '-', '_' and ' ' as word
// boundaries (e.g. "foo_bar" -> "FooBar", "generate" -> "Generate").
func toPascalCase(s string) string {
	var b strings.Builder
	capitalize := true
	for _, r := range s {
		if r == '-' || r == '_' || r == ' ' {
			capitalize = true
			continue
		}
		if capitalize {
			b.WriteRune(unicode.ToUpper(r))
			capitalize = false
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// lowerFirst returns s with its first rune lower-cased.
func lowerFirst(s string) string {
	if s == "" {
		return ""
	}
	r := []rune(s)
	r[0] = unicode.ToLower(r[0])
	return string(r)
}
