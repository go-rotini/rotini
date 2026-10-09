package codegen

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

const repeatSpec = `version: 0.0.0
command:
  name: app
  flags:
    - name: name
      schema: { type: string, repeatable: false }
    - name: level
      schema: { type: int, repeatable: true }
    - name: tag
      schema: { type: '[]int', uniqueItems: true }
  env:
    - name: hosts
      schema: { type: '[]string', uniqueItems: true }
`

func repeatProgram(t *testing.T) *program {
	t.Helper()
	spec := decodeSpecYAML(t, repeatSpec)
	gp, err := resolveTree(spec, filepath.Join(t.TempDir(), ".rotini.spec.yaml"), "example.com/app")
	if err != nil {
		t.Fatal(err)
	}
	gp.spec = spec
	return gp
}

// TestDefinitionLiteral_repeatFacts pins the runtime data `repeatable: false` and
// `uniqueItems` generate, and that `repeatable: true` (the default) generates nothing.
func TestDefinitionLiteral_repeatFacts(t *testing.T) {
	got := renderDefinition(repeatProgram(t))
	for _, want := range []string{
		`Name: "name", Identifiers: []string{"--name"}, Type: "string", NoRepeat: true`,
		`Constraints: rotini.Constraints{UniqueItems: true}`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("definition lacks %q:\n%s", want, got)
		}
	}
	if n := strings.Count(got, "NoRepeat"); n != 1 {
		t.Errorf("NoRepeat appears %d times, want 1", n)
	}
	schema := &InputSchema{Type: "[]string", UniqueItems: true}
	if tags := constraintTags(schema); tags != `unique:"true"` {
		t.Errorf("constraintTags = %q, want the unique tag", tags)
	}
}

// TestContract_repeatFacts pins `repeatable: false` on a flag entry, and `uniqueItems` on the
// list's own schema rather than its items.
func TestContract_repeatFacts(t *testing.T) {
	gp := repeatProgram(t)
	raw, err := gp.contract(gp.contractNodes())
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Commands []struct {
			Flags []struct {
				Name       string         `json:"name"`
				Repeatable *bool          `json:"repeatable"`
				Schema     map[string]any `json:"schema"`
			} `json:"flags"`
			Env []struct {
				Schema map[string]any `json:"schema"`
			} `json:"env"`
		} `json:"commands"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	flags := doc.Commands[0].Flags
	if r := flags[0].Repeatable; r == nil || *r {
		t.Errorf("name: repeatable = %v, want false", r)
	}
	if flags[1].Repeatable != nil {
		t.Errorf("level: repeatable = %v, want it left out", *flags[1].Repeatable)
	}
	if flags[2].Schema["uniqueItems"] != true || doc.Commands[0].Env[0].Schema["uniqueItems"] != true {
		t.Errorf("uniqueItems missing: flag %v, env %v", flags[2].Schema, doc.Commands[0].Env[0].Schema)
	}
	if items, _ := flags[2].Schema["items"].(map[string]any); items["uniqueItems"] != nil {
		t.Errorf("uniqueItems moved onto items: %v", items)
	}
	schema, err := compileSchema("contract", schemaContractFileBytes)
	if err != nil {
		t.Fatal(err)
	}
	if problems := validateInstance("contract", raw, schema); len(problems) > 0 {
		t.Errorf("the contract does not match schema-contract.json: %v", problems)
	}
}
