package codegen

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"text/tabwriter"
	"text/template"

	"github.com/go-rotini/jsonc"
	"github.com/go-rotini/toml"
	"github.com/go-rotini/yaml"
)

type FileFormat string

const (
	FileFormatJSON  FileFormat = "json"
	FileFormatJSONC FileFormat = "jsonc"
	FileFormatTOML  FileFormat = "toml"
	FileFormatYAML  FileFormat = "yaml"
)

var (
	//go:embed templates/.rotini.spec.yaml.tmpl
	templateSpec string
	//go:embed templates/.rotini.conf.yaml.tmpl
	templateConf string
	//go:embed templates/main.go.tmpl
	templateMain string
	//go:embed templates/handler_stub.go.tmpl
	templateHandlerStub string
	//go:embed templates/handler_root.go.tmpl
	templateHandlerRoot string
	//go:embed templates/handler_version.go.tmpl
	templateHandlerVersion string
	//go:embed templates/handler_help.go.tmpl
	templateHandlerHelp string
	//go:embed templates/handlers.go.tmpl
	templateHandlers string
	//go:embed templates/rotini.go.tmpl
	templateRotini string
	//go:embed templates/help.txt.tmpl
	templateHelp string
	//go:embed templates/man.txt.tmpl
	templateMan string

	ErrUnsupportedFileFormat = errors.New("unsupported spec file format")
)

func convert(yamlBytes []byte, fileFormat FileFormat) ([]byte, error) {
	if fileFormat == FileFormatYAML {
		return yamlBytes, nil
	}

	jsonBytes, err := yaml.ToJSON(yamlBytes)
	if err != nil {
		return nil, fmt.Errorf("convert seed to json: %w", err)
	}

	switch fileFormat {
	case FileFormatJSON:
		var v any
		if err := json.Unmarshal(jsonBytes, &v); err != nil {
			return nil, fmt.Errorf("decode json: %w", err)
		}
		out, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			return nil, fmt.Errorf("encode json: %w", err)
		}
		return append(out, '\n'), nil
	case FileFormatJSONC:
		var v any
		if err := jsonc.Unmarshal(jsonBytes, &v); err != nil {
			return nil, fmt.Errorf("decode jsonc: %w", err)
		}
		out, err := jsonc.MarshalIndent(v, "  ")
		if err != nil {
			return nil, fmt.Errorf("encode jsonc: %w", err)
		}
		return append(out, '\n'), nil
	case FileFormatTOML:
		out, err := toml.FromJSON(jsonBytes)
		if err != nil {
			return nil, fmt.Errorf("convert to toml: %w", err)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("%w: %s", ErrUnsupportedFileFormat, fileFormat)
	}
}

