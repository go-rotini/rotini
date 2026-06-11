package internal

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
	if _, err := renderMainFile("example.com/app/internal/cmd/app", "cli"); err != nil {
		t.Errorf("main: %v", err)
	}
	if _, err := renderHandlerStubFile("cli", "appSubHandlers"); err != nil {
		t.Errorf("handler stub: %v", err)
	}
	if _, err := renderHandlerRootFile("cli", "appHandlers", "App", "HelpApp"); err != nil {
		t.Errorf("handler root: %v", err)
	}
	if _, err := renderHandlerVersionFile("cli", "appVersionHandlers", "App", "HelpAppVersion"); err != nil {
		t.Errorf("handler version: %v", err)
	}
	if _, err := renderHandlerHelpFile("cli", "appHelpHandlers", "App", "HelpAppHelp"); err != nil {
		t.Errorf("handler help: %v", err)
	}
}

func TestSmokeRenderHandlersFile(t *testing.T) {
	out, err := renderHandlersFile(templateHandlersData{
		Package:         "cli",
		FrameworkImport: "example.com/app/internal/cmd/app/cligen",
		FrameworkQual:   "cligen.",
		ChildImports:    []templateHandlersImport{{Alias: "childcli", Path: "example.com/child/cli"}},
		Methods: []templateHandlersMethod{
			{Method: "App", HandlerType: "appHandlers"},
			{Method: "AppChild", Composed: true, DelegateAlias: "childcli", DelegateMethod: "Child"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("handlers:\n%s", out)
}

func TestSmokeRenderRotiniFile(t *testing.T) {
	out, err := renderRotiniFile(templateRotiniData{
		Package:    "cligen",
		Imports:    []string{`"time"`},
		Methods:    []string{"App", "AppGenerate"},
		Definition: "var definition = rotini.Definition{}",
		Blocks: []templateInputBlock{
			{
				Prefix: "App",
				Flags: []templateInputField{
					{Field: "Verbose", GoType: "bool", Tag: inputFieldTag("flag:verbose", "", "", "")},
				},
				Arguments: []templateInputField{
					{Field: "Paths", GoType: "[]string", Tag: inputFieldTag("argument:paths", "", "", "")},
				},
				Env: []templateInputField{
					{Field: "Home", GoType: "string", Tag: inputFieldTag("env:home", "key:home", "APP_HOME", `min:"1"`)},
				},
				Config: []templateInputField{
					{Field: "Timeout", GoType: "time.Duration", Tag: inputFieldTag("config:timeout", "key:timeout", "", "")},
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
	if !strings.Contains(string(out), "\"time\"\n\n\t\"github.com/go-rotini/rotini\"") {
		t.Error("imports should be grouped std then third-party")
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

func TestSmokeRenderHelpFile(t *testing.T) {
	out, err := renderHelpFile(smokeDocData())
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
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
	out, err := renderManFile(smokeDocData())
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
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

func TestRenderDocFile_parseError(t *testing.T) {
	if _, err := renderDocFile("broken", "{{", templateHelpData{}); err == nil {
		t.Error("renderDocFile(unparsable) = nil, want a parse error")
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

func TestMergeGenFile_parseErrors(t *testing.T) {
	good := []byte("package x\n\nvar A = 1\n")
	if _, err := mergeGenFile("x", []byte("not go"), good); err == nil {
		t.Error("mergeGenFile(bad rollup) = nil, want a parse error")
	}
	if _, err := mergeGenFile("x", good, []byte("not go")); err == nil {
		t.Error("mergeGenFile(bad framework) = nil, want a parse error")
	}
}

func TestInputFieldTag(t *testing.T) {
	cases := []struct {
		rotini, recon, envVar, constraint, want string
	}{
		{"name", "", "", "", "`rotini:\"name\"`"},
		{"name", "key", "", "", "`rotini:\"name\" recon:\"key\"`"},
		{"name", "key", "VAR", "", "`rotini:\"name\" recon:\"key\" env:\"VAR\"`"},
		{"name", "key", "VAR", `min:"1"`, "`rotini:\"name\" recon:\"key\" env:\"VAR\" min:\"1\"`"},
	}
	for _, tc := range cases {
		if got := inputFieldTag(tc.rotini, tc.recon, tc.envVar, tc.constraint); got != tc.want {
			t.Errorf("inputFieldTag(%q,%q,%q,%q) = %s, want %s", tc.rotini, tc.recon, tc.envVar, tc.constraint, got, tc.want)
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
