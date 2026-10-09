package codegen

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

const argvSpec = `version: 0.0.0
command:
  name: app
  options_first: true
  response_files: { prefix: "+" }
  arguments:
    - name: host
      schema: { type: string }
  flags:
    - name: format
      schema:
        type: string
        enum:
          - json
          - { value: yaml, summary: human-friendly }
    - name: ipv4
      identifiers: ['-4']
      schema: { type: bool }
  env:
    - name: mode
      schema:
        type: string
        enum: [{ value: fast, summary: quick }, slow]
  commands:
    - name: exec
      options_first: true
      arguments:
        - name: host
          schema: { type: string }
        - name: command
          passthrough: true
          schema: { type: '[]string' }
    - name: plain
      flags:
        - name: color
          schema: { type: string, enum: [always, never] }
`

func argvProgram(t *testing.T) *program {
	t.Helper()
	spec := decodeSpecYAML(t, argvSpec)
	gp, err := resolveTree(spec, filepath.Join(t.TempDir(), ".rotini.spec.yaml"), "example.com/app")
	if err != nil {
		t.Fatal(err)
	}
	gp.spec = spec
	return gp
}

// TestDefinitionLiteral_argvFacts pins the runtime data the argv facts generate:
// OptionsFirst on the root and a command, a passthrough argument, described enums (and none
// for plain ones), and response files.
func TestDefinitionLiteral_argvFacts(t *testing.T) {
	got := renderDefinition(argvProgram(t))
	for _, want := range []string{
		"OptionsFirst: true,\n",
		`Name: "command", Type: "[]string", Variadic: true, Passthrough: true`,
		`Enum: []string{"json", "yaml"}, EnumValues: []rotini.EnumValue{`,
		`{Value: "json"},`,
		`{Value: "yaml", Summary: "human-friendly"},`,
		`Identifiers: []string{"-4"}`,
		`ResponseFiles: &rotini.ResponseFilesDef{Prefix: "+"},`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("definition lacks %q:\n%s", want, got)
		}
	}
	if n := strings.Count(got, "OptionsFirst: true"); n != 2 {
		t.Errorf("OptionsFirst appears %d times, want 2 (root and exec)", n)
	}
	if strings.Contains(got, `"always", "never"}, EnumValues`) {
		t.Error("a plain enum got EnumValues")
	}
}

