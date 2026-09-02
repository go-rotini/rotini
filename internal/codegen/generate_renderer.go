package codegen

// This file owns template parsing and rendering: every generated artifact —
// seed spec/conf files, the main.go entrypoint, handler stubs and seeds, the
// handlers rollup, the framework (rotini) file, and the help/man doc pages —
// renders through here. Reading inputs lives in reader.go; writing outputs
// lives in writer.go.

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"go/format"
	"strings"
	"text/tabwriter"
	"text/template"

	"github.com/go-rotini/jsonc"
	"github.com/go-rotini/toml"
	"github.com/go-rotini/yaml"
)

var (
	//go:embed templates/.rotini.spec.yaml.tmpl
	templateSpec string
	//go:embed templates/.rotini.conf.yaml.tmpl
	templateConf string
	//go:embed templates/main.go.tmpl
	templateMain string
	//go:embed templates/handler.go.tmpl
	templateHandlerStub string
	//go:embed templates/rotini.go.tmpl
	templateRotini string

	//go:embed templates/models.go.tmpl
	templateModels string
	//go:embed templates/help.txt.tmpl
	templateHelp string
	//go:embed templates/man.txt.tmpl
	templateMan string
	//go:embed templates/markdown.md.tmpl
	templateMarkdown string
)

// convert transcodes a rendered YAML document to the target serialization.
// YAML — the authoring format — is returned verbatim; json and jsonc become
// pretty-printed JSON (a valid JSONC document); toml is transcoded through
// JSON. Conversion goes through an untyped value, so it carries every field
// the template declares.
func convert(yamlBytes []byte, target fileFormat) ([]byte, error) {
	if target == formatYAML {
		return yamlBytes, nil
	}

	jsonBytes, err := yaml.ToJSON(yamlBytes)
	if err != nil {
		return nil, fmt.Errorf("convert seed to json: %w", err)
	}

	switch target {
	case formatJSON:
		var v any
		if err := json.Unmarshal(jsonBytes, &v); err != nil {
			return nil, fmt.Errorf("decode json: %w", err)
		}
		out, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			return nil, fmt.Errorf("encode json: %w", err)
		}
		return append(out, '\n'), nil
	case formatJSONC:
		var v any
		if err := jsonc.Unmarshal(jsonBytes, &v); err != nil {
			return nil, fmt.Errorf("decode jsonc: %w", err)
		}
		out, err := jsonc.MarshalIndent(v, "  ")
		if err != nil {
			return nil, fmt.Errorf("encode jsonc: %w", err)
		}
		return append(out, '\n'), nil
	case formatTOML:
		out, err := toml.FromJSON(jsonBytes)
		if err != nil {
			return nil, fmt.Errorf("convert to toml: %w", err)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("%w: %s", errUnsupportedFormat, target)
	}
}

func renderTemplate(name, text string, data any) ([]byte, error) {
	tmpl, err := template.New(name).Funcs(templateFuncMap()).Parse(text)
	if err != nil {
		return nil, fmt.Errorf("parse %s template: %w", name, err)
	}

	var buffer bytes.Buffer
	if err := tmpl.Execute(&buffer, data); err != nil {
		return nil, fmt.Errorf("render %s template: %w", name, err)
	}

	return buffer.Bytes(), nil
}

// renderGoFile renders a Go source template, gofmt-formats the result, and
// groups its imports, so templates need no whitespace gymnastics and malformed
// output fails at render time. A formatting failure includes the unformatted
// source to make template bugs diagnosable.
func renderGoFile(name, text string, data any) ([]byte, error) {
	rendered, err := renderTemplate(name, text, data)
	if err != nil {
		return nil, err
	}

	formatted, err := format.Source(rendered)
	if err != nil {
		return nil, fmt.Errorf("gofmt %s: %w\n--- generated source ---\n%s", name, err, rendered)
	}

	return groupImports(formatted)
}

// templateSeedData is the context for the spec and conf seed templates. The seed
// is MINIMAL: a root-only spec and a conf declaring the entrypoint + packages with
// every feature off. `rotini init` runs the standard generate over it, producing a
// ready-to-build root-only CLI the author grows from there.
type templateSeedData struct {
	Version string
	Package string
}

