package codegen

// Template parsing and rendering for every generated artifact: seed spec and conf files, the
// entrypoint, handler stubs, the generated cmd and models files, and the doc pages.

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"go/format"
	"reflect"
	"strconv"
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

// convert transcodes a rendered YAML seed to the target format: YAML is returned verbatim,
// json and jsonc become indented JSON, and toml becomes TOML. Keys keep the template's order
// (via orderedValue), and comments carry into jsonc and toml.
func convert(yamlBytes []byte, target fileFormat) ([]byte, error) {
	if target == formatYAML {
		return yamlBytes, nil
	}

	var doc any
	if err := yaml.UnmarshalWithOptions(yamlBytes, &doc, yaml.WithOrderedMap()); err != nil {
		return nil, fmt.Errorf("convert seed: %w", err)
	}
	comments, err := seedComments(yamlBytes)
	if err != nil {
		return nil, fmt.Errorf("convert seed: %w", err)
	}
	value := orderedValue(doc)

	switch target {
	case formatJSON:
		out, err := json.MarshalIndent(value, "", "  ")
		if err != nil {
			return nil, fmt.Errorf("encode json: %w", err)
		}
		return append(out, '\n'), nil
	case formatJSONC:
		byPath := map[string][]jsonc.Comment{}
		for path, lines := range comments {
			for _, line := range lines {
				byPath[path] = append(byPath[path], jsonc.Comment{Position: jsonc.HeadCommentPos, Text: line})
			}
		}
		out, err := jsonc.MarshalWithOptions(value, jsonc.WithIndent("  "), jsonc.WithEscapeHTML(false), jsonc.WithComment(byPath))
		if err != nil {
			return nil, fmt.Errorf("encode jsonc: %w", err)
		}
		return append(out, '\n'), nil
	case formatTOML:
		out, err := toml.Marshal(value)
		if err != nil {
			return nil, fmt.Errorf("convert to toml: %w", err)
		}
		return tomlWithComments(out, comments), nil
	default:
		return nil, fmt.Errorf("%w: %s", errUnsupportedFormat, target)
	}
}

// tomlWithComments writes each comment above the table header its path names (`[a.b]`, or
// `[[a.b]]` for an array of tables), since the toml encoder only comments key/value lines. A
// comment whose table is absent from the output is dropped.
func tomlWithComments(out []byte, comments map[string][]string) []byte {
	text := string(out)
	for path, lines := range comments {
		for _, header := range []string{"[[" + path + "]]\n", "[" + path + "]\n"} {
			i := strings.Index(text, header)
			if i < 0 || (i > 0 && text[i-1] != '\n') {
				continue
			}
			var block strings.Builder
			for _, line := range lines {
				block.WriteString("# " + line + "\n")
			}
			text = text[:i] + block.String() + text[i:]
			break
		}
	}
	return []byte(text)
}

// seedComments collects a YAML seed's head comments by the dotted path of the key they sit
// above ("generate.features"). A comment above a list item is attributed to the key holding
// the list, where JSONC and TOML can carry it.
func seedComments(yamlBytes []byte) (map[string][]string, error) {
	file, err := yaml.Parse(yamlBytes)
	if err != nil {
		return nil, fmt.Errorf("read seed comments: %w", err)
	}
	out := map[string][]string{}
	var walk func(n *yaml.Node, path string, listItem bool)
	walk = func(n *yaml.Node, path string, listItem bool) {
		switch n.Kind {
		case yaml.DocumentNode:
			for _, c := range n.Children {
				walk(c, path, false)
			}
		case yaml.SequenceNode:
			for _, c := range n.Children {
				walk(c, path, true)
			}
		case yaml.MappingNode:
			for i := 0; i+1 < len(n.Children); i += 2 {
				key, val := n.Children[i], n.Children[i+1]
				child := key.Value
				if path != "" {
					child = path + "." + key.Value
				}
				if key.HeadComment != "" {
					// A comment above a list item parses onto the item's first key.
					at := child
					if i == 0 && listItem {
						at = path
					}
					out[at] = append(out[at], commentLines(key.HeadComment)...)
				}
				walk(val, child, false)
			}
		}
	}
	for _, d := range file.Docs {
		walk(d, "", false)
	}
	return out, nil
}