// TestContract_argvFacts pins the contract's argv facts: options_first, the passthrough
// marker, enum_values beside a plain schema enum, and response_files.
func TestContract_argvFacts(t *testing.T) {
	gp := argvProgram(t)
	raw, err := gp.contract(gp.contractNodes())
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		ResponseFiles struct {
			Prefix string `json:"prefix"`
		} `json:"response_files"`
		Commands []struct {
			Name         string `json:"name"`
			OptionsFirst bool   `json:"options_first"`
			Arguments    []struct {
				Name        string `json:"name"`
				Passthrough bool   `json:"passthrough"`
			} `json:"arguments"`
			Flags []struct {
				Name       string                       `json:"name"`
				EnumValues map[string]map[string]string `json:"enum_values"`
				Schema     map[string]any               `json:"schema"`
			} `json:"flags"`
			Env []struct {
				EnumValues map[string]map[string]string `json:"enum_values"`
			} `json:"env"`
		} `json:"commands"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.ResponseFiles.Prefix != "+" {
		t.Errorf("response_files = %+v", doc.ResponseFiles)
	}
	root, exec := doc.Commands[0], doc.Commands[1]
	if !root.OptionsFirst || !exec.OptionsFirst || doc.Commands[2].OptionsFirst {
		t.Errorf("options_first: root %v exec %v plain %v", root.OptionsFirst, exec.OptionsFirst, doc.Commands[2].OptionsFirst)
	}
	if exec.Arguments[0].Passthrough || !exec.Arguments[1].Passthrough {
		t.Errorf("passthrough marks %+v", exec.Arguments)
	}
	format := root.Flags[0]
	if want := map[string]map[string]string{"json": {}, "yaml": {"summary": "human-friendly"}}; !reflect.DeepEqual(format.EnumValues, want) {
		t.Errorf("enum_values = %v, want %v", format.EnumValues, want)
	}
	if got := format.Schema["enum"]; !reflect.DeepEqual(got, []any{"json", "yaml"}) {
		t.Errorf("schema enum = %v, want the plain values", got)
	}
	if root.Env[0].EnumValues["fast"]["summary"] != "quick" {
		t.Errorf("env enum_values = %v", root.Env[0].EnumValues)
	}
	if doc.Commands[2].Flags[0].EnumValues != nil {
		t.Error("a plain enum got enum_values")
	}
	schema, err := compileSchema("contract", schemaContractFileBytes)
	if err != nil {
		t.Fatal(err)
	}
	if problems := validateInstance("contract", raw, schema); len(problems) > 0 {
		t.Errorf("the contract does not match schema-contract.json: %v", problems)
	}
}

// TestSchemaToDoc_flattensEnums pins that every schema position's enum is written as plain
// values, while a property that happens to be named enum is left alone.
func TestSchemaToDoc_flattensEnums(t *testing.T) {
	described := []any{"a", map[string]any{"value": "b", "summary": "the b"}}
	s := Schema{
		Type: "object", Enum: described,
		Properties: map[string]Schema{
			"enum": {Type: "string", Enum: described},
			"list": {Type: "array", Items: &Schema{Enum: described}},
		}}
	doc := schemaToDoc(s).(map[string]any)
	plain := []any{"a", "b"}
	props := doc["properties"].(map[string]any)
	for name, got := range map[string]any{
		"root":  doc["enum"],
		"enum":  props["enum"].(map[string]any)["enum"],
		"items": props["list"].(map[string]any)["items"].(map[string]any)["enum"],
	} {
		if !reflect.DeepEqual(got, plain) {
			t.Errorf("%s enum = %v, want %v", name, got, plain)
		}
	}
	if _, ok := props["enum"]; !ok {
		t.Error("the property named enum was dropped")
	}
}

// TestEnumValues_everySpecFormat pins that a described enum decodes the same from every spec
// format, since each format decodes an object member into the generated []any its own way.
func TestEnumValues_everySpecFormat(t *testing.T) {
	want := []enumValue{{Value: "json"}, {Value: "yaml", Summary: "human-friendly"}}
	for format, body := range map[fileFormat]string{
		formatYAML:  "version: 0.0.0\ncommand:\n  name: a\n  flags:\n    - name: f\n      schema:\n        enum: [json, {value: yaml, summary: human-friendly}]\n",
		formatJSON:  `{"version":"0.0.0","command":{"name":"a","flags":[{"name":"f","schema":{"enum":["json",{"value":"yaml","summary":"human-friendly"}]}}]}}`,
		formatJSONC: "// c\n{\"version\":\"0.0.0\",\"command\":{\"name\":\"a\",\"flags\":[{\"name\":\"f\",\"schema\":{\"enum\":[\"json\",{\"value\":\"yaml\",\"summary\":\"human-friendly\"}]}}]}}",
		formatTOML:  "version = \"0.0.0\"\n[command]\nname = \"a\"\n[[command.flags]]\nname = \"f\"\n[command.flags.schema]\nenum = [\"json\", {value = \"yaml\", summary = \"human-friendly\"}]\n",
	} {
		spec, err := decodeData[Spec](format, []byte(body), "spec")
		if err != nil {
			t.Fatalf("%s: %v", format, err)
		}
		if got := enumValues(spec.Command.Flags[0].Schema.Enum); !slices.Equal(got, want) {
			t.Errorf("%s: enum = %+v, want %+v", format, got, want)
		}
	}
}

// TestLintItemConstraints_describedEnums pins that comparing a list's enum with its items'
// works on described members, which as decoded maps can't be compared directly.
func TestLintItemConstraints_describedEnums(t *testing.T) {
	spec := decodeSpecYAML(t, `version: 0.0.0
command:
  name: a
  flags:
    - name: f
      schema:
        type: array
        enum: [{value: x, summary: the x}]
        items: { type: string, enum: [{value: y, summary: the y}] }
`)
	if problems := lintItemConstraints(spec); len(problems) != 1 {
		t.Errorf("problems = %v, want the one enum conflict", problems)
	}
}