// renderSeedFile renders one YAML seed template and transcodes it to the
// requested file format.
func renderSeedFile(name, text, version, pkg string, target fileFormat) ([]byte, error) {
	rendered, err := renderTemplate(name, text, templateSeedData{
		Version: version,
		Package: pkg,
	})
	if err != nil {
		return nil, err
	}

	return convert(rendered, target)
}

func renderSpecFile(version, pkg string, target fileFormat) ([]byte, error) {
	return renderSeedFile("spec", templateSpec, version, pkg, target)
}

func renderConfFile(version, pkg string, target fileFormat) ([]byte, error) {
	return renderSeedFile("conf", templateConf, version, pkg, target)
}

type templateMainData struct {
	Package      string
	PackageAlias string
	Extension    string // spec/conf file extension for the //go:generate directive, e.g. "yaml"
}

func renderMainFile(pkg, pkgAlias, extension string) ([]byte, error) {
	return renderGoFile("main", templateMain, templateMainData{
		Package:      pkg,
		PackageAlias: pkgAlias,
		Extension:    extension,
	})
}

// templateHandlerData is the per-command handler stub context: every generated
// command gets an empty stub (the end-user wires it). RuntimeImport is the
// emitted-runtime import line (the stub references rotini.Context / rotini.Handlers).
type templateHandlerData struct {
	Package       string
	HandlersType  string
	RuntimeImport string
}

func renderHandlerStubFile(pkg, handlersType, runtimeImport string) ([]byte, error) {
	return renderGoFile("handler", templateHandlerStub, templateHandlerData{
		Package:       pkg,
		HandlersType:  handlersType,
		RuntimeImport: runtimeImport,
	})
}

// templateHandlersImport is one child cmd package import folded into the generated
// cli file's rollup; composed commands delegate to the child's Handlers().
type templateHandlersImport struct {
	Alias string
	Path  string
}

// templateHandlersMethod is one ProgramHandlers method in the generated rollup:
// own commands return a local handler type, composed commands delegate to the
// child's cmd package. (The rollup is folded into the cli file — see templateRotiniData.)
type templateHandlersMethod struct {
	Method         string
	Composed       bool
	Passthrough    bool   // W9: delegate via alias.method() instead of alias.Handlers().method()
	HandlerType    string // own commands: the local handler struct name
	DelegateAlias  string // composed commands: the child import alias
	DelegateMethod string // composed commands: the child's ProgramHandlers method (or W9 convention)
}

// templateInputField is one generated input struct field (flag, argument, env,
// config, or a <Prefix>Inputs field). Tag is the complete struct-tag literal,
// backticks included ("" when the field carries no tag) — see inputFieldTag.
type templateInputField struct {
	Field   string
	GoType  string
	Tag     string
	Comment string // optional trailing line-comment ("" for none)
}

// inputFieldTag assembles a complete struct-tag literal for a generated input
// field from its fieldDef: the rotini tag plus the optional recon / env /
// envnest / cfgfile / constraint tags. Constraint is pre-rendered
// space-separated tags (e.g. `min:"1" max:"65535"`); every part but Tag may
// be empty.
func inputFieldTag(f fieldDef) string {
	tag := fmt.Sprintf("rotini:%q", f.Tag)
	if f.Recon != "" {
		tag += fmt.Sprintf(" recon:%q", f.Recon)
	}
	if f.EnvVar != "" {
		tag += fmt.Sprintf(" env:%q", f.EnvVar)
	}
	if f.EnvNest != "" {
		tag += fmt.Sprintf(" envnest:%q", f.EnvNest)
	}
	if f.CfgFile != "" {
		tag += fmt.Sprintf(" cfgfile:%q", f.CfgFile)
	}
	if f.Constraint != "" {
		tag += " " + f.Constraint
	}
	return "`" + tag + "`"
}