// commentLines splits a parsed comment block into its lines, without the '#' markers.
func commentLines(text string) []string {
	var lines []string
	for line := range strings.SplitSeq(text, "\n") {
		lines = append(lines, strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "#")))
	}
	return lines
}

// orderedValue rebuilds a decoded ordered YAML value with every mapping as a struct whose
// fields are the mapping's keys in order (tagged for the json, jsonc and toml encoders), so
// encoding keeps the order a map would lose.
func orderedValue(v any) any {
	switch t := v.(type) {
	case yaml.MapSlice:
		fields := make([]reflect.StructField, len(t))
		values := make([]reflect.Value, len(t))
		for i, item := range t {
			val := reflect.ValueOf(orderedValue(item.Value))
			typ := reflect.TypeFor[any]()
			if val.IsValid() {
				typ = val.Type()
			}
			fields[i] = reflect.StructField{
				Name: "F" + strconv.Itoa(i),
				Type: typ,
				Tag:  reflect.StructTag(fmt.Sprintf(`json:%q`, fmt.Sprint(item.Key))),
			}
			values[i] = val
		}
		out := reflect.New(reflect.StructOf(fields)).Elem()
		for i, val := range values {
			if val.IsValid() {
				out.Field(i).Set(val)
			}
		}
		return out.Interface()
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = orderedValue(e)
		}
		return out
	default:
		return v
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

// renderGoFileWithHeader renders a Go source template, gofmt-formats it and groups its
// imports. A formatting failure includes the unformatted source. header is the target's conf
// `header:` (a license block or //go:build constraint, or ""); it is written verbatim above
// the "Code generated" line and formatted with the rest, so a malformed header fails here.
func renderGoFileWithHeader(header, name, text string, data any) ([]byte, error) {
	rendered, err := renderTemplate(name, text, data)
	if err != nil {
		return nil, err
	}
	if h := strings.TrimRight(header, "\n"); h != "" {
		rendered = append([]byte(h+"\n\n"), rendered...)
	}

	formatted, err := format.Source(rendered)
	if err != nil {
		return nil, fmt.Errorf("gofmt %s: %w\n--- generated source ---\n%s", name, err, rendered)
	}

	return groupImports(formatted)
}

// templateSeedData is the data for the spec and conf seed templates that `rotini init` writes.
type templateSeedData struct {
	Version string
	Package string
}

// renderSeedFile renders one YAML seed template and transcodes it to target.
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
	Header       string // the target's conf `header:`; "" for none
}

func renderMainFile(header, pkg, pkgAlias, extension string) ([]byte, error) {
	return renderGoFileWithHeader(header, "main", templateMain, templateMainData{
		Header:       header,
		Package:      pkg,
		PackageAlias: pkgAlias,
		Extension:    extension,
	})
}

// templateHandlerData is the data for a create-once handler stub: a type embedding the No*
// hooks, with a Run seeded from what the spec and conf declare.
type templateHandlerData struct {
	Package       string
	HandlerType   string
	InputsType    string // the command's generated inputs type, e.g. RotiniGenerateInputs
	Invocation    string // how a user types the command, e.g. "rotini generate"
	Prefix        string // this command's frame inside the inputs type, e.g. inputs.RotiniGenerate
	RuntimeImport string

	// The seeded Run body, derived from the spec and conf (a --help flag with the help
	// feature on, a --version flag, a `help` command with a variadic path argument).
	HelpFlag           string // Go field of this command's bool `help` flag; "" when none or the help feature is off
	HelpFrame          string // inputs frame holding HelpFlag: this command's prefix, or an ancestor's when inherited
	HelpFlagName       string // logical name of the flag answered first ("help", else "version"); not used by the stub template
	AnswerBeforeInputs bool   // help/version are answered from argv before Inputs validates; not used by the stub template
	UsesInputs         bool   // the body reads `inputs`; when false Inputs still runs, for its validation
	VersionFlag        string // Go field of this command's bool `version` flag; "" when there is none
	Header             string // the target's conf `header:`; "" for none
	HelpPathArg        string // Go field of the variadic path argument on a command named `help`; "" otherwise
	VersionOnly        bool   // a command named `version` whose whole job is to print it
	PrintHelpWhenBare  bool   // a dispatcher root: sub-commands, no own arguments, help feature on
	NeedsInputs        bool   // the body calls Inputs (for its result or its validation)
}

