package codegen

import (
	"errors"

	"strings"
	"testing"
)

func TestSmokeRenderSeedFiles(t *testing.T) {
	for _, format := range []fileFormat{formatYAML, formatJSON, formatJSONC, formatTOML} {
		if _, err := renderSpecFile("v1.0.0", "app", format); err != nil {
			t.Errorf("spec %s: %v", format, err)
		}
		if _, err := renderConfFile("v1.0.0", "app", format); err != nil {
			t.Errorf("conf %s: %v", format, err)
		}
	}
}

func TestSmokeRenderMainAndHandlerFiles(t *testing.T) {
	if _, err := renderMainFile("", "example.com/app/internal/cmd/app", "cli", "yaml"); err != nil {
		t.Errorf("main: %v", err)
	}
	// Every seeded body the template can emit has to render AND compile-format. The
	// plain stub is the default; the rest are what stubBody selects when the spec and
	// conf already declare what the body needs.
	base := templateHandlerData{
		Package:       "cli",
		HandlersType:  "appSubHandlers",
		InputsType:    "AppSubInputs",
		Invocation:    "app sub",
		Prefix:        "AppSub",
		RuntimeImport: `"github.com/go-rotini/rotini"`,
	}
	bodies := map[string]func(d templateHandlerData) templateHandlerData{
		"plain": func(d templateHandlerData) templateHandlerData { return d },
		"help flag": func(d templateHandlerData) templateHandlerData {
			d.HelpFlag, d.HelpFrame = "Help", d.Prefix
			return d
		},
		"version flag": func(d templateHandlerData) templateHandlerData { d.VersionFlag = "Version"; return d },
		"help command": func(d templateHandlerData) templateHandlerData {
			d.HelpPathArg = "Command"
			return d
		},
		"version cmd": func(d templateHandlerData) templateHandlerData { d.VersionOnly = true; return d },
		"bare root help": func(d templateHandlerData) templateHandlerData {
			d.PrintHelpWhenBare = true
			return d
		},
		"all flags": func(d templateHandlerData) templateHandlerData {
			d.HelpFlag, d.HelpFrame, d.VersionFlag = "Help", d.Prefix, "Version"
			return d
		},
	}
	for name, mutate := range bodies {
		t.Run(name, func(t *testing.T) {
			// renderGoFile gofmts, so a body that does not parse fails here.
			if _, err := renderHandlerStubFile(mutate(base)); err != nil {
				t.Errorf("handler stub: %v", err)
			}
		})
	}
}