// templateInputBlock is the set of generated input types for a single command:
// <Prefix>Flags, <Prefix>Arguments, optional <Prefix>Env / <Prefix>Config,
// <Prefix>CommandInputs, and <Prefix>Inputs.
type templateInputBlock struct {
	Prefix       string // PascalCase type prefix, e.g. "RotiniGenerate"
	Flags        []templateInputField
	Arguments    []templateInputField
	Env          []templateInputField
	Config       []templateInputField
	StdinType    string // Stdin field type (e.g. "*RotiniGenerateStdin"); "" when none
	StdinFormat  string // stdin decode format for the Stdin field's tag (e.g. "yaml")
	InputsFields []templateInputField
}

// templateFeatureVar / templateFeatureCase / templateFeature are the data the
// rotini template ranges over to emit a feature's embed vars and resolver.
type templateFeatureVar struct {
	Name    string // Go var name, e.g. "HelpRotiniGenerate"
	Embed   string // //go:embed path (embed mode), e.g. "help/rotini_generate.txt"; "" in inline mode
	Literal string // Go string literal of the content (inline mode), e.g. `"Usage:\n…"`; "" in embed mode
}

type templateFeatureCase struct {
	PathsLiteral string // case values, e.g. `"generate", "gen"` (root: `""`)
	Var          string // the var returned for these paths
}

type templateFeature struct {
	Resolver string // resolver func name, e.g. "Help"/"Man"/"Completion"
	Noun     string // word used in the doc comment + error, e.g. "help"
	PerShell bool   // completion: resolver takes a shell string, not a command path
	Vars     []templateFeatureVar
	Cases    []templateFeatureCase
}

type templateRotiniData struct {
	Package       string
	RuntimeImport string                   // emitted-runtime import line (identifier `rotini`)
	Imports       []string                 // pre-rendered import lines (aliased form "alias \"path\"")
	ChildImports  []templateHandlersImport // composed-child cmd packages the rollup delegates to
	Methods       []string                 // ProgramHandlers method names, e.g. "RotiniGenerate"
	RollupMethods []templateHandlersMethod // the generated handlers struct's command→handler methods
	Definition    string                   // pre-rendered definition var declaration
	ModelsImport  string                   // models package import line; "" unless the types were split out
	ModelAliases  []string                 // model type names re-exported here as aliases; empty unless split
	Blocks        []templateInputBlock
	OutputTypes   string // pre-rendered output type declarations; "" when none
	BindMeta      string // pre-rendered bind metadata; "" when none
	Features      []templateFeature
	EmbedImport   bool // emit `import _ "embed"` — only when some feature uses //go:embed
}

func renderRotiniFile(data templateRotiniData) ([]byte, error) {
	return renderGoFile("rotini", templateRotini, data)
}

// templateModelsData is the models file: nothing but the typed input and output
// structs, so the package it declares can be imported from anywhere — including a
// handler package the cmd package itself imports.
type templateModelsData struct {
	Package     string
	Imports     []string // pre-rendered import lines for the field types
	Blocks      []templateInputBlock
	OutputTypes string // pre-rendered output type declarations; "" when none
}

func renderModelsFile(data templateModelsData) ([]byte, error) {
	return renderGoFile("models", templateModels, data)
}

// templateDocHeadings holds the resolved section headings (defaults applied).
// Each value is rendered verbatim — the trailing ":" lives in the value, so an
// override can drop or restyle it.
type templateDocHeadings struct {
	Usage, Commands, Arguments, Flags, Environment, Configuration, Cascading, Examples string
}

// templateDocCommandGroup is one bucket of sub-commands in the Commands
// section. Title is the command's `group` value; "" is the ungrouped bucket,
// which the template heads with its own default.
type templateDocCommandGroup struct {
	Title    string
	Commands []templateDocCommandRow
}

type templateDocCommandRow struct {
	Name       string
	Summary    string
	Aliases    []string
	Group      string // the child command's `group` (buckets it in the Commands section)
	Deprecated string
}

type templateDocArgumentRow struct {
	Name       string
	Summary    string
	Required   bool
	Variadic   bool
	Default    string
	Enum       []string
	Deprecated string
}

type templateDocFlagRow struct {
	Identifiers []string
	Summary     string
	Type        string // "" for bool flags
	Required    bool
	Default     string
	Enum        []string
	Deprecated  string
}