func renderHandlerStubFile(data templateHandlerData) ([]byte, error) {
	return renderGoFileWithHeader(data.Header, "handler", templateHandlerStub, data)
}

// templateHandlersImport is one composed child's cmd package import in the generated file.
type templateHandlersImport struct {
	Alias string
	Path  string
}

// templateHandlersMethod is one method of the generated handlers struct: an own command returns
// its local handler type, and a composed command delegates to the child's cmd package.
type templateHandlersMethod struct {
	Method         string
	Composed       bool
	Passthrough    bool   // delegate via alias.method() instead of alias.Handlers().method()
	HandlerType    string // own commands: the local handler struct name
	DelegateAlias  string // composed commands: the child import alias
	DelegateMethod string // composed commands: the child's ProgramHandlers method, or the convention
}

// templateInputField is one generated input struct field (flag, argument, env, config, or a
// <Prefix>Inputs field). Tag is the complete struct-tag literal from inputFieldTag, or "".
type templateInputField struct {
	Field   string
	GoType  string
	Tag     string
	Comment string // optional trailing line-comment ("" for none)
}

// inputFieldTag assembles a generated input field's complete struct-tag literal: the rotini
// tag plus the optional recon, env, envnest, cfgfile and constraint tags. Every part but Tag
// may be empty.
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
	// Prefer a raw string literal, unless a value (a pattern, an enum member) holds a backquote.
	if strings.Contains(tag, "`") {
		return strconv.Quote(tag)
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

// templateFeatureVar is one feature page var in the generated file.
type templateFeatureVar struct {
	Name    string // Go var name, e.g. "HelpRotiniGenerate"
	Embed   string // //go:embed path (embed mode), e.g. "renders/help_rotini_generate.txt"; "" in inline mode
	Literal string // Go string literal of the content (inline mode), e.g. `"Usage:\n…"`; "" in embed mode
}

// templateFeaturePage is one entry of a generated page list: a rotini.Page literal's fields.
type templateFeaturePage struct {
	Name        string // the page name, e.g. "taskr-add"
	PathLiteral string // the command path as a Go literal: `[]string{"add"}`, or `nil` for the root
	Var         string // the var holding the page
}

// templateFeatureCase is one resolver case.
type templateFeatureCase struct {
	PathsLiteral string // case values, e.g. `"generate", "gen"` (root: `""`)
	Var          string // the var returned for these paths
}

// templateFeature is one feature's vars, resolver and optional page list in the generated file.
type templateFeature struct {
	Resolver string // resolver func name, e.g. "Help", "Man", "Completion"
	Noun     string // word used in the doc comment and error, e.g. "help"
	PerShell bool   // completion: resolver takes a shell string, not a command path
	Section  string // man: the section the pages were generated for, emitted as ManSection; "" otherwise
	// PagesFunc is the generated page-list function (ManPages, MarkdownPages) and Pages its
	// entries, the visible commands in tree order; "" and nil for features without one.
	PagesFunc string
	Pages     []templateFeaturePage
	Vars      []templateFeatureVar
	Cases     []templateFeatureCase
}

// templateRotiniData is the data for the generated cmd file.
type templateRotiniData struct {
	Package       string
	RuntimeImport string                   // the rotini runtime's import line (identifier `rotini`)
	Imports       []string                 // pre-rendered import lines (aliased form "alias \"path\"")
	ChildImports  []templateHandlersImport // composed children's cmd packages
	Methods       []string                 // ProgramHandlers method names, e.g. "RotiniGenerate"
	RollupMethods []templateHandlersMethod // the generated handlers struct's command→handler methods
	Definition    string                   // pre-rendered definition var declaration
	ModelsImport  string                   // models package import line; "" unless the types were split out
	ModelAliases  []string                 // model type names re-exported here as aliases; empty unless split
	Blocks        []templateInputBlock
	OutputTypes   string // pre-rendered output type declarations; "" when none
	InputSettings string // pre-rendered InputSettings declaration; "" when none
	Features      []templateFeature
	EmbedImport   bool // emit `import _ "embed"`: some feature uses //go:embed
	// PathResolvers is true when some feature emits a path-keyed resolver (help, man,
	// markdown), which imports "strings". The shell-keyed completion resolver does not.
	PathResolvers bool
	// HelpResolver is "Help" when the help feature is on, else "". NewProgram passes it to
	// WithHelp so [rotini.Context.Help] can find the running command's page.
	HelpResolver string
	Header       string // the target's conf `header:`; "" for none
}

func renderRotiniFile(data templateRotiniData) ([]byte, error) {
	return renderGoFileWithHeader(data.Header, "rotini", templateRotini, data)
}

// templateModelsData is the data for the models file: only the typed input and output structs,
// so its package can be imported anywhere, including by a handler package the cmd package
// imports.
type templateModelsData struct {
	Package     string
	Imports     []string // pre-rendered import lines for the field types
	Blocks      []templateInputBlock
	OutputTypes string // pre-rendered output type declarations; "" when none
	Header      string // the target's conf `header:`; "" for none
}

func renderModelsFile(data templateModelsData) ([]byte, error) {
	return renderGoFileWithHeader(data.Header, "models", templateModels, data)
}

// templateDocHeadings holds the resolved section headings, each rendered verbatim (the trailing
// ":" is part of the value).
type templateDocHeadings struct {
	Usage, Commands, Arguments, Flags, Environment, Configuration, Cascading, Examples, Output string
}

// templateDocCommandGroup is one bucket of sub-commands in the Commands section. Title is the
// `group` value; "" is the ungrouped bucket, headed with the template's default.
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
	Type        string // "" for bool and count flags, and when the token moved into an identifier
	Required    bool
	Default     string
	Implicit    string // the value a bare flag takes (implicit_value); its identifier reads --x[=<type>]
	Enum        []string
	Deprecated  string
	Group       string // the flag's `group` (buckets it in the Flags section)
}

