package codegen

import (
	"strings"
	"testing"
)

// TestStdin_rawFormatsImplyTheirSchema pins that text and lines need no schema type: with
// `schema:` left out, or carrying only `required`, they still generate their Stdin field.
func TestStdin_rawFormatsImplyTheirSchema(t *testing.T) {
	spec := `version: 0.0.0
command:
  name: demo
  commands:
    - name: upper
      stdin: {format: text, schema: {required: true}}
    - name: sort
      stdin: {format: lines}
    - name: apply
      stdin: {format: yaml, schema: {type: object, properties: {kind: {type: string}}}}
`
	gen := emitInModule(t, spec, goldenConf)["internal/cmd/demo/zz_demo.go"]
	for _, want := range []string{
		"Stdin     *string `stdin:\"text,required\"`",
		"Stdin     *[]string `stdin:\"lines\"`",
		"Stdin     *DemoApplyStdin `stdin:\"yaml\"`",
	} {
		if !strings.Contains(gen, want) {
			t.Errorf("generated file is missing %q", want)
		}
	}
}

// TestStdin_schemasCoverOnlyDecodedPayloads pins that StdinSchemas holds valid JSON Schema
// for decoded payloads only: a raw format has nothing to validate, and its Go type
// ("[]string") is not a JSON Schema type.
func TestStdin_schemasCoverOnlyDecodedPayloads(t *testing.T) {
	spec := `version: 0.0.0
command:
  name: demo
  commands:
    - name: upper
      stdin: {format: text, schema: {type: string}}
    - name: sort
      stdin: {format: lines, schema: {type: '[]string'}}
    - name: apply
      stdin: {format: json, schema: {type: object, properties: {kind: {type: string}}}}
`
	gen := emitInModule(t, spec, goldenConf)["internal/cmd/demo/zz_demo.go"]
	if strings.Contains(gen, `\"type\":\"[]string\"`) || strings.Contains(gen, `"DemoUpperStdin"`) || strings.Contains(gen, `"DemoSortStdin"`) {
		t.Errorf("StdinSchemas has an entry for a raw format:\n%s", gen)
	}
	if !strings.Contains(gen, `"DemoApplyStdin":`) {
		t.Error("StdinSchemas is missing the decoded payload's schema")
	}
}