type templateDocEnvRow struct {
	Var        string
	Summary    string
	Type       string
	Required   bool
	Default    string
	Enum       []string
	Deprecated string
}

type templateDocConfigRow struct {
	Name       string
	Location   string // "<file>.<key>" / "<key>" — where the value is read from
	Summary    string
	Type       string
	Required   bool
	Default    string
	Enum       []string
	Deprecated string
}

type templateDocExitRow struct {
	Code    int
	Summary string
}

// templateHelpData is the per-command doc-data context. The help and man
// templates render the same data; man additionally renders ExitStatus and SeeAlso.
type templateHelpData struct {
	Header        string
	Invocation    string // full command path, e.g. "rotini generate"
	Summary       string
	Description   string
	Usage         string // declarative usage override ("" when unset)
	UsageDerived  string // always-computed usage line
	Footer        string
	Headings      templateDocHeadings
	CommandGroups []templateDocCommandGroup
	Arguments     []templateDocArgumentRow
	Flags         []templateDocFlagRow
	Environment   []templateDocEnvRow
	Configuration []templateDocConfigRow
	Cascading     []templateDocFlagRow
	Examples      []string
	ExitStatus    []templateDocExitRow
	SeeAlso       []string
}

// parseDocTemplate parses doc-template text (help/man) with the shared FuncMap.
func parseDocTemplate(name, text string) (*template.Template, error) {
	tmpl, err := template.New(name).Funcs(templateFuncMap()).Parse(text)
	if err != nil {
		return nil, fmt.Errorf("parse %s template: %w", name, err)
	}
	return tmpl, nil
}

// renderDocText renders one doc page (help/man): sanitize the row text, execute
// the template, align tab-separated columns, and tidy the result. It is a pure
// function of (tmpl, data) so repeated passes produce byte-identical output.
func renderDocText(tmpl *template.Template, data templateHelpData) (string, error) {
	var buffer bytes.Buffer
	if err := tmpl.Execute(&buffer, sanitizeDocData(data)); err != nil {
		return "", fmt.Errorf("execute %s template: %w", tmpl.Name(), err)
	}

	return tidy(tabAlign(buffer.String())), nil
}

// sanitizeDocData replaces tabs/newlines in row text (which would corrupt
// tabwriter columns) with spaces. Block fields (Header/Description/Footer/
// Usage) are left intact. Row slices are copied so the source data is not
// mutated.
func sanitizeDocData(d templateHelpData) templateHelpData {
	clean := func(s string) string {
		return strings.ReplaceAll(strings.ReplaceAll(s, "\t", " "), "\n", " ")
	}
	d.CommandGroups = append([]templateDocCommandGroup(nil), d.CommandGroups...)
	for i := range d.CommandGroups {
		d.CommandGroups[i].Commands = append([]templateDocCommandRow(nil), d.CommandGroups[i].Commands...)
		for j := range d.CommandGroups[i].Commands {
			d.CommandGroups[i].Commands[j].Summary = clean(d.CommandGroups[i].Commands[j].Summary)
			d.CommandGroups[i].Commands[j].Deprecated = clean(d.CommandGroups[i].Commands[j].Deprecated)
		}
	}
	d.Arguments = append([]templateDocArgumentRow(nil), d.Arguments...)
	for i := range d.Arguments {
		d.Arguments[i].Summary = clean(d.Arguments[i].Summary)
		d.Arguments[i].Deprecated = clean(d.Arguments[i].Deprecated)
	}
	cleanFlags := func(rows []templateDocFlagRow) []templateDocFlagRow {
		rows = append([]templateDocFlagRow(nil), rows...)
		for i := range rows {
			rows[i].Summary = clean(rows[i].Summary)
			rows[i].Deprecated = clean(rows[i].Deprecated)
		}
		return rows
	}
	d.Flags = cleanFlags(d.Flags)
	d.Cascading = cleanFlags(d.Cascading)
	d.Environment = append([]templateDocEnvRow(nil), d.Environment...)
	for i := range d.Environment {
		d.Environment[i].Summary = clean(d.Environment[i].Summary)
		d.Environment[i].Deprecated = clean(d.Environment[i].Deprecated)
	}
	d.Configuration = append([]templateDocConfigRow(nil), d.Configuration...)
	for i := range d.Configuration {
		d.Configuration[i].Summary = clean(d.Configuration[i].Summary)
		d.Configuration[i].Deprecated = clean(d.Configuration[i].Deprecated)
	}
	d.ExitStatus = append([]templateDocExitRow(nil), d.ExitStatus...)
	for i := range d.ExitStatus {
		d.ExitStatus[i].Summary = clean(d.ExitStatus[i].Summary)
	}
	d.SeeAlso = append([]string(nil), d.SeeAlso...)
	for i := range d.SeeAlso {
		d.SeeAlso[i] = clean(d.SeeAlso[i])
	}
	return d
}

