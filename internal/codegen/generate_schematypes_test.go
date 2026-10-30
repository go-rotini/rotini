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
	if err := json.Unmarshal([]byte(validationSchema(Schema{Ref: "#/schemas/DB"}, schemas)), &doc); err != nil {
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

// TestNamedSchemaSizedNumbers pins that a named schema's sized number types generate fields of
// that Go type, inside lists, maps and nested objects too, while the runtime
// validation schema still reads them as JSON Schema integers and numbers.
func TestNamedSchemaSizedNumbers(t *testing.T) {
	var schemas map[string]Schema
	if err := json.Unmarshal([]byte(`{
		"Stats": {"type": "object", "properties": {
			"total": {"type": "uint64"},
			"small": {"type": "int8"},
			"mid": {"type": "int16"},
			"wide": {"type": "int32"},
			"big": {"type": "int64"},
			"u": {"type": "uint"},
			"u8": {"type": "uint8"},
			"u16": {"type": "uint16"},
			"u32": {"type": "uint32"},
			"ratio": {"type": "float32"},
			"plain": {"type": "int"},
			"sizes": {"type": "[]uint32"},
			"counts": {"type": "map[string]int64"},
			"items": {"type": "array", "items": {"type": "uint16"}},
			"inner": {"type": "object", "properties": {"n": {"type": "uint8"}}}}},
		"ID": {"type": "uint64"}}`), &schemas); err != nil {
		t.Fatal(err)
	}
	src, err := buildOutputTypes(&program{schemas: schemas}, "app")
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{
		`Total\s+uint64\s`, `Small\s+int8\s`, `Mid\s+int16\s`, `Wide\s+int32\s`, `Big\s+int64\s`,
		`U\s+uint\s`, `U8\s+uint8\s`, `U16\s+uint16\s`, `U32\s+uint32\s`, `Ratio\s+float32\s`,
		`Plain\s+int\s`, `Sizes\s+\[\]uint32\s`, `Counts\s+map\[string\]int64\s`,
		`Items\s+\[\]uint16\s`, `N\s+uint8\s`, `type ID uint64`,
	} {
		if !regexp.MustCompile(field).MatchString(src) {
			t.Errorf("generated types lack %s:\n%s", strings.ReplaceAll(field, `\s`, " "), src)
		}
	}

	var doc struct {
		Definitions map[string]struct {
			Properties map[string]map[string]any `json:"properties"`
		} `json:"definitions"`
	}
	if err := json.Unmarshal([]byte(validationSchema(Schema{Ref: "#/schemas/Stats"}, schemas)), &doc); err != nil {
		t.Fatal(err)
	}
	props := doc.Definitions["Stats"].Properties
	if props["total"]["type"] != "integer" || props["ratio"]["type"] != "number" {
		t.Errorf("validation schema types = %v, %v; want integer, number", props["total"]["type"], props["ratio"]["type"])
	}
	if _, leaked := props["total"][goTypeHint]; leaked {
		t.Errorf("the Go type hint leaked into the validation schema: %v", props["total"])
	}
}
