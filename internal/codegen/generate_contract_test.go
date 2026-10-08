package codegen

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"
)

// contractSchemas generates the contract for spec and returns each input's schema, keyed by
// "<command> <input>", plus the raw stdin entry of each command.
func contractSchemas(t *testing.T, spec string) (inputs map[string]map[string]any, stdin map[string]map[string]any) {
	t.Helper()
	gp, err := resolveTree(decodeSpecYAML(t, spec), filepath.Join(t.TempDir(), ".rotini.spec.yaml"), "example.com/app")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := gp.contract(gp.contractNodes())
	if err != nil {
		t.Fatal(err)
	}
	type input struct {
		Name   string         `json:"name"`
		Schema map[string]any `json:"schema"`
	}
	var parsed struct {
		Commands []struct {
			Name      string         `json:"name"`
			Arguments []input        `json:"arguments"`
			Flags     []input        `json:"flags"`
			Env       []input        `json:"env"`
			Stdin     map[string]any `json:"stdin"`
		} `json:"commands"`
	}
	if err := json.Unmarshal(doc, &parsed); err != nil {
		t.Fatal(err)
	}
	inputs, stdin = map[string]map[string]any{}, map[string]map[string]any{}
	for _, c := range parsed.Commands {
		for _, list := range [][]input{c.Arguments, c.Flags, c.Env} {
			for _, in := range list {
				inputs[c.Name+" "+in.Name] = in.Schema
			}
		}
		if c.Stdin != nil {
			stdin[c.Name] = c.Stdin
		}
	}
	return inputs, stdin
}

// TestContract_valueTypes pins that every value type keeps its JSON type in the contract,
// with a format where JSON Schema has one, rather than an empty schema.
func TestContract_valueTypes(t *testing.T) {
	inputs, _ := contractSchemas(t, `version: 0.0.0
command:
  name: app
  flags:
    - {name: wait, identifiers: [--wait], schema: {type: duration}}
    - {name: day, identifiers: [--day], schema: {type: date}}
    - {name: at, identifiers: [--at], schema: {type: datetime}}
    - {name: addr, identifiers: [--addr], schema: {type: ip}}
    - {name: site, identifiers: [--site], schema: {type: url}}
    - {name: small, identifiers: [--small], schema: {type: int8}}
    - {name: size, identifiers: [--size], schema: {type: bytesize}}
    - {name: waits, identifiers: [--waits], schema: {type: "[]duration"}}
    - {name: days, identifiers: [--days], schema: {type: "map[string]date"}}
  env:
    - {name: timeout, schema: {type: duration}}
`)
	for name, want := range map[string]map[string]any{
		"app wait":    {"type": "string"},
		"app day":     {"type": "string", "format": "date"},
		"app at":      {"type": "string", "format": "date-time"},
		"app addr":    {"type": "string"},
		"app site":    {"type": "string", "format": "uri"},
		"app small":   {"type": "integer"},
		"app size":    {"type": "string"},
		"app waits":   {"type": "array", "items": map[string]any{"type": "string"}},
		"app days":    {"type": "object", "additionalProperties": map[string]any{"type": "string", "format": "date"}},
		"app timeout": {"type": "string"},
	} {
		if got := inputs[name]; !reflect.DeepEqual(got, want) {
			t.Errorf("%s: %v, want %v", name, got, want)
		}
	}
}

// TestContract_listConstraintsOnItems pins that a list's or map's per-value constraints sit
// on its items, where the runtime applies them, and only the item counts stay on the list.
func TestContract_listConstraintsOnItems(t *testing.T) {
	inputs, _ := contractSchemas(t, `version: 0.0.0
command:
  name: app
  arguments:
    - name: ports
      schema: {type: "[]int", minimum: 1, maximum: 65535, minItems: 1}
  flags:
    - name: tag
      identifiers: [--tag]
      schema: {type: "[]string", pattern: "^[a-z]+$", maxItems: 3}
    - name: mode
      identifiers: [--mode]
      schema: {type: "[]string", enum: [fast, slow]}
    - name: label
      identifiers: [--label]
      schema: {type: "map[string]string", maxLength: 8}
`)
	for name, want := range map[string]map[string]any{
		"app ports": {"type": "array", "minItems": float64(1),
			"items": map[string]any{"type": "integer", "minimum": float64(1), "maximum": float64(65535)}},
		"app tag": {"type": "array", "maxItems": float64(3),
			"items": map[string]any{"type": "string", "pattern": "^[a-z]+$"}},
		"app mode":  {"type": "array", "items": map[string]any{"type": "string", "enum": []any{"fast", "slow"}}},
		"app label": {"type": "object", "additionalProperties": map[string]any{"type": "string", "maxLength": float64(8)}},
	} {
		if got := inputs[name]; !reflect.DeepEqual(got, want) {
			t.Errorf("%s:\n got  %v\n want %v", name, got, want)
		}
	}
}

// TestContract_stdinRequired pins that a required stdin payload is marked required.
func TestContract_stdinRequired(t *testing.T) {
	_, stdin := contractSchemas(t, `version: 0.0.0
command:
  name: app
  commands:
    - name: upper
      stdin: {format: text, schema: {type: string, required: true}}
    - name: lower
      stdin: {format: text, schema: {type: string}}
`)
	if got := stdin["app upper"]["required"]; got != true {
		t.Errorf("required stdin: required = %v, want true (%v)", got, stdin["app upper"])
	}
	if _, has := stdin["app lower"]["required"]; has {
		t.Errorf("optional stdin is marked required: %v", stdin["app lower"])
	}
}

// TestContract_importedTypeIsOpen pins that an imported Go type, which JSON Schema can't
// describe, still constrains nothing.
func TestContract_importedTypeIsOpen(t *testing.T) {
	inputs, _ := contractSchemas(t, `version: 0.0.0
command:
  name: app
  flags:
    - name: level
      identifiers: [--level]
      schema: {type: slog.Level, import: log/slog}
`)
	if got := inputs["app level"]; len(got) != 0 {
		t.Errorf("imported type: %v, want {}", got)
	}
}
