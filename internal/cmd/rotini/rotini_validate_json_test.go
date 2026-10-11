package rotini

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-rotini/jsonschema"
)

// `rotini validate --format json` writes what validate's `output:` declares, for a real spec
// with an error and a warning.
func TestCLI_validateJSONMatchesItsOutput(t *testing.T) {
	var declared string
	for _, c := range definition.Commands {
		if c.Name == "validate" && c.Output != nil {
			declared = c.Output.Schema
		}
	}
	if declared == "" {
		t.Fatal("validate declares no output schema")
	}
	schema, err := jsonschema.Compile([]byte(declared))
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	spec := "version: " + testVersion + `
command:
  name: app
  commands:
    - name: list
      flags:
        - name: limit
          identifiers: [--limit]
          agent: true
          schema: { type: int }
    - name: bad
      flags:
        - name: one
          identifiers: [--same]
          schema: { type: bool }
        - name: two
          identifiers: [--same]
          schema: { type: bool }
`
	if err := os.WriteFile(filepath.Join(dir, ".rotini.spec.yaml"), []byte(spec), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	p, out, _ := newTestCLI(t)
	if code, _ := p.Run([]string{"validate", ".rotini.spec.yaml", "--format", "json"}); code != 1 {
		t.Fatalf("exit = %d, want 1:\n%s", code, out.String())
	}
	result, err := schema.Validate(out.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if !result.Valid {
		t.Errorf("stdout doesn't match validate's declared output: %v\n%s", result.Errors, out.String())
	}
	var got RotiniValidateOutput
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	severities := map[string]bool{}
	for _, pr := range got.Problems {
		severities[pr.Severity] = true
		if pr.Line == 0 {
			t.Errorf("problem %+v has no line", pr)
		}
	}
	if !severities["error"] || !severities["warning"] {
		t.Errorf("want an error and a warning: %s", out.String())
	}
}