func TestSmokeRenderRotiniFile(t *testing.T) {
	out, err := renderRotiniFile(templateRotiniData{
		Package:       "cligen",
		RuntimeImport: `"github.com/go-rotini/rotini"`,
		Imports:       []string{`"time"`},
		ChildImports:  []templateHandlersImport{{Alias: "childcli", Path: "example.com/child/cli"}},
		Methods:       []string{"App", "AppGenerate"},
		RollupMethods: []templateHandlersMethod{
			{Method: "App", HandlerType: "appHandlers"},
			{Method: "AppChild", Composed: true, DelegateAlias: "childcli", DelegateMethod: "Child"},
		},
		Definition: "var definition = rotini.Definition{}",
		Blocks: []templateInputBlock{
			{
				Prefix: "App",
				Flags: []templateInputField{
					{Field: "Verbose", GoType: "bool", Tag: inputFieldTag(fieldDef{Tag: "flag:verbose"})},
				},
				Arguments: []templateInputField{
					{Field: "Paths", GoType: "[]string", Tag: inputFieldTag(fieldDef{Tag: "argument:paths"})},
				},
				Env: []templateInputField{
					{Field: "Home", GoType: "string", Tag: inputFieldTag(fieldDef{Tag: "env:home", Recon: "key:home", EnvVar: "APP_HOME", Constraint: `min:"1"`})},
				},
				Config: []templateInputField{
					{Field: "Timeout", GoType: "time.Duration", Tag: inputFieldTag(fieldDef{Tag: "config:timeout", Recon: "key:timeout"})},
				},
				StdinType:    "*AppStdin",
				StdinFormat:  "yaml",
				InputsFields: []templateInputField{{Field: "App", GoType: "AppCommandInputs"}},
			},
			{Prefix: "AppGenerate"},
		},
		OutputTypes: "type AppOutput struct{}",
		BindMeta:    "var bindMeta = map[string]string{}",
		Features: []templateFeature{
			{
				Resolver: "Help", Noun: "help",
				Vars:  []templateFeatureVar{{Name: "HelpApp", Embed: "help/app.txt"}},
				Cases: []templateFeatureCase{{PathsLiteral: `""`, Var: "HelpApp"}},
			},
			{
				Resolver: "Completion", Noun: "completion", PerShell: true,
				Vars:  []templateFeatureVar{{Name: "CompletionBash", Embed: "completion/bash.txt"}},
				Cases: []templateFeatureCase{{PathsLiteral: `"bash"`, Var: "CompletionBash"}},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "\"time\"\n\n\tchildcli \"example.com/child/cli\"") {
		t.Error("imports should be grouped std then third-party")
	}
	// The rollup is folded into the cli file: the handlers struct + its per-command
	// wiring (own commands return a local stub; composed commands delegate).
	if !strings.Contains(string(out), "return &appHandlers{}") {
		t.Error("rollup wiring for an own command is missing from the generated cli file")
	}
	if !strings.Contains(string(out), "childcli.Handlers().Child()") {
		t.Error("rollup wiring for a composed command is missing from the generated cli file")
	}
	t.Logf("rotini:\n%s", out)
}

func smokeDocData() templateHelpData {
	return templateHelpData{
		Header:       "app — does things",
		Invocation:   "app generate",
		Summary:      "generate stuff",
		Description:  "Generates all the stuff.",
		UsageDerived: "app generate [flags] <path>",
		Footer:       "See https://example.com for docs.",
		Headings: templateDocHeadings{
			Usage: "Usage:", Commands: "Commands:", Arguments: "Arguments:",
			Flags: "Flags:", Environment: "Environment:", Configuration: "Configuration:",
			Cascading: "Global Flags:", Examples: "Examples:",
		},
		CommandGroups: []templateDocCommandGroup{
			{Commands: []templateDocCommandRow{{Name: "sub", Summary: "a\tsub\ncommand", Aliases: []string{"s"}}}},
			{Title: "Advanced", Commands: []templateDocCommandRow{{Name: "deep", Summary: "deep magic", Deprecated: "use sub"}}},
		},
		Arguments: []templateDocArgumentRow{
			{Name: "path", Summary: "input path", Required: true, Variadic: true},
		},
		Flags: []templateDocFlagRow{
			{Identifiers: []string{"--out", "-o"}, Summary: "output file", Type: "string", Default: "out.txt", Enum: []string{"a", "b"}},
		},
		Environment: []templateDocEnvRow{
			{Var: "APP_HOME", Summary: "home dir", Type: "string", Required: true},
		},
		Configuration: []templateDocConfigRow{
			{Name: "timeout", Location: "conf.timeout", Summary: "request timeout", Type: "duration"},
		},
		Cascading: []templateDocFlagRow{
			{Identifiers: []string{"--verbose"}, Summary: "noisy output"},
		},
		Examples:   []string{"app generate ./src"},
		ExitStatus: []templateDocExitRow{{Code: 0, Summary: "success"}, {Code: 1, Summary: "failure"}},
		SeeAlso:    []string{"app(1)", "app-sub(1)"},
	}
}

// renderDocSmoke drives the live doc-render path (parse + render) over an embedded
// template, the same path features.go uses in production.
func renderDocSmoke(t *testing.T, name, text string, data templateHelpData) string {
	t.Helper()
	tmpl, err := parseDocTemplate(name, text)
	if err != nil {
		t.Fatal(err)
	}
	out, err := renderDocText(tmpl, data)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestSmokeRenderHelpFile(t *testing.T) {
	s := renderDocSmoke(t, "help", templateHelp, smokeDocData())
	if strings.HasSuffix(s, "\n") {
		t.Error("help page should not end with a trailing newline")
	}
	if strings.Contains(s, "\n\n\n") {
		t.Error("help page should not contain blank-line runs")
	}
	if strings.Contains(s, "a\tsub\ncommand") {
		t.Error("row text should have been sanitized")
	}
	t.Logf("help:\n%s", s)
}

func TestSmokeRenderManFile(t *testing.T) {
	s := renderDocSmoke(t, "man", templateMan, smokeDocData())
	for _, want := range []string{"NAME", "SYNOPSIS", "EXIT STATUS", "SEE ALSO"} {
		if !strings.Contains(s, want) {
			t.Errorf("man page missing %q section", want)
		}
	}
	t.Logf("man:\n%s", s)
}

func TestSmokeConvertJSONC(t *testing.T) {
	out, err := convert([]byte("name: app\nversion: 1\n"), formatJSONC)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"name"`) {
		t.Errorf("jsonc output lost data: %s", out)
	}
	t.Logf("jsonc:\n%s", out)
}

// TestTemplateFuncMapHelpers executes a template exercising every helper —
// including the branchy ones (default/first/last with and without content) —
// so the documented allowlist is proven to behave, not just to exist.
func TestTemplateFuncMapHelpers(t *testing.T) {
	const text = `{{join .List ","}}
{{upper "ab"}} {{lower "AB"}} {{title "foo-bar baz"}}
{{trim "  t  "}} {{trimPrefix "p-" "p-u"}} {{trimSuffix "-s" "w-s"}}
{{replace "a" "b" "aaa"}} {{repeat 3 "-"}}
{{default "fallback" ""}} {{default "kept" "set"}}
{{contains "b" "abc"}} {{contains "z" "abc"}}
{{hasPrefix "a" "abc"}} {{hasSuffix "z" "abc"}}
{{first .List}} {{last .List}} {{first .Empty}} {{last .Empty}}
{{indent 2 .Block}}`
	data := map[string]any{
		"List":  []string{"a", "b"},
		"Empty": []string{},
		"Block": "one\n\ntwo",
	}
	out, err := renderTemplate("funcs", text, data)
	if err != nil {
		t.Fatalf("renderTemplate: %v", err)
	}
	want := "a,b\nAB ab Foo-Bar Baz\nt u w\nbbb ---\nfallback set\ntrue false\ntrue false\na b  \n  one\n\n  two"
	if string(out) != want {
		t.Errorf("helpers output mismatch:\n got %q\nwant %q", out, want)
	}
}

func TestRenderTemplate_errors(t *testing.T) {
	if _, err := renderTemplate("broken", "{{", nil); err == nil {
		t.Error("renderTemplate(unparsable) = nil, want a parse error")
	}
	// Execution failure: referencing a field the data type does not have.
	if _, err := renderTemplate("exec", "{{.Nope}}", struct{}{}); err == nil {
		t.Error("renderTemplate(bad field) = nil, want an execute error")
	}
}

func TestRenderGoFile_gofmtError(t *testing.T) {
	_, err := renderGoFileWithHeader("", "invalid", "package x\nfunc {", nil)
	if err == nil || !strings.Contains(err.Error(), "gofmt") {
		t.Errorf("renderGoFileWithHeader(invalid Go) = %v, want a gofmt error with source context", err)
	}
}

func TestConvert_errors(t *testing.T) {
	if _, err := convert([]byte("a: 1\n"), formatUnknown); err == nil {
		t.Error("convert(unknown format) = nil, want errUnsupportedFormat")
	}
	if _, err := convert([]byte("a: [unclosed"), formatJSON); err == nil {
		t.Error("convert(bad yaml) = nil, want a conversion error")
	}
}

func TestParseDocTemplate_error(t *testing.T) {
	if _, err := parseDocTemplate("broken", "{{"); err == nil {
		t.Error("parseDocTemplate(unparsable) = nil, want a parse error")
	}
}

func TestRenderDocText_execError(t *testing.T) {
	tmpl, err := parseDocTemplate("exec", `{{template "missing"}}`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := renderDocText(tmpl, templateHelpData{}); err == nil {
		t.Error("renderDocText(missing sub-template) = nil, want an execute error")
	}
}

func TestGroupImports_parseError(t *testing.T) {
	if _, err := groupImports([]byte("not go")); err == nil {
		t.Error("groupImports(invalid) = nil, want a parse error")
	}
}

func TestInputFieldTag(t *testing.T) {
	cases := []struct {
		f    fieldDef
		want string
	}{
		{fieldDef{Tag: "name"}, "`rotini:\"name\"`"},
		{fieldDef{Tag: "name", Recon: "key"}, "`rotini:\"name\" recon:\"key\"`"},
		{fieldDef{Tag: "name", Recon: "key", EnvVar: "VAR"}, "`rotini:\"name\" recon:\"key\" env:\"VAR\"`"},
		{fieldDef{Tag: "name", Recon: "key", EnvVar: "VAR", Constraint: `min:"1"`}, "`rotini:\"name\" recon:\"key\" env:\"VAR\" min:\"1\"`"},
		{fieldDef{Tag: "name", Recon: "key", EnvNest: "ACME_HTTP,__"}, "`rotini:\"name\" recon:\"key\" envnest:\"ACME_HTTP,__\"`"},
		{fieldDef{Tag: "name", Recon: "key", CfgFile: "project"}, "`rotini:\"name\" recon:\"key\" cfgfile:\"project\"`"},
	}
	for _, tc := range cases {
		if got := inputFieldTag(tc.f); got != tc.want {
			t.Errorf("inputFieldTag(%+v) = %s, want %s", tc.f, got, tc.want)
		}
	}
}

// TestSanitizeDocData_copies confirms sanitization never mutates the caller's
// slices — render passes must stay pure functions of their input.
func TestSanitizeDocData_copies(t *testing.T) {
	in := templateHelpData{
		Flags:    []templateDocFlagRow{{Summary: "a\tb"}},
		SeeAlso:  []string{"x\ty"},
		Examples: []string{"kept\tintact"}, // examples are block-ish: untouched
	}
	out := sanitizeDocData(in)
	if in.Flags[0].Summary != "a\tb" || in.SeeAlso[0] != "x\ty" {
		t.Error("sanitizeDocData mutated the input")
	}
	if out.Flags[0].Summary != "a b" || out.SeeAlso[0] != "x y" {
		t.Errorf("sanitizeDocData did not clean rows: %+v", out)
	}
	if out.Examples[0] != "kept\tintact" {
		t.Errorf("examples should be untouched, got %q", out.Examples[0])
	}
}

// TestTemplateFailure covers the error an author of an EDITABLE template reads.
//
// `template: true` seeds help.txt.tmpl / man.txt.tmpl / markdown.md.tmpl for customisation, so
// these are the only template errors a rotini USER ever sees — every other template in the
// package is rotini's own and its errors are rotini's bugs. text/template's stock wording names
// the file three times, says "template:" twice, and ends by naming `codegen.templateHelpData`,
// an internal Go type the author cannot look up. It also withholds the one useful reply: the
// available fields are listed in the template's own header comment.
func TestTemplateFailure(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "an unknown field points at the field list, not at a Go type",
			in:   `template: help.txt.tmpl:78:2: executing "help.txt.tmpl" at <.NoSuchField>: can't evaluate field NoSuchField in type codegen.templateHelpData`,
			want: `help.txt.tmpl:78:2: can't evaluate field NoSuchField; the fields available to this template are listed in the comment at the top of help.txt.tmpl`,
		},
		{
			name: "another execution failure keeps its position and loses the noise",
			in:   `template: help.txt.tmpl:12:5: executing "help.txt.tmpl" at <index .Flags 9>: error calling index: index out of range`,
			want: `help.txt.tmpl:12:5: error calling index: index out of range`,
		},
		{
			name: "a message with no restatement is passed through",
			in:   `template: help.txt.tmpl:3: unexpected EOF`,
			want: `help.txt.tmpl:3: unexpected EOF`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := templateFailure("help.txt.tmpl", errors.New(tt.in))
			if got != tt.want {
				t.Errorf("got  %q\nwant %q", got, tt.want)
			}
			// The bar from E-23: no Go internals in a message an author reads.
			for _, leak := range []string{"codegen.", "in type ", "template: template:"} {
				if strings.Contains(got, leak) {
					t.Errorf("leaks %q: %s", leak, got)
				}
			}
		})
	}
}

