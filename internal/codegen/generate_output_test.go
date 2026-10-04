package codegen

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// outputConf renders all three page features from manSpec, whose `status` declares the long
// form of output, `deploy` the short form, and the root an exit status that writes output.
const outputConf = `version: 0.0.0
generate:
  packages:
    - type: main
      file: cmd/acme/main.go
      package: main
    - type: cmd
      file: internal/cmd/acme/zz_acme.go
      package: acme
  features:
    - type: help
      enabled: true
      embed: true
    - type: man
      enabled: true
      embed: true
    - type: markdown
      enabled: true
      embed: true
`

// TestOutputSection pins the OUTPUT section on each kind of page: the shape's description and
// type, its top-level fields, and an exit status that still writes output.
func TestOutputSection(t *testing.T) {
	dir, _ := emitModule(t, manSpec, outputConf)
	pages := manPages(t, dir)
	want := map[string][]string{
		"help_acme_status.txt": {"Output:\n  One status per service.\n  []Status"},
		"help_acme_deploy.txt": {
			"Output:\n  Status\n" +
				"    healthy boolean    whether it answers its \\\\health check\n" +
				"    service string     the service .name (required)",
		},
		"acme-status.1": {".SH OUTPUT\nOne status per service.\n.PP\nWrites \\fB[]Status\\fR to standard output.\n.SH \"SEE ALSO\""},
		"acme-deploy.1": {
			".SH OUTPUT\nWrites \\fBStatus\\fR to standard output.\n.TP\n\\fBhealthy\\fR \\fIboolean\\fR\nwhether it answers its \\e\\ehealth check\n" +
				".TP\n\\fBservice\\fR \\fIstring\\fR\nthe service .name (required)\n.SH \"SEE ALSO\"",
		},
		"acme.1":                  {".TP\n\\fB3\\fR\nsome deploys failed (writes \\fB[]Status\\fR to standard output)\n"},
		"markdown_acme_status.md": {"## Output\n\nOne status per service.\n\nWrites `[]Status` to stdout."},
		"markdown_acme_deploy.md": {
			"## Output\n\nWrites `Status` to stdout.\n\n" +
				"- `healthy` `boolean` — whether it answers its \\\\health check\n- `service` `string` — the service .name (required)",
		},
		"markdown_acme.md": {"- `3` — some deploys failed (writes `[]Status` to stdout)"},
	}
	for name, subs := range want {
		page, ok := pages[name]
		if !ok {
			t.Fatalf("no page %s; have %v", name, keysOf(pages))
		}
		for _, sub := range subs {
			if !strings.Contains(page, sub) {
				t.Errorf("%s lacks %q:\n%s", name, sub, page)
			}
		}
	}
	// No output declared, no section.
	for _, name := range []string{"help_acme.txt", "acme.1", "markdown_acme.md"} {
		if p := pages[name]; strings.Contains(p, "Output:") || strings.Contains(p, ".SH OUTPUT") || strings.Contains(p, "## Output") {
			t.Errorf("%s has an OUTPUT section but the root declares no output:\n%s", name, p)
		}
	}
}

// TestOutputSection_headingOverride: `headings.output` on a command replaces its help heading verbatim.
func TestOutputSection_headingOverride(t *testing.T) {
	spec := strings.Replace(manSpec, "      summary: show status\n", "      summary: show status\n      headings:\n        output: \"Writes:\"\n", 1)
	dir, _ := emitModule(t, spec, outputConf)
	if p := manPages(t, dir)["help_acme_status.txt"]; !strings.Contains(p, "Writes:\n  One status per service.") {
		t.Errorf("the output heading was not overridden:\n%s", p)
	}
}

