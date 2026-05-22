package internal

import (
	"bytes"
	"embed"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/template"
	"unicode"
)

// Generated programs import the rotini runtime under this path. The framework
// file references rotini.CommandHandlers; the handler rollup references
// rotini.NewProgram and rotini.CommandHandlers.
const (
	rotiniImportPath = "github.com/go-rotini/rotini"
	rotiniPkgName    = "rotini"
)

//go:embed templates/rotini.go.tmpl templates/handler.go.tmpl templates/handlers.go.tmpl
var templateFS embed.FS

// fieldDef is one generated struct field: a Go identifier and its type.
type fieldDef struct {
	Field  string
	GoType string
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

// generateAll runs a single generation pass: it writes the framework file,
// creates any missing handler stubs, (re)writes the handler rollup, and prunes
// orphaned stubs when configured.
func generateAll(spec *Spec, conf *Conf) error {
	moduleRoot, moduleName, err := findModule()
	if err != nil {
		return err
	}

	lay, err := resolveLayout(conf, moduleRoot, moduleName)
	if err != nil {
		return err
	}

	root, cmds := buildCommands(spec)

	if err := writeFrameworkFile(lay, root, cmds); err != nil {
		return err
	}
	if err := writeHandlerStubs(lay, root, cmds); err != nil {
		return err
	}
	if err := writeHandlerRollup(lay, root, cmds); err != nil {
		return err
	}
	if c := conf.Generate.Cmd; c != nil && c.Prune != nil && c.Prune.Enabled {
		if err := pruneStubs(lay, root, cmds, c.Prune.Keep); err != nil {
			return err
		}
	}
	return nil
}

// buildCommands resolves the spec into the root command plus a slice of
// sub-commands sorted by their underscore-joined path. Sorting makes the
// generated output deterministic regardless of spec ordering.
func buildCommands(spec *Spec) (genCommand, []genCommand) {
	rootName := spec.Name
	rootPascal := toPascalCase(rootName)

	root := genCommand{
		prefix:   rootPascal,
		handler:  lowerFirst(rootPascal) + "Handlers",
		filename: rootName + ".go",
		flags:    flagFields(spec.Inputs),
		args:     argFields(spec.Inputs),
		inputs:   []fieldDef{{Field: rootPascal, GoType: rootPascal + "CommandInputs"}},
	}

	var nodes []struct {
		path string
		cmd  Command
	}
	var walk func(cmds []Command, parent string)
	walk = func(cmds []Command, parent string) {
		for _, c := range cmds {
			path := c.Name
			if parent != "" {
				path = parent + "_" + c.Name
			}
			nodes = append(nodes, struct {
				path string
				cmd  Command
			}{path, c})
			walk(c.Commands, path)
		}
	}
	walk(spec.Commands, "")

	sort.Slice(nodes, func(i, j int) bool { return nodes[i].path < nodes[j].path })

	cmds := make([]genCommand, 0, len(nodes))
	for _, n := range nodes {
		prefix := rootPascal + toPascalCase(n.path)
		cmds = append(cmds, genCommand{
			prefix:   prefix,
			handler:  lowerFirst(rootPascal) + toPascalCase(n.path) + "Handlers",
			filename: rootName + "_" + n.path + ".go",
			flags:    flagFields(n.cmd.Inputs),
			args:     argFields(n.cmd.Inputs),
			inputs:   inputsFields(rootPascal, n.path),
		})
	}
	return root, cmds
}

// inputsFields returns the fields of a command's <Prefix>Inputs struct: one
// per ancestor command (root first, then each intermediate) plus the command
// itself, each field named after the command's PascalCase prefix and typed as
// that prefix's CommandInputs.
func inputsFields(rootPascal, path string) []fieldDef {
	prefixes := []string{rootPascal}
	segments := strings.Split(path, "_")
	for i := 1; i <= len(segments); i++ {
		sub := strings.Join(segments[:i], "_")
		prefixes = append(prefixes, rootPascal+toPascalCase(sub))
	}
	fields := make([]fieldDef, 0, len(prefixes))
	for _, p := range prefixes {
		fields = append(fields, fieldDef{Field: p, GoType: p + "CommandInputs"})
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
	fields := make([]fieldDef, 0, len(in.Flags)+len(in.Variables))
	for _, f := range in.Flags {
		fields = append(fields, fieldDef{Field: toPascalCase(f.Name), GoType: goFieldType(f.Schema)})
	}
	for _, v := range in.Variables {
		fields = append(fields, fieldDef{Field: toPascalCase(v.Name), GoType: goFieldType(v.Schema)})
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
		fields = append(fields, fieldDef{Field: toPascalCase(a.Name), GoType: goFieldType(a.Schema)})
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

// writeFrameworkFile renders and writes the framework file: the ProgramHandlers
// aggregate interface plus the typed input structs for every command. It is
// always (over)written — it is fully generated and carries a DO NOT EDIT
// banner.
func writeFrameworkFile(lay layout, root genCommand, cmds []genCommand) error {
	all := append([]genCommand{root}, cmds...)

	methods := make([]string, 0, len(all))
	blocks := make([]inputBlock, 0, len(all))
	stdImports := map[string]bool{}
	for _, c := range all {
		methods = append(methods, c.prefix)
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

	data := map[string]any{
		"Package":      lay.frameworkPkgName,
		"RotiniImport": rotiniImportPath,
		"RotiniPkg":    rotiniPkgName,
		"StdImports":   sortedKeys(stdImports),
		"Methods":      methods,
		"Blocks":       blocks,
	}
	content, err := renderGo("framework", "templates/rotini.go.tmpl", data)
	if err != nil {
		return err
	}
	return writeGeneratedFile(filepath.Join(lay.frameworkDir, lay.frameworkFile), content)
}

// writeHandlerStubs creates a per-command handler stub for the root command and
// every sub-command, but only when the file does not already exist — stubs are
// user-editable, so an existing stub is never overwritten.
func writeHandlerStubs(lay layout, root genCommand, cmds []genCommand) error {
	for _, c := range append([]genCommand{root}, cmds...) {
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
// handlers struct, the ProgramHandlers compile-time assertion, the Program var,
// and one method per command returning its stub. It is always (over)written.
func writeHandlerRollup(lay layout, root genCommand, cmds []genCommand) error {
	type rollupMethod struct {
		Method      string
		HandlerType string
	}
	methods := make([]rollupMethod, 0, len(cmds)+1)
	for _, c := range append([]genCommand{root}, cmds...) {
		methods = append(methods, rollupMethod{Method: c.prefix, HandlerType: c.handler})
	}

	data := map[string]any{
		"Package":         lay.handlerPkgName,
		"RotiniImport":    rotiniImportPath,
		"RotiniPkg":       rotiniPkgName,
		"FrameworkImport": lay.frameworkImport,
		"FrameworkPkg":    lay.frameworkPkgName,
		"Methods":         methods,
	}
	content, err := renderGo("rollup", "templates/handlers.go.tmpl", data)
	if err != nil {
		return err
	}
	return writeGeneratedFile(filepath.Join(lay.handlerDir, lay.rollupFile), content)
}

// pruneStubs removes handler .go files that no longer correspond to a command,
// preserving the rollup file, the keep list, and any test files.
func pruneStubs(lay layout, root genCommand, cmds []genCommand, keepList []string) error {
	protected := map[string]bool{
		root.filename:  true,
		lay.rollupFile: true,
	}
	for _, c := range cmds {
		protected[c.filename] = true
	}
	for _, k := range keepList {
		protected[k] = true
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

// resolveLayout turns the (defaulted) conf package settings into absolute
// output directories, package names, and the framework import path.
func resolveLayout(conf *Conf, moduleRoot, moduleName string) (layout, error) {
	fw := conf.Generate.Framework
	cmd := conf.Generate.Cmd

	fwPkgDir := filepath.ToSlash(fw.Package)
	cmdPkgDir := filepath.ToSlash(cmd.Package)
	if fwPkgDir == cmdPkgDir {
		return layout{}, fmt.Errorf("generate.framework.package and generate.cmd.package must differ (both %q); the merged-package layout is not yet supported", fwPkgDir)
	}

	return layout{
		frameworkDir:     filepath.Join(moduleRoot, filepath.FromSlash(fwPkgDir)),
		frameworkPkgName: filepath.Base(fwPkgDir),
		frameworkFile:    fw.GenFile,
		frameworkImport:  moduleName + "/" + fwPkgDir,

		handlerDir:     filepath.Join(moduleRoot, filepath.FromSlash(cmdPkgDir)),
		handlerPkgName: filepath.Base(cmdPkgDir),
		rollupFile:     cmd.GenFile,
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
	if conf.Generate.Framework == nil {
		conf.Generate.Framework = &GenerateFrameworkConfig{}
	}
	if conf.Generate.Cmd == nil {
		conf.Generate.Cmd = &GenerateCmdConfig{}
	}
	fw := conf.Generate.Framework
	if fw.Package == "" {
		fw.Package = "rtg"
	}
	if fw.GenFile == "" {
		fw.GenFile = "rotini.go"
	}
	cmd := conf.Generate.Cmd
	if cmd.Package == "" {
		cmd.Package = "rth"
	}
	if cmd.GenFile == "" {
		cmd.GenFile = "handlers.go"
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