func renderTemplate[T any](name string, text string, data T) ([]byte, error) {
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

type templateSpecData struct {
	Version string
	Package string
}

func renderSpecFile(version string, pkg string, fileFormat FileFormat) ([]byte, error) {
	bytes, err := renderTemplate(
		"spec",
		templateSpec,
		templateSpecData{
			Version: version,
			Package: pkg,
		},
	)

	if err != nil {
		return nil, err
	}

	return convert(bytes, fileFormat)
}

type templateConfData struct {
	Version string
	Package string
}

func renderConfFile(version string, pkg string, fileFormat FileFormat) ([]byte, error) {
	bytes, err := renderTemplate(
		"conf",
		templateConf,
		templateConfData{
			Version: version,
			Package: pkg,
		},
	)

	if err != nil {
		return nil, err
	}

	return convert(bytes, fileFormat)
}

type templateMainData struct {
	Package      string
	PackageAlias string
}

func renderMainFile(pkg string, pkgAlias string) ([]byte, error) {
	bytes, err := renderTemplate(
		"main",
		templateMain,
		templateMainData{
			Package:      pkg,
			PackageAlias: pkgAlias,
		},
	)

	if err != nil {
		return nil, err
	}

	return bytes, nil
}

type templateHandlerStubData struct {
	Package      string
	HandlersType string
}

func renderHandlerStubFile(pkg string, handlersType string) ([]byte, error) {
	bytes, err := renderTemplate(
		"handler_stub",
		templateHandlerStub,
		templateHandlerStubData{
			Package:      pkg,
			HandlersType: handlersType,
		},
	)

	if err != nil {
		return nil, err
	}

	return bytes, nil
}

type templateHandlerRootData struct {
	Package         string
	HandlersType    string
	RootCommandName string
	HelpVar         string
}

func renderHandlerRootFile(pkg string, handlersType string, rootCommandName string, helpVar string) ([]byte, error) {
	bytes, err := renderTemplate(
		"handler_root",
		templateHandlerRoot,
		templateHandlerRootData{
			Package:         pkg,
			HandlersType:    handlersType,
			RootCommandName: rootCommandName,
			HelpVar:         helpVar,
		},
	)

	if err != nil {
		return nil, err
	}

	return bytes, nil
}

type templateHandlerVersionData struct {
	Package         string
	HandlersType    string
	RootCommandName string
	HelpVar         string
}

func renderHandlerVersionFile(pkg string, handlersType string, rootCommandName string, helpVar string) ([]byte, error) {
	bytes, err := renderTemplate(
		"handler_version",
		templateHandlerVersion,
		templateHandlerVersionData{
			Package:         pkg,
			HandlersType:    handlersType,
			RootCommandName: rootCommandName,
			HelpVar:         helpVar,
		},
	)

	if err != nil {
		return nil, err
	}

	return bytes, nil
}

type templateHandlerHelpData struct {
	Package         string
	HandlersType    string
	RootCommandName string
	HelpVar         string
}

func renderHandlerHelpFile(pkg string, handlersType string, rootCommandName string, helpVar string) ([]byte, error) {
	bytes, err := renderTemplate(
		"handler_help",
		templateHandlerHelp,
		templateHandlerHelpData{
			Package:         pkg,
			HandlersType:    handlersType,
			RootCommandName: rootCommandName,
			HelpVar:         helpVar,
		},
	)

	if err != nil {
		return nil, err
	}

	return bytes, nil
}

// templateHandlersImport is one child cli package import in the handlers
// rollup; composed commands delegate to the child's Handlers().
type templateHandlersImport struct {
	Alias string
	Path  string
}

// templateHandlersMethod is one ProgramHandlers method in the handlers rollup:
// own commands return a local handler type, composed commands delegate to the
// child's cli package.
type templateHandlersMethod struct {
	Method         string
	Composed       bool
	HandlerType    string // own commands: the local handler struct name
	DelegateAlias  string // composed commands: the child import alias
	DelegateMethod string // composed commands: the child's ProgramHandlers method
}

type templateHandlersData struct {
	Package         string
	RotiniPkg       string
	FrameworkImport string // "" when cli and cligen share a package
	FrameworkQual   string // e.g. "cligen."; "" when same package
	ChildImports    []templateHandlersImport
	Methods         []templateHandlersMethod
}

func renderHandlersFile(data templateHandlersData) ([]byte, error) {
	return renderTemplate("handlers", templateHandlers, data)
}

// templateInputField is one generated input struct field (flag, argument, env,
// config, or a <Prefix>Inputs field).
type templateInputField struct {
	Field      string
	GoType     string
	Tag        string // rotini struct-tag body
	Recon      string // recon struct-tag body for env/config fields; "" otherwise
	EnvVar     string // explicit env var name for an env field; "" = snake-upper default
	Constraint string // space-separated validation struct-tags; "" when none
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
	Name  string // Go var name, e.g. "HelpRotiniGenerate"
	Embed string // //go:embed path, e.g. "help/rotini_generate.txt"
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
	Package      string
	RotiniImport string
	RotiniPkg    string
	Imports      []string // pre-rendered import lines (aliased form "alias \"path\"")
	Methods      []string // ProgramHandlers method names, e.g. "RotiniGenerate"
	Definition   string   // pre-rendered definition var declaration
	Blocks       []templateInputBlock
	OutputTypes  string // pre-rendered output type declarations; "" when none
	BindMeta     string // pre-rendered bind metadata; "" when none
	Features     []templateFeature
}

func renderRotiniFile(data templateRotiniData) ([]byte, error) {
	return renderTemplate("rotini", templateRotini, data)
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
// templates render the same data (templateManData is an alias); man
// additionally renders ExitStatus and SeeAlso.
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

type templateManData = templateHelpData

func renderHelpFile(data templateHelpData) ([]byte, error) {
	return renderDocFile("help", templateHelp, data)
}

func renderManFile(data templateManData) ([]byte, error) {
	return renderDocFile("man", templateMan, data)
}

// renderDocFile renders one doc page (help/man): sanitize the row text, execute
// the template, align tab-separated columns, and tidy the result. It is a pure
// function of (text, data) so repeated passes produce byte-identical output.
func renderDocFile(name string, text string, data templateHelpData) ([]byte, error) {
	rendered, err := renderTemplate(name, text, sanitizeDocData(data))
	if err != nil {
		return nil, err
	}

	aligned, err := tabAlign(string(rendered))
	if err != nil {
		return nil, err
	}

	return []byte(tidy(aligned)), nil
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
func tabAlign(s string) (string, error) {
	var buffer bytes.Buffer
	tw := tabwriter.NewWriter(&buffer, 0, 0, 4, ' ', 0)
	if _, err := tw.Write([]byte(s)); err != nil {
		return "", fmt.Errorf("tabwriter write: %w", err)
	}
	if err := tw.Flush(); err != nil {
		return "", fmt.Errorf("tabwriter flush: %w", err)
	}
	return buffer.String(), nil
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