// tabAlign aligns each contiguous block of tab-separated lines with tabwriter.
func tabAlign(s string) string {
	var buffer bytes.Buffer
	tw := tabwriter.NewWriter(&buffer, 0, 0, 4, ' ', 0)
	if _, err := tw.Write([]byte(s)); err != nil {
		panic(err) // unreachable: writes to a bytes.Buffer cannot fail
	}
	if err := tw.Flush(); err != nil {
		panic(err) // unreachable: flushing to a bytes.Buffer cannot fail
	}
	return buffer.String()
}

// tidy trims trailing whitespace per line and collapses runs of blank lines to
// a single blank line, then strips leading and trailing blank lines entirely —
// the rendered page ends exactly at its last line of content, with no trailing
// newline.
func tidy(s string) string {
	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " \t")
	}
	out := strings.Join(lines, "\n")
	for strings.Contains(out, "\n\n\n") {
		out = strings.ReplaceAll(out, "\n\n\n", "\n\n")
	}
	return strings.Trim(out, "\n")
}

// templateFuncMap is the deterministic, dependency-free helper set available to
// every template (an allowlist — no clock/entropy funcs exist to call, keeping
// rendering byte-stable).
func templateFuncMap() template.FuncMap {
	return template.FuncMap{
		"join":       strings.Join,
		"upper":      strings.ToUpper,
		"lower":      strings.ToLower,
		"title":      titleASCII,
		"trim":       strings.TrimSpace,
		"trimPrefix": func(prefix, s string) string { return strings.TrimPrefix(s, prefix) },
		"trimSuffix": func(suffix, s string) string { return strings.TrimSuffix(s, suffix) },
		"replace":    func(old, repl, s string) string { return strings.ReplaceAll(s, old, repl) },
		"indent":     indentLines,
		"repeat":     func(n int, s string) string { return strings.Repeat(s, n) },
		"default": func(def, s string) string {
			if s == "" {
				return def
			}
			return s
		},
		"contains":  func(substr, s string) bool { return strings.Contains(s, substr) },
		"hasPrefix": func(prefix, s string) bool { return strings.HasPrefix(s, prefix) },
		"hasSuffix": func(suffix, s string) bool { return strings.HasSuffix(s, suffix) },
		"first": func(elems []string) string {
			if len(elems) == 0 {
				return ""
			}
			return elems[0]
		},
		"last": func(elems []string) string {
			if len(elems) == 0 {
				return ""
			}
			return elems[len(elems)-1]
		},
	}
}

// titleASCII upper-cases the first letter of each word (ASCII only). A local
// implementation: strings.Title is deprecated and golang.org/x/text would add
// a dependency.
func titleASCII(s string) string {
	var b strings.Builder
	atWordStart := true
	for _, r := range s {
		if atWordStart && r >= 'a' && r <= 'z' {
			b.WriteRune(r - ('a' - 'A'))
		} else {
			b.WriteRune(r)
		}
		atWordStart = r == ' ' || r == '\t' || r == '-' || r == '_'
	}
	return b.String()
}

// indentLines prefixes every non-empty line of s with n spaces.
func indentLines(n int, s string) string {
	pad := strings.Repeat(" ", n)
	lines := strings.Split(s, "\n")
	for i, ln := range lines {
		if ln != "" {
			lines[i] = pad + ln
		}
	}
	return strings.Join(lines, "\n")
}