// templateDocFlagGroup is one bucket of flags in the Flags section. Title is the `group` value;
// "" is the ungrouped bucket, headed with the template's default.
type templateDocFlagGroup struct {
	Title string
	Flags []templateDocFlagRow
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
	Location   string // where the value is read from: "<file>.<key>", "<key>" or ""
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
	Output  string // the shape stdout carries with this code, as a type name; "" for none
}

// templateHelpData is the per-command data shared by the help, man and markdown templates.
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
	FlagGroups    []templateDocFlagGroup
	Arguments     []templateDocArgumentRow
	Flags         []templateDocFlagRow
	Environment   []templateDocEnvRow
	Configuration []templateDocConfigRow
	Cascading     []templateDocFlagRow
	Examples      []string
	ExitStatus    []templateDocExitRow
	SeeAlso       []string

	// Man page fields, set for every feature.
	PageName     string   // the page's name: the command path joined with "-", lowercased (taskr-add)
	Section      string   // the man section, "1" unless the conf sets another
	Source       string   // the program, for the page header: the root's display_name, else its name
	Date         string   // the header date: SOURCE_DATE_EPOCH's day (YYYY-MM-DD) when set, else ""
	RelatedPages []string // page names to cross-reference: the parent's, then each visible child's

	// Output documents the command's `output:` schema; nil when it declares none.
	Output *templateDocOutput
}

// parseDocTemplate parses doc-template text with the shared FuncMap.
func parseDocTemplate(name, text string) (*template.Template, error) {
	tmpl, err := template.New(name).Funcs(templateFuncMap()).Parse(text)
	if err != nil {
		return nil, fmt.Errorf("parse %s template: %w", name, err)
	}
	return tmpl, nil
}

// renderDocText renders one help or markdown page: it sanitizes row text, executes the
// template, aligns tab-separated columns and tidies the result. Output is a pure function of
// (tmpl, data).
func renderDocText(tmpl *template.Template, data templateHelpData) (string, error) {
	var buffer bytes.Buffer
	if err := tmpl.Execute(&buffer, sanitizeDocData(data)); err != nil {
		return "", errors.New(templateFailure(tmpl.Name(), err))
	}

	return tidy(tabAlign(buffer.String())), nil
}

