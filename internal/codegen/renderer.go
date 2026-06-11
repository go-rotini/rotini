package codegen

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"sort"
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

	ErrUnsupportedFileFormat = errors.New("unsupported file format")
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

func renderTemplate(name string, text string, data any) ([]byte, error) {
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
func renderGoFile(name string, text string, data any) ([]byte, error) {
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

// groupImports rewrites a Go source file's single gofmt'd import block into the two
// conventional groups — standard library first, then third-party — separated by a
// blank line, and re-formats. gofmt sorts imports but never splits std from
// third-party (that is goimports' job); the templates emit one merged block, so this
// restores the idiom without taking on the golang.org/x/tools dependency. A file
// with fewer than two imports, or whose imports already fall in a single group, is
// returned gofmt'd but otherwise unchanged.
func groupImports(src []byte) ([]byte, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "", src, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("group imports: parse: %w", err)
	}
	var decl *ast.GenDecl
	for _, d := range f.Decls {
		if gd, ok := d.(*ast.GenDecl); ok && gd.Tok == token.IMPORT && gd.Lparen.IsValid() {
			decl = gd
			break
		}
	}

	// Rearrange only when there is a parenthesized block holding both a standard-
	// library and a third-party group; otherwise the block is already conventional
	// and gofmt-formatted as-is below.
	out := src
	if decl != nil && len(decl.Specs) >= 2 {
		var std, third []string
		for _, s := range decl.Specs {
			is := s.(*ast.ImportSpec)
			spec := is.Path.Value
			if is.Name != nil {
				spec = is.Name.Name + " " + spec
			}
			if isThirdPartyImport(is.Path.Value) {
				third = append(third, spec)
			} else {
				std = append(std, spec)
			}
		}
		if len(std) > 0 && len(third) > 0 {
			sort.Strings(std)
			sort.Strings(third)

			var block strings.Builder
			block.WriteString("import (\n")
			for _, s := range std {
				block.WriteString("\t" + s + "\n")
			}
			block.WriteString("\n")
			for _, s := range third {
				block.WriteString("\t" + s + "\n")
			}
			block.WriteString(")")

			start := fset.Position(decl.Pos()).Offset
			end := fset.Position(decl.End()).Offset
			var buf bytes.Buffer
			buf.Write(src[:start])
			buf.WriteString(block.String())
			buf.Write(src[end:])
			out = buf.Bytes()
		}
	}

	formatted, err := format.Source(out)
	if err != nil {
		return nil, fmt.Errorf("group imports: gofmt: %w", err)
	}
	return formatted, nil
}

// isThirdPartyImport reports whether a quoted import path is outside the
// standard library: its first path element contains a dot (a domain), the same
// heuristic goimports uses.
func isThirdPartyImport(quotedPath string) bool {
	p := strings.Trim(quotedPath, `"`)
	if i := strings.IndexByte(p, '/'); i >= 0 {
		p = p[:i]
	}
	return strings.Contains(p, ".")
}

// templateSeedData is the context for the spec and conf seed templates.
type templateSeedData struct {
	Version string
	Package string
}

// renderSeedFile renders one YAML seed template and transcodes it to the
// requested file format.
func renderSeedFile(name string, text string, version string, pkg string, fileFormat FileFormat) ([]byte, error) {
	rendered, err := renderTemplate(name, text, templateSeedData{
		Version: version,
		Package: pkg,
	})

	if err != nil {
		return nil, err
	}

	return convert(rendered, fileFormat)
}

func renderSpecFile(version string, pkg string, fileFormat FileFormat) ([]byte, error) {
	return renderSeedFile("spec", templateSpec, version, pkg, fileFormat)
}

func renderConfFile(version string, pkg string, fileFormat FileFormat) ([]byte, error) {
	return renderSeedFile("conf", templateConf, version, pkg, fileFormat)
}

type templateMainData struct {
	Package      string
	PackageAlias string
}

func renderMainFile(pkg string, pkgAlias string) ([]byte, error) {
	return renderGoFile("main", templateMain, templateMainData{
		Package:      pkg,
		PackageAlias: pkgAlias,
	})
}

// templateHandlerData is the per-command handler seed context, shared by the
// stub/root/version/help handler templates (the stub reads only Package and
// HandlersType).
type templateHandlerData struct {
	Package         string
	HandlersType    string
	RootCommandName string
	HelpVar         string
}

func renderHandlerStubFile(pkg string, handlersType string) ([]byte, error) {
	return renderGoFile("handler_stub", templateHandlerStub, templateHandlerData{
		Package:      pkg,
		HandlersType: handlersType,
	})
}

func renderHandlerRootFile(pkg string, handlersType string, rootCommandName string, helpVar string) ([]byte, error) {
	return renderGoFile("handler_root", templateHandlerRoot, templateHandlerData{
		Package:         pkg,
		HandlersType:    handlersType,
		RootCommandName: rootCommandName,
		HelpVar:         helpVar,
	})
}

func renderHandlerVersionFile(pkg string, handlersType string, rootCommandName string, helpVar string) ([]byte, error) {
	return renderGoFile("handler_version", templateHandlerVersion, templateHandlerData{
		Package:         pkg,
		HandlersType:    handlersType,
		RootCommandName: rootCommandName,
		HelpVar:         helpVar,
	})
}

func renderHandlerHelpFile(pkg string, handlersType string, rootCommandName string, helpVar string) ([]byte, error) {
	return renderGoFile("handler_help", templateHandlerHelp, templateHandlerData{
		Package:         pkg,
		HandlersType:    handlersType,
		RootCommandName: rootCommandName,
		HelpVar:         helpVar,
	})
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
	FrameworkImport string // "" when cli and cligen share a package
	FrameworkQual   string // e.g. "cligen."; "" when same package
	ChildImports    []templateHandlersImport
	Methods         []templateHandlersMethod
}

func renderHandlersFile(data templateHandlersData) ([]byte, error) {
	return renderGoFile("handlers", templateHandlers, data)
}

// templateInputField is one generated input struct field (flag, argument, env,
// config, or a <Prefix>Inputs field). Tag is the complete struct-tag literal,
// backticks included ("" when the field carries no tag) — see inputFieldTag.
type templateInputField struct {
	Field  string
	GoType string
	Tag    string
}

// inputFieldTag assembles a complete struct-tag literal for a generated input
// field: the rotini tag plus the optional recon / env / constraint tags.
// constraint is pre-rendered space-separated tags (e.g. `min:"1" max:"65535"`);
// every part but rotiniTag may be empty.
func inputFieldTag(rotiniTag string, recon string, envVar string, constraint string) string {
	tag := fmt.Sprintf("rotini:%q", rotiniTag)
	if recon != "" {
		tag += fmt.Sprintf(" recon:%q", recon)
	}
	if envVar != "" {
		tag += fmt.Sprintf(" env:%q", envVar)
	}
	if constraint != "" {
		tag += " " + constraint
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
	Package     string
	Imports     []string // pre-rendered import lines (aliased form "alias \"path\"")
	Methods     []string // ProgramHandlers method names, e.g. "RotiniGenerate"
	Definition  string   // pre-rendered definition var declaration
	Blocks      []templateInputBlock
	OutputTypes string // pre-rendered output type declarations; "" when none
	BindMeta    string // pre-rendered bind metadata; "" when none
	Features    []templateFeature
}

func renderRotiniFile(data templateRotiniData) ([]byte, error) {
	return renderGoFile("rotini", templateRotini, data)
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
