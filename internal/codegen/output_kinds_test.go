package codegen

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// kindsSpec declares `acme watch`, a stream of `progress` items written inline and `result`
// items from a named schema, told apart by `kind`.
const kindsSpec = `version: 0.0.0
command:
  name: acme
  summary: s
  schemas:
    Result:
      type: object
      description: How the run ended.
      properties:
        kind: { type: string, const: result }
        ok: { type: boolean, description: whether it worked }
      required: [kind, ok]
  commands:
    - name: watch
      summary: watch a run
      output_stream: true
      output:
        description: Progress as the run goes, then its result.
        oneOf:
          - type: object
            description: How far the run has got.
            properties:
              kind: { type: string, const: progress }
              percent: { type: integer, description: how far along }
            required: [kind, percent]
          - $ref: "#/schemas/Result"
`

// kindsConf asks for the pages, the output schema files and the contract.
const kindsConf = `version: 0.0.0
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

// TestOutputKinds_goTypes pins the generated Go shape: a sealed <Prefix>Output interface, a
// struct per inline kind, the named schema's type for a $ref kind, a marker method on each, and
// an OutputDef carrying the tag and each kind's type and schema.
func TestOutputKinds_goTypes(t *testing.T) {
	dir, _ := emitModule(t, kindsSpec, kindsConf)
	raw, err := os.ReadFile(filepath.Join(dir, "internal", "cmd", "acme", "zz_acme.go"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)
	for _, want := range []string{
		"// Progress as the run goes, then its result.\ntype AcmeWatchOutput interface{ isAcmeWatchOutput() }",
		"func (AcmeWatchOutputProgress) isAcmeWatchOutput() {}",
		"func (Result) isAcmeWatchOutput() {}",
		"// How far the run has got.\ntype AcmeWatchOutputProgress struct {",
		"Kind string `json:\"kind\"`",
		"Tag: \"kind\", Kinds: []rotini.OutputKind{{Value: \"progress\", Type: reflect.TypeFor[AcmeWatchOutputProgress](), Schema: `",
		"{Value: \"result\", Type: reflect.TypeFor[Result](), Schema: `",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("generated code lacks %q", want)
		}
	}
	if strings.Contains(src, "type AcmeWatchOutput struct") {
		t.Error("an output of several kinds generated a struct for the whole output")
	}
}

// TestOutputKinds_models pins that the kinds build when the types live in a models package:
// the interface, the inline kind and its marker methods move there, and the cmd package
// re-exports them.
func TestOutputKinds_models(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a module")
	}
	dir, _ := emitModule(t, kindsSpec, `version: 0.0.0
generate:
  packages:
    - type: cmd
      file: internal/cmd/acme/zz_acme.go
      package: acme
    - type: models
      file: internal/models/zz_models.go
      package: models
`)
	models, err := os.ReadFile(filepath.Join(dir, "internal", "models", "zz_models.go"))
	if err != nil {
		t.Fatal(err)
	}
	cmd, err := os.ReadFile(filepath.Join(dir, "internal", "cmd", "acme", "zz_acme.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"type AcmeWatchOutput interface{ isAcmeWatchOutput() }", "func (Result) isAcmeWatchOutput() {}"} {
		if !strings.Contains(string(models), want) {
			t.Errorf("the models package lacks %q", want)
		}
	}
	for _, want := range []string{"AcmeWatchOutput = models.AcmeWatchOutput", "AcmeWatchOutputProgress = models.AcmeWatchOutputProgress"} {
		if !strings.Contains(string(cmd), want) {
			t.Errorf("the cmd package lacks the alias %q", want)
		}
	}
	if out, err := goBuild(t, dir); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
}

// TestOutputKinds_pages pins that help, man and markdown list each kind with its fields.
func TestOutputKinds_pages(t *testing.T) {
	dir, _ := emitModule(t, kindsSpec, kindsConf)
	pages := manPages(t, dir)
	want := map[string][]string{
		"help_acme_watch.txt": {
			"each item's kind says which kind it is:\n  kind: progress  How far the run has got.\n    kind string",
			"  kind: result (Result)  How the run ended.\n    kind string",
		},
		"acme-watch.1": {
			"item's \\fBkind\\fR says which kind it is.\n.SS \"kind: progress\"\nHow far the run has got.\n.TP\n\\fBkind\\fR \\fIstring\\fR",
			".SS \"kind: result (Result)\"\nHow the run ended.\n",
		},
		"markdown_acme_watch.md": {
			"Each item's `kind` says which kind it is.\n\n### `kind: progress`\n\nHow far the run has got.\n\n- `kind` `string` (required)\n- `percent` `integer` — how far along (required)\n",
			"### `kind: result` (`Result`)\n\nHow the run ended.\n\n- `kind` `string` (required)\n- `ok` `boolean` — whether it worked (required)",
		},
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
}

// TestOutputKinds_contract pins that the contract and the output schema file describe the
// stream as standard JSON Schema oneOf, with each kind's tag as a const.
func TestOutputKinds_contract(t *testing.T) {
	dir, _ := emitModule(t, kindsSpec, kindsConf)
	raw, err := os.ReadFile(filepath.Join(dir, "cli-contract.json"))
	if err != nil {
		t.Fatal(err)
	}
	schema, err := compileSchema("contract", schemaContractFileBytes)
	if err != nil {
		t.Fatal(err)
	}
	if problems := validateInstance("contract", raw, schema); len(problems) > 0 {
		t.Fatalf("the contract does not match schema-contract.json: %v", problems)
	}
	var doc contractDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	var watch map[string]any
	for _, c := range doc.Commands {
		if c.Name == "acme watch" {
			watch, _ = c.Output.(map[string]any)
			if !c.Stream {
				t.Error("watch is not marked as a stream")
			}
		}
	}
	kinds, _ := watch["oneOf"].([]any)
	if len(kinds) != 2 {
		t.Fatalf("watch's output has %d oneOf kinds, want 2: %v", len(kinds), watch)
	}
	progress, _ := kinds[0].(map[string]any)
	tag, _ := progress["properties"].(map[string]any)["kind"].(map[string]any)
	if tag["const"] != "progress" {
		t.Errorf("the progress kind's tag = %v, want const progress", tag)
	}
	if ref, _ := kinds[1].(map[string]any)["$ref"].(string); !strings.HasSuffix(ref, "/Result") {
		t.Errorf("the result kind = %v, want a $ref to Result", kinds[1])
	}

	file, err := os.ReadFile(filepath.Join(dir, "schemas", "output", "acme-watch.output.json"))
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := compileSchema("watch", file)
	if err != nil {
		t.Fatalf("the output schema file does not compile: %v\n%s", err, file)
	}
	for item, valid := range map[string]bool{
		`{"kind":"progress","percent":50}`: true,
		`{"kind":"result","ok":true}`:      true,
		`{"kind":"result","percent":50}`:   false,
		`{"kind":"other"}`:                 false,
	} {
		if problems := validateInstance("item", []byte(item), compiled); (len(problems) == 0) != valid {
			t.Errorf("%s: valid = %v, want %v (%v)", item, len(problems) == 0, valid, problems)
		}
	}
}