// renderManText renders one roff man page. Unlike renderDocText it does not align columns
// (roff does its own layout) and drops blank lines (stray vertical space in roff; the template
// writes .PP for breaks). Trailing whitespace is trimmed, filled text lines are wrapped at
// manLineWidth, and the page ends with a newline.
func renderManText(tmpl *template.Template, data templateHelpData) (string, error) {
	var buffer bytes.Buffer
	if err := tmpl.Execute(&buffer, sanitizeDocData(data)); err != nil {
		return "", errors.New(templateFailure(tmpl.Name(), err))
	}
	var lines []string
	fill := true // outside a .nf/.fi or .EX/.EE block, where roff joins text lines anyway
	prev := ""   // the line after .TP is its tag and must stay one line
	for l := range strings.SplitSeq(buffer.String(), "\n") {
		l = strings.TrimRight(l, " \t")
		switch {
		case l == "":
			continue
		case l == ".nf" || strings.HasPrefix(l, ".nf ") || l == ".EX":
			fill = false
		case l == ".fi" || strings.HasPrefix(l, ".fi ") || l == ".EE":
			fill = true
		}
		if fill && !strings.HasPrefix(l, ".") && prev != ".TP" {
			lines = append(lines, wrapRoffText(l, manLineWidth)...)
		} else {
			lines = append(lines, l)
		}
		prev = l
	}
	return strings.Join(lines, "\n") + "\n", nil
}

// manLineWidth is the longest filled text line a rendered man page keeps; mandoc's style check
// flags input lines longer than 80 bytes.
const manLineWidth = 80

// wrapRoffText breaks one filled roff text line at spaces into pieces of at most width bytes
// (a longer single word stays whole). Fill mode rejoins them with spaces, so the page is
// unchanged. A piece that would begin with "." gets a "\&" so it stays text.
func wrapRoffText(line string, width int) []string {
	if len(line) <= width {
		return []string{line}
	}
	var out []string
	var cur strings.Builder
	for word := range strings.SplitSeq(line, " ") {
		if cur.Len() > 0 && cur.Len()+1+len(word) > width {
			out = append(out, cur.String())
			cur.Reset()
		}
		if cur.Len() > 0 {
			cur.WriteByte(' ')
		} else if strings.HasPrefix(word, ".") {
			cur.WriteString(`\&`)
		}
		cur.WriteString(word)
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

// sanitizeDocData replaces tabs and newlines in row text with spaces, since they would corrupt
// tabwriter columns. Block fields (Header, Description, Footer, Usage) are left intact. Row
// slices are copied so the input is not mutated.
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
	// Templates render flags from FlagGroups, which hold their own row copies.
	d.FlagGroups = append([]templateDocFlagGroup(nil), d.FlagGroups...)
	for i := range d.FlagGroups {
		d.FlagGroups[i].Flags = cleanFlags(d.FlagGroups[i].Flags)
	}
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
	if d.Output != nil {
		out := *d.Output
		out.Description = clean(out.Description)
		out.Fields = append([]templateDocOutputField(nil), out.Fields...)
		for i := range out.Fields {
			out.Fields[i].Description = clean(out.Fields[i].Description)
		}
		d.Output = &out
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

// tidy trims trailing whitespace per line, collapses runs of blank lines to one, and strips
// leading and trailing blank lines, so the page ends at its last line with no trailing newline.
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

// templateFuncMap is the helper set available to every template. It holds no clock or entropy
// functions, so rendering stays byte-stable.
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
		// roff escaping, for man page templates (see generate_roff.go).
		"roff":      roffInline,
		"roffLines": roffLines,
		"roffBlock": roffBlock,
		"roffArg":   roffArg,
	}
}

// titleASCII upper-cases the first ASCII letter of each word; words are split on space, tab,
// '-' and '_'.
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

// templateFailure rewrites a doc template's execution error for the author of an editable
// template: it drops the redundant `executing "<file>" at <.X>` clause and the internal
// `in type codegen.…` suffix, and for an unknown field points at the field list in the
// template's header comment:
//
//	help.txt.tmpl:78:2: can't evaluate field NoSuchField; the fields available to this
//	template are listed in the comment at the top of help.txt.tmpl
func templateFailure(name string, err error) string {
	msg := strings.TrimPrefix(err.Error(), "template: ")
	// "help.txt.tmpl:78:2: executing "help.txt.tmpl" at <.X>: can't evaluate field X" keeps
	// the position and the detail.
	if head, after, ok := strings.Cut(msg, `executing "`); ok {
		if _, detail, found := strings.Cut(after, ": "); found {
			msg = head + detail
		}
	}
	if before, _, ok := strings.Cut(msg, " in type codegen."); ok {
		msg = before
	}
	if strings.Contains(msg, "can't evaluate field") {
		return fmt.Sprintf("%s; the fields available to this template are listed in the comment at the top of %s", msg, name)
	}
	return msg
}