// The seeded spec is the first rotini document anyone reads, so it is written the way every doc
// and example writes one: inline identifier lists and inline one-key schemas.
func TestSeedSpecUsesTheDocumentedStyle(t *testing.T) {
	out, err := renderSpecFile("1.0.0", "app", formatYAML)
	if err != nil {
		t.Fatal(err)
	}
	seed := string(out)
	for _, want := range []string{"identifiers: [-h, --help]", "identifiers: [-v, --version]", "schema: { type: bool }"} {
		if !strings.Contains(seed, want) {
			t.Errorf("seed missing %q", want)
		}
	}
	for _, bad := range []string{"identifiers:\n", "schema:\n"} {
		if strings.Contains(seed, bad) {
			t.Errorf("seed writes %q expanded:\n%s", strings.TrimSpace(bad), seed)
		}
	}
}

// TestHelpPage_groupedFlagSummaryStaysOnItsRow renders a real help page: a flag summary holding a
// tab or a newline must not break the page's columns. The sanitizer cleaned the flat Flags list,
// but the templates render flags from their GROUPS, which kept the raw text.
func TestHelpPage_groupedFlagSummaryStaysOnItsRow(t *testing.T) {
	t.Parallel()
	tmpl, err := parseDocTemplate("help", templateHelp)
	if err != nil {
		t.Fatal(err)
	}
	in := &Inputs{Flags: []FlagInput{{Name: "mode", Identifiers: []string{"--mode"}, Summary: "first\tsecond\nthird", Schema: &InputSchema{BaseSchema: BaseSchema{Type: "string"}}}}}
	data := buildHelpData("app", cmdHelp{}, in, nil, nil, nil, "")
	data.Headings.Usage, data.Headings.Flags = "Usage:", "Flags:"
	page, err := renderDocText(tmpl, data)
	if err != nil {
		t.Fatal(err)
	}
	var row string
	for line := range strings.SplitSeq(page, "\n") {
		if strings.Contains(line, "--mode") {
			row = line
		}
	}
	if !strings.Contains(row, "first second third") || strings.Contains(page, "\nthird") {
		t.Errorf("the summary broke its row:\n%s", page)
	}
}
