package codegen

import (
	"strings"
	"testing"

	"github.com/go-rotini/jsonschema"
)

// TestHumanizeSchemaError covers the translation of JSON Schema's vocabulary into rotini's.
//
// rotini's lint rules set the bar for a validation message: they name the command, the input
// and the rule, and say why it matters. Two schema-level failures did not come close, and they
// are the two mistakes everyone makes first — a typo'd key and a command with no name:
//
//	/command/summry: schema is false; nothing matches
//	/command: no anyOf branch matched
//
// Neither tells the person who caused it anything. Both readings below are DERIVED — the key
// from the instance pointer, the noun from the failing schema definition, the choice from the
// anyOf branches' own causes — so a schema change carries them along rather than stranding a
// hardcoded string.
func TestHumanizeSchemaError(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		err  jsonschema.ValidationError
		want string
	}{
		{
			name: "an unknown key names itself and its shape",
			err: jsonschema.ValidationError{
				Keyword: "false", InstanceLocation: "/command/summry",
				KeywordLocation: "#/definitions/Command/additionalProperties",
				Message:         "schema is false; nothing matches",
			},
			want: `unknown key "summry" on a command`,
		},
		{
			name: "a multi-word definition becomes multi-word prose",
			err: jsonschema.ValidationError{
				Keyword: "false", InstanceLocation: "/command/flags/0/nope",
				KeywordLocation: "#/definitions/FlagInput/additionalProperties",
				Message:         "schema is false; nothing matches",
			},
			want: `unknown key "nope" on a flag input`,
		},
		{
			name: "a pointer-escaped key is unescaped",
			err: jsonschema.ValidationError{
				Keyword: "false", InstanceLocation: "/command/a~1b",
				KeywordLocation: "#/definitions/Command/additionalProperties",
				Message:         "schema is false; nothing matches",
			},
			want: `unknown key "a/b" on a command`,
		},
		{
			name: "an anyOf names the choice the author actually has",
			err: jsonschema.ValidationError{
				Keyword: "anyOf", InstanceLocation: "/command",
				KeywordLocation: "#/definitions/Command/anyOf",
				Message:         "no anyOf branch matched",
				Causes: []jsonschema.ValidationError{
					{Keyword: "required", Message: `missing required property "name"`},
					{Keyword: "required", Message: `missing required property "$ref"`},
				},
			},
			want: `a command needs either "name" or "$ref"`,
		},
		{
			name: "an anyOf of something other than required keys is left alone",
			err: jsonschema.ValidationError{
				Keyword: "anyOf", InstanceLocation: "/x", KeywordLocation: "#/definitions/Thing/anyOf",
				Message: "no anyOf branch matched",
				Causes: []jsonschema.ValidationError{
					{Keyword: "type", Message: "value is not of type string"},
				},
			},
			want: "no anyOf branch matched", // saying nothing beats guessing
		},
		{
			name: "a type mismatch names the key, not just the expectation",
			err: jsonschema.ValidationError{
				Keyword: "type", InstanceLocation: "/command/commands",
				KeywordLocation: "#/definitions/Command/properties/commands/type",
				Message:         "value is not of type array",
			},
			want: `"commands" must be of type array`,
		},
		{
			name: "a type mismatch at the root keeps the stock wording",
			err: jsonschema.ValidationError{
				Keyword: "type", InstanceLocation: "", KeywordLocation: "#/type",
				Message: "value is not of type object",
			},
			want: "value is not of type object",
		},
		{
			name: "an unrecognized keyword passes through untouched",
			err: jsonschema.ValidationError{
				Keyword: "minLength", InstanceLocation: "/command/name",
				KeywordLocation: "#/definitions/Command/properties/name/minLength",
				Message:         "length 0 is less than minimum 1",
			},
			want: "length 0 is less than minimum 1",
		},
		{
			name: "a plain missing-required was always clear",
			err: jsonschema.ValidationError{
				Keyword: "required", InstanceLocation: "",
				KeywordLocation: "#/required", Message: `missing required property "version"`,
			},
			want: `missing required property "version"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := humanizeSchemaError(&tt.err); got != tt.want {
				t.Errorf("got  %q\nwant %q", got, tt.want)
			}
		})
	}
}

// TestSchemaMessagesAreNotJargon is the standing bar, applied to whatever the validator
// actually produces for the documents below rather than to a hand-built error.
func TestSchemaMessagesAreNotJargon(t *testing.T) {
	// Not parallel: validateSpecText uses t.Chdir, which cannot run alongside t.Parallel.
	// Phrases that describe the validator's internals rather than the author's mistake.
	jargon := []string{"schema is false", "anyOf", "allOf", "oneOf", "nothing matches", "branch matched"}

	docs := map[string]string{
		"an unknown key":         "version: 0.0.0\ncommand:\n  name: app\n  summry: typo\n",
		"a command with no name": "version: 0.0.0\ncommand:\n  summary: nameless\n",
		"an unknown flag key":    "version: 0.0.0\ncommand:\n  name: app\n  flags:\n    - name: f\n      nope: 1\n",
	}
	for name, doc := range docs {
		t.Run(name, func(t *testing.T) {
			msgs := validateSpecText(t, doc)
			if len(msgs) == 0 {
				t.Fatal("the document was accepted")
			}
			for _, msg := range msgs {
				for _, j := range jargon {
					if strings.Contains(msg, j) {
						t.Errorf("message speaks JSON Schema rather than rotini: %q (contains %q)", msg, j)
					}
				}
				// The bar the lint rules set: name the thing that is wrong.
				if !strings.Contains(msg, `"`) {
					t.Errorf("message names nothing in quotes: %q", msg)
				}
			}
		})
	}
}

// validateSpecText runs the real validate stage over a spec document and returns every problem
// it reported, so the bar above is applied to what the tool actually says rather than to a
// hand-built error.
func validateSpecText(t *testing.T, spec string) []string {
	t.Helper()
	mod := t.TempDir()
	writeTestFile(t, mod, "go.mod", "module example.com/demo\n\ngo 1.26\n")
	writeTestFile(t, mod, ".rotini.spec.yaml", spec)
	writeTestFile(t, mod, ".rotini.conf.yaml", lintFixtureConf)
	t.Chdir(mod)

	var msgs []string
	failure := NewProcessor("0.0.0").Validate(".rotini.spec.yaml", ".rotini.conf.yaml", false, "collect",
		func(string, error) {}, func([]error) {})
	if failure != nil {
		for _, line := range strings.Split(failure.Error(), "\n") {
			if line = strings.TrimSpace(line); line != "" {
				msgs = append(msgs, line)
			}
		}
	}
	return msgs
}
