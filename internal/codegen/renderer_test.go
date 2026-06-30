package codegen

import (
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
	if _, err := renderMainFile("example.com/app/internal/cmd/app", "cli", "yaml"); err != nil {
		t.Errorf("main: %v", err)
	}
	// Every command — including help/version/completion — gets the same empty stub.
	if _, err := renderHandlerStubFile("cli", "appSubHandlers", `rotini "example.com/app/internal/cmd/app/rotini"`); err != nil {
		t.Errorf("handler stub: %v", err)
	}
}

func TestSmokeRenderRotiniFile(t *testing.T) {
	out, err := renderRotiniFile(templateRotiniData{
		Package:       "cligen",
		RuntimeImport: `rotini "github.com/go-rotini/rotini/internal/runtime"`,
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
	_, err := renderGoFile("invalid", "package x\nfunc {", nil)
	if err == nil || !strings.Contains(err.Error(), "gofmt") {
		t.Errorf("renderGoFile(invalid Go) = %v, want a gofmt error with source context", err)
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

func TestSplitGoFile_parseError(t *testing.T) {
	if _, _, err := splitGoFile([]byte("not go")); err == nil {
		t.Error("splitGoFile(invalid) = nil, want a parse error")
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