func TestShapeTypeName(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in   *Schema
		want string
	}{
		{nil, ""},
		{&Schema{Ref: "#/schemas/Task"}, "Task"},
		{&Schema{Type: "array", Items: &Schema{Ref: "#/schemas/Task"}}, "[]Task"},
		{&Schema{Type: "[]string"}, "[]string"},
		{&Schema{Properties: map[string]Schema{"a": {Type: "string"}}}, "object"},
		{&Schema{Type: "integer"}, "integer"},
	}
	for _, tt := range tests {
		if got := shapeTypeName(tt.in); got != tt.want {
			t.Errorf("shapeTypeName(%+v) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// contractConf asks for the output schema files and the contract document.
const contractConf = `version: 0.0.0
generate:
  schemas:
    output:
      dir: schemas/output
  contract:
    file: cli-contract.json
  packages:
    - type: cmd
      file: internal/cmd/acme/zz_acme.go
      package: acme
`

// TestOutputSchemaFiles: one standard JSON Schema per declared output, named after the page,
// with the named schemas it reaches as definitions; none for a hidden command or an undeclared
// output; and a stale file the directory still holds is removed.
func TestOutputSchemaFiles(t *testing.T) {
	dir, _ := emitModule(t, manSpec, contractConf)
	outDir := filepath.Join(dir, "schemas", "output")
	entries, err := os.ReadDir(outDir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	want := []string{"acme-deploy.output.json", "acme-status.output.json", "acme.exit-3.output.json"}
	if !slices.Equal(names, want) {
		t.Fatalf("output schema files = %v, want %v", names, want)
	}
	for _, name := range names {
		raw, err := os.ReadFile(filepath.Join(outDir, name))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := compileSchema(name, raw); err != nil {
			t.Errorf("%s is not a usable JSON Schema: %v\n%s", name, err, raw)
		}
	}
	status, _ := os.ReadFile(filepath.Join(outDir, "acme-status.output.json"))
	var doc map[string]any
	if err := json.Unmarshal(status, &doc); err != nil {
		t.Fatal(err)
	}
	items, _ := doc["items"].(map[string]any)
	if doc["type"] != "array" || items["$ref"] != "#/definitions/Status" || doc["title"] != "acme status output" ||
		doc["description"] != "One status per service." {
		t.Errorf("acme-status.output.json header is wrong:\n%s", status)
	}
	def, _ := doc["definitions"].(map[string]any)["Status"].(map[string]any)
	props, _ := def["properties"].(map[string]any)
	if healthy, _ := props["healthy"].(map[string]any); healthy["type"] != "boolean" {
		t.Errorf("Status.healthy should be a JSON Schema boolean:\n%s", status)
	}

	// A file no output produces any more is removed; any other file is left alone.
	writeTestFile(t, dir, "schemas/output/acme-gone.output.json", "{}")
	writeTestFile(t, dir, "schemas/output/notes.json", "{}")
	if err := NewProcessor("0.0.0").Generate(".rotini.spec.yaml", ".rotini.conf.yaml", false, func(string, error) {}, func([]error) {}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(outDir, "acme-gone.output.json")); !os.IsNotExist(err) {
		t.Error("the stale output schema was not removed")
	}
	if _, err := os.Stat(filepath.Join(outDir, "notes.json")); err != nil {
		t.Errorf("a file rotini did not write was touched: %v", err)
	}
}

// TestContractDocument: the contract validates against its own published schema, and carries
// what a tool definition needs for each visible command.
func TestContractDocument(t *testing.T) {
	dir, _ := emitModule(t, manSpec, contractConf)
	raw, err := os.ReadFile(filepath.Join(dir, "cli-contract.json"))
	if err != nil {
		t.Fatal(err)
	}
	schema, err := compileSchema("contract", schemaContractFileBytes)
	if err != nil {
		t.Fatal(err)
	}
	if problems := validateInstance("contract", raw, schema); len(problems) > 0 {
		t.Fatalf("the contract does not match schema-contract.json: %v\n%s", problems, raw)
	}

	var doc contractDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	var names []string
	byName := map[string]contractCommand{}
	for _, c := range doc.Commands {
		names = append(names, c.Name)
		byName[c.Name] = c
	}
	if !slices.Equal(names, []string{"acme", "acme deploy", "acme status"}) {
		t.Fatalf("commands = %v; the hidden one must be left out", names)
	}
	if doc.Format != contractFormat || doc.Name != "acme" || doc.Definitions["Status"] == nil || len(doc.Errors) == 0 {
		t.Errorf("document header is wrong:\n%s", raw)
	}

	root := byName["acme"]
	if len(root.Env) != 1 || root.Env[0].Variables[0] != "ACME_TOKEN" || !root.Env[0].Secret {
		t.Errorf("root env = %+v", root.Env)
	}
	if len(root.ExitStatus) != 3 || root.ExitStatus[2].Output == nil {
		t.Errorf("root exit statuses = %+v", root.ExitStatus)
	}

	deploy := byName["acme deploy"]
	if len(deploy.Flags) != 2 || deploy.Flags[0].Name != "replicas" || !deploy.Flags[1].Inherited || deploy.Flags[1].Name != "verbose" {
		t.Errorf("deploy flags (own, then inherited) = %+v", deploy.Flags)
	}
	params, _ := json.Marshal(deploy.Parameters)
	for _, want := range []string{
		`"service":{"description":"service to deploy","enum":["web","api"],"type":"string"}`,
		`"replicas":{"default":1,"description":"replica count","maximum":10,"minimum":1,"type":"integer"}`,
		`"rest":{"description":"extra args","items":{"type":"string"},"type":"array"}`,
		`"required":["service"]`,
		`"additionalProperties":false`,
	} {
		if !strings.Contains(string(params), want) {
			t.Errorf("deploy parameters lack %s:\n%s", want, params)
		}
	}
	if out, _ := json.Marshal(deploy.Output); string(out) != `{"$ref":"#/definitions/Status"}` {
		t.Errorf("deploy output = %s, want the shape alone", out)
	}
}

func TestStandardSchema(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in, want string
	}{
		{`{"type":"int"}`, `{"type":"integer"}`},
		{`{"type":"[]duration"}`, `{"items":{"type":"string"},"type":"array"}`},
		{`{"type":"array","items":{"type":"float64"}}`, `{"items":{"type":"number"},"type":"array"}`},
		{`{"type":"map[string]int"}`, `{"additionalProperties":{"type":"integer"},"type":"object"}`},
		{`{"type":"datetime"}`, `{"format":"date-time","type":"string"}`},
		{`{"type":"string","nullable":true}`, `{"type":["string","null"]}`},
		{`{"type":"uuid.UUID","import":"github.com/google/uuid"}`, `{}`},
		{`{"type":"object","properties":{"a":{"type":"bool","pattern_message":"x"}}}`, `{"properties":{"a":{"type":"boolean"}},"type":"object"}`},
	}
	for _, tt := range tests {
		var v any
		if err := json.Unmarshal([]byte(tt.in), &v); err != nil {
			t.Fatal(err)
		}
		got, _ := json.Marshal(standardSchema(v))
		if string(got) != tt.want {
			t.Errorf("standardSchema(%s) = %s, want %s", tt.in, got, tt.want)
		}
	}
}
