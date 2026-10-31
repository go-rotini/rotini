package codegen

import (
	"strings"
	"testing"
)

// unknownKeySpec has a mistyped command key, a mistyped schema-block key, and a type typo only
// the lint rules catch.
const unknownKeySpec = blockSpecHead + `  sumary: oops
  flags:
    - name: f
      identifiers: [--f]
      summary: s
      schema: { type: strng, requird: true }
`

// TestValidate_collectLintsPastUnknownKeys pins that with fail: collect a document whose only
// schema problems are unknown keys is still linted, so the type typo behind them is reported
// too, and that each unknown key suggests the nearest key.
func TestValidate_collectLintsPastUnknownKeys(t *testing.T) {
	err, _ := validateInModule(t, unknownKeySpec, goldenConf, "collect")
	if err == nil {
		t.Fatal("validate passed a spec with unknown keys")
	}
	msg := err.Error()
	for _, want := range []string{
		`unknown key "sumary" on a command; did you mean "summary"?`,
		`unknown key "requird" in a flag schema; did you mean "required"?`,
		`did you mean "string"?`,
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("report lacks %q:\n%s", want, msg)
		}
	}
}

// TestValidate_fastStopsAtUnknownKeys pins that fail: fast still stops at the first problem.
func TestValidate_fastStopsAtUnknownKeys(t *testing.T) {
	err, _ := validateInModule(t, unknownKeySpec, goldenConf, "fast")
	if err == nil || strings.Contains(err.Error(), `"string"`) || strings.Count(err.Error(), "\n") > 0 {
		t.Errorf("fast mode = %v, want the first unknown key alone", err)
	}
}

// TestValidate_otherSchemaProblemsStopLinting pins that a schema problem other than an unknown
// key still keeps the document from being linted.
func TestValidate_otherSchemaProblemsStopLinting(t *testing.T) {
	err, _ := validateInModule(t, blockSpecHead+`  sumary: oops
  aliases: 5
  flags:
    - name: f
      schema: { type: strng }
`, goldenConf, "collect")
	if err == nil || !strings.Contains(err.Error(), `"sumary"`) || strings.Contains(err.Error(), `"string"`) {
		t.Errorf("report = %v, want the schema problems without the lint's", err)
	}
}

// TestValidate_confUnknownKeySuggests pins the suggestion for a mistyped conf key.
func TestValidate_confUnknownKeySuggests(t *testing.T) {
	err, _ := validateInModule(t, goldenSpec, goldenConf+"validate:\n  flags_frist: true\n", "")
	if err == nil || !strings.Contains(err.Error(), `unknown key "flags_frist" on a validate config; did you mean "flags_first"?`) {
		t.Errorf("report = %v, want the suggestion", err)
	}
}
