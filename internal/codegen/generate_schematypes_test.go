package codegen

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
)

// TestNamedSchemaRotiniTypes pins that a named schema's rotini type names (int, bool, []int,
// duration) are written as JSON Schema in the runtime validation schema and generate typed
// fields, as their JSON Schema spellings do, while pattern_message and a type rotini doesn't
// know are kept.
func TestNamedSchemaRotiniTypes(t *testing.T) {
	var schemas map[string]Schema
	if err := json.Unmarshal([]byte(`{"DB": {"type": "object", "properties": {
		"port": {"type": "int", "minimum": 1},
		"tls": {"type": "bool"},
		"ports": {"type": "[]int"},
		"timeout": {"type": "duration"},
		"name": {"type": "string", "pattern": "^a", "pattern_message": "starts with a"},
		"id": {"type": "Thing"}}}}`), &schemas); err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Definitions map[string]struct {
			Properties map[string]map[string]any `json:"properties"`
		} `json:"definitions"`
	}
	if err := json.Unmarshal([]byte(validationSchema(Schema{BaseSchema: BaseSchema{Ref: "#/schemas/DB"}}, schemas)), &doc); err != nil {
		t.Fatal(err)
	}
	props := doc.Definitions["DB"].Properties
	for name, want := range map[string]string{"port": "integer", "tls": "boolean", "ports": "array", "timeout": "string", "name": "string", "id": "Thing"} {
		if got := props[name]["type"]; got != want {
			t.Errorf("%s: type = %v, want %s", name, got, want)
		}
	}
	if items, _ := props["ports"]["items"].(map[string]any); items["type"] != "integer" {
		t.Errorf("ports: items = %v, want integer", props["ports"]["items"])
	}
	if props["name"]["pattern_message"] != "starts with a" {
		t.Errorf("name: pattern_message dropped: %v", props["name"])
	}

	delete(schemas["DB"].Properties, "id")
	src, err := buildOutputTypes(&program{schemas: schemas}, "app")
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{`Port\s+int\s`, `TLS\s+bool\s`, `Ports\s+\[\]int\s`} {
		if !regexp.MustCompile(field).MatchString(src) {
			t.Errorf("generated types lack %s:\n%s", strings.ReplaceAll(field, `\s`, " "), src)
		}
	}
}
