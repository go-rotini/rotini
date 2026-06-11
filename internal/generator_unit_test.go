package internal

// Focused unit tests for generator.go's pure helpers — the spec→literal and
// spec→display derivations the end-to-end generate tests exercise only on
// their happy paths.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultString(t *testing.T) {
	cases := []struct {
		in   any
		want string
	}{
		{nil, ""},
		{"text", "text"},
		{true, "true"},
		{float64(2.5), "2.5"},
		{int(7), "7"},
		{int64(9), "9"},
		{[]string{"x"}, "[x]"}, // anything else falls through to %v
	}
	for _, tc := range cases {
		if got := defaultString(tc.in); got != tc.want {
			t.Errorf("defaultString(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestGoRawString(t *testing.T) {
	if got := goRawString(`{"a":1}`); got != "`"+`{"a":1}`+"`" {
		t.Errorf("goRawString(plain) = %s, want a backtick literal", got)
	}
	if got := goRawString("has`tick"); got != `"has`+"`"+`tick"` {
		t.Errorf("goRawString(backtick) = %s, want a quoted literal", got)
	}
}

func TestJSONSchemaTypeToGo(t *testing.T) {
	cases := map[string]string{
		"boolean": "bool", "bool": "bool",
		"integer": "int", "int": "int",
		"number": "float64", "float64": "float64",
		"array": "[]string", "[]string": "[]string",
		"object": "map[string]any", "map": "map[string]any",
		"duration": "time.Duration",
		"time":     "time.Time", "datetime": "time.Time", "date": "time.Time",
		"uuid.UUID": "uuid.UUID", // unknown types pass through
	}
	for in, want := range cases {
		if got := jsonSchemaTypeToGo(in); got != want {
			t.Errorf("jsonSchemaTypeToGo(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestGoFieldType(t *testing.T) {
	if got := goFieldType(nil); got != "string" {
		t.Errorf("goFieldType(nil) = %q, want string", got)
	}
	nullable := &InputSchema{BaseSchema: BaseSchema{Type: "int"}, Required: false}
	nullable.Nullable = true
	if got := goFieldType(nullable); got != "*int" {
		t.Errorf("goFieldType(nullable int) = %q, want *int", got)
	}
}

func TestEnvVarOf(t *testing.T) {
	if got := envVarOf(nil); got != "" {
		t.Errorf("envVarOf(nil) = %q, want empty", got)
	}
	if got := envVarOf(&InputSchema{Variable: "APP_TOKEN"}); got != "APP_TOKEN" {
		t.Errorf("envVarOf(explicit) = %q, want APP_TOKEN", got)
	}
}

func TestConfigLocation(t *testing.T) {
	cases := []struct {
		name  string
		input ConfigInput
		want  string
	}{
		{"nil schema", ConfigInput{Name: "x"}, ""},
		{"file and key", ConfigInput{Name: "x", Schema: &InputSchema{File: "app", Key: "a.b"}}, "app.a.b"},
		{"file without key uses the name", ConfigInput{Name: "x", Schema: &InputSchema{File: "app"}}, "app.x"},
		{"bare key", ConfigInput{Name: "x", Schema: &InputSchema{Key: "a.b"}}, "a.b"},
		{"neither", ConfigInput{Name: "x", Schema: &InputSchema{}}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := configLocation(tc.input); got != tc.want {
				t.Errorf("configLocation = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestFlagIdentifiers(t *testing.T) {
	explicit := FlagInput{Name: "out", Identifiers: []string{"-o"}}
	if got := flagIdentifiers(explicit); len(got) != 1 || got[0] != "-o" {
		t.Errorf("flagIdentifiers(explicit) = %v", got)
	}
	derived := FlagInput{Name: "dry_run"}
	if got := flagIdentifiers(derived); len(got) != 1 || got[0] != "--dry-run" {
		t.Errorf("flagIdentifiers(derived) = %v, want [--dry-run]", got)
	}
}

func TestSchemaAccessors_nil(t *testing.T) {
	if got := schemaDefaultString(nil); got != "" {
		t.Errorf("schemaDefaultString(nil) = %q, want empty", got)
	}
	if got := enumOf(nil); got != nil {
		t.Errorf("enumOf(nil) = %v, want nil", got)
	}
}

func TestConstraintsLiteral(t *testing.T) {
	full := &InputSchema{}
	full.Minimum, full.Maximum = 1, 65535
	full.MinLength, full.MaxLength = 2, 5
	full.MinItems, full.MaxItems = 3, 4
	full.Pattern = "^x$"
	got := constraintsLiteral(full)
	for _, want := range []string{
		"Minimum: 1", "Maximum: 65535", "MinLength: 2", "MaxLength: 5",
		"MinItems: 3", "MaxItems: 4", `Pattern: "^x$"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("constraintsLiteral missing %q in %s", want, got)
		}
	}
	if got := constraintsLiteral(&InputSchema{}); got != "" {
		t.Errorf("constraintsLiteral(zero) = %q, want empty", got)
	}
}

func TestRemoteDefsLiteral(t *testing.T) {
	rcs := []RemoteCommandSpec{
		{Name: "plugin", Aliases: []string{"p"}, Timeout: "10s"},
		{Name: "noop", Timeout: "not-a-duration"}, // unparsable timeout is dropped
	}
	got := remoteDefsLiteral("acme", rcs)
	for _, want := range []string{
		`Name: "plugin"`, `Binary: "acme-plugin"`, `Aliases: []string{"p"}`,
		"Timeout: 10000000000", `Binary: "acme-noop"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("remoteDefsLiteral missing %q in %s", want, got)
		}
	}
	if strings.Contains(got, `Name: "noop", Binary: "acme-noop", Timeout`) {
		t.Errorf("unparsable timeout should be omitted: %s", got)
	}
	if got := remoteDefsLiteral("acme", nil); got != "" {
		t.Errorf("remoteDefsLiteral(none) = %q, want empty", got)
	}
}

func TestResolveHeadings(t *testing.T) {
	defaults := resolveHeadings(cmdHelp{})
	if defaults.Usage != "Usage:" || defaults.Cascading != "Global Flags:" {
		t.Errorf("default headings wrong: %+v", defaults)
	}

	overridden := resolveHeadings(cmdHelp{Headings: &HelpHeadings{
		Usage: "USAGE", Commands: "CMDS", Arguments: "ARGS", Flags: "OPTS",
		Environment: "ENV", Configuration: "CONF", Cascading: "INHERITED", Examples: "EG",
	}})
	want := templateDocHeadings{
		Usage: "USAGE", Commands: "CMDS", Arguments: "ARGS", Flags: "OPTS",
		Environment: "ENV", Configuration: "CONF", Cascading: "INHERITED", Examples: "EG",
	}
	if overridden != want {
		t.Errorf("overridden headings = %+v, want %+v", overridden, want)
	}
}

func TestAddImport_dedupes(t *testing.T) {
	gp := &genProgram{}
	gp.addImport("childcli", "example.com/child")
	gp.addImport("othercli", "example.com/child") // same path: dropped
	gp.addImport("othercli", "example.com/other")
	if len(gp.childImports) != 2 {
		t.Fatalf("childImports = %v, want 2 unique paths", gp.childImports)
	}
	if gp.childImports[0].Alias != "childcli" || gp.childImports[1].Path != "example.com/other" {
		t.Errorf("childImports = %v", gp.childImports)
	}
}

func TestChildCliImport(t *testing.T) {
	// A child conf naming the cmd package wins.
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, ".rotini.conf.yaml"),
		"generate:\n  packages:\n    cmd:\n      package: custom/handlers\n")
	specPath := filepath.Join(dir, ".rotini.spec.yaml")
	if got := childCliImport(specPath, "example.com/mod"); got != "example.com/mod/custom/handlers" {
		t.Errorf("childCliImport(with conf) = %q", got)
	}

	// A conf without a cmd package falls through to the convention.
	dir2 := t.TempDir()
	writeTestFile(t, filepath.Join(dir2, ".rotini.conf.yaml"), "validate:\n  fail: collect\n")
	if got := childCliImport(filepath.Join(dir2, ".rotini.spec.yaml"), "example.com/mod"); got != "example.com/mod/internal/cmd/"+filepath.Base(dir2) {
		t.Errorf("childCliImport(conf without cmd) = %q", got)
	}

	// No conf at all also falls through.
	dir3 := t.TempDir()
	if got := childCliImport(filepath.Join(dir3, ".rotini.spec.yaml"), "example.com/mod"); got != "example.com/mod/internal/cmd/"+filepath.Base(dir3) {
		t.Errorf("childCliImport(no conf) = %q", got)
	}
}

func TestWriteCompletionFiles_errors(t *testing.T) {
	if err := writeCompletionFiles("", "app", nil); err == nil {
		t.Error("writeCompletionFiles(empty dir) = nil, want an error")
	}
	badShell := []helpNode{{name: "nushell", file: "completion_nushell.txt"}}
	if err := writeCompletionFiles(t.TempDir(), "app", badShell); err == nil {
		t.Error("writeCompletionFiles(unsupported shell) = nil, want an error")
	}
}

func TestWriteFeatureFiles_emptyDir(t *testing.T) {
	if err := writeFeatureFiles("", nil, helpFeatureDesc); err == nil {
		t.Error("writeFeatureFiles(empty dir) = nil, want an error")
	}
}

// TestLoadFeatureTemplate_badUserTemplate confirms a user-edited template that
// no longer parses fails loudly (naming the feature and path) instead of
// silently falling back to the embedded default.
func TestLoadFeatureTemplate_badUserTemplate(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, helpTemplateName), "{{")
	if _, err := loadFeatureTemplate(dir, helpFeatureDesc); err == nil || !strings.Contains(err.Error(), "help template") {
		t.Errorf("loadFeatureTemplate(bad template) = %v, want a parse error naming the template", err)
	}
}

// TestGenerateFeatureDirOutsideCmdgen confirms a feature dir that does not
// resolve under the cmdgen package is rejected — //go:embed could not reach it.
func TestGenerateFeatureDirOutsideCmdgen(t *testing.T) {
	tmp := t.TempDir()
	writeTestFile(t, filepath.Join(tmp, "go.mod"), minimalGoMod)
	writeTestFile(t, filepath.Join(tmp, ".rotini.spec.yaml"),
		"$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-spec.json\ncommand:\n  name: mycli\n")
	writeTestFile(t, filepath.Join(tmp, ".rotini.conf.yaml"),
		confSchemaHeader+"generate:\n  features:\n    help:\n      enabled: true\n      dir: docs/help\n")
	t.Chdir(tmp)

	err := Generate(".rotini.spec.yaml", ".rotini.conf.yaml", false, "", nil)
	if err == nil || !strings.Contains(err.Error(), "must resolve under the cmdgen package") {
		t.Errorf("Generate(feature dir outside cmdgen) = %v, want a must-resolve-under error", err)
	}
}

// TestProcessorValidate_nilCallback confirms a nil onValidate is tolerated (the
// handlers may pass nil when they have no per-pass reporting).
func TestProcessorValidate_nilCallback(t *testing.T) {
	spec := writeTemp(t, "spec.yaml", validSpecHeader+"command:\n  name: demo\n")
	if err := NewProcessor("").Validate(spec, "", false, "", nil); err != nil {
		t.Errorf("Validate(nil callback) = %v, want nil", err)
	}
}

func TestStubFilename(t *testing.T) {
	cases := map[string]string{
		"app":         "app.go",
		"app_test":    "app_test_.go",    // _test.go escape
		"app_windows": "app_windows_.go", // GOOS escape
		"app_wasm":    "app_wasm_.go",    // GOARCH escape
		"app_build":   "app_build.go",
	}
	for in, want := range cases {
		if got := stubFilename(in); got != want {
			t.Errorf("stubFilename(%q) = %q, want %q", in, got, want)
		}
	}
	if got := commandStubFilename("app", "build", "custom.go"); got != "custom.go" {
		t.Errorf("commandStubFilename(override) = %q, want custom.go", got)
	}
}

func TestSnakeUpper(t *testing.T) {
	cases := map[string]string{
		"apiKey":      "API_KEY",
		"max-retries": "MAX_RETRIES",
		"a b":         "A_B",
		"v2Beta":      "V2_BETA",
	}
	for in, want := range cases {
		if got := snakeUpper(in); got != want {
			t.Errorf("snakeUpper(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestWriteEntrypoint_createOnce confirms the entrypoint main.go is never
// overwritten, and that an undeclared entrypoint writes nothing.
func TestWriteEntrypoint_createOnce(t *testing.T) {
	dir := t.TempDir()
	lay := layout{
		handlerImport:  "example.com/mod/internal/cmd/app",
		entrypointDir:  dir,
		entrypointFile: "main.go",
	}
	if err := writeEntrypoint(lay); err != nil {
		t.Fatalf("writeEntrypoint: %v", err)
	}
	path := filepath.Join(dir, "main.go")
	writeTestFile(t, path, "package main // EDITED\n")
	if err := writeEntrypoint(lay); err != nil {
		t.Fatalf("writeEntrypoint (second): %v", err)
	}
	mustContain(t, path, "EDITED")

	if err := writeEntrypoint(layout{}); err != nil {
		t.Errorf("writeEntrypoint(no entrypoint) = %v, want nil", err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("unexpected files written: %v", entries)
	}
}

// TestGenerateHiddenInDefinition confirms hidden commands/flags/arguments are
// recorded in the generated Definition — the runtime excludes them from shell
// completion (they still parse and dispatch).
func TestGenerateHiddenInDefinition(t *testing.T) {
	tmp := t.TempDir()
	writeTestFile(t, filepath.Join(tmp, "go.mod"), minimalGoMod)
	writeTestFile(t, filepath.Join(tmp, ".rotini.spec.yaml"),
		"$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-spec.json\n"+
			"command:\n"+
			"  name: app\n"+
			"  inputs:\n"+
			"    flags:\n"+
			"      - name: secret\n"+
			"        hidden: true\n"+
			"        identifiers: [--secret]\n"+
			"        schema: { type: bool }\n"+
			"      - name: loud\n"+
			"        identifiers: [--loud]\n"+
			"        schema: { type: bool }\n"+
			"    arguments:\n"+
			"      - name: ghostarg\n"+
			"        hidden: true\n"+
			"        schema: { type: string }\n"+
			"  commands:\n"+
			"    - name: ghost\n"+
			"      hidden: true\n"+
			"    - name: run\n")
	t.Chdir(tmp)
	if err := Generate(".rotini.spec.yaml", "", false, "", nil); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	gen := filepath.Join(tmp, "internal", "cmd", "app", "zz_rotini.gen.go")
	mustContain(t, gen,
		`{Name: "secret", Identifiers: []string{"--secret"}, Type: "bool", Hidden: true}`,
		`{Name: "ghostarg", Type: "string", Hidden: true}`,
	)
	src := readFileString(t, gen)
	ghost := src[strings.Index(src, `Name: "ghost"`):]
	if !strings.Contains(ghost[:200], "Hidden:") {
		t.Errorf("ghost command literal missing Hidden:\n%s", ghost[:200])
	}
	if strings.Contains(src, `Name: "loud", Identifiers: []string{"--loud"}, Type: "bool", Hidden`) {
		t.Error("visible flag must not carry Hidden")
	}
}
