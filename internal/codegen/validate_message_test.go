package codegen

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/go-rotini/jsonschema"
)

// TestHumanizeSchemaError pins the rewriting of JSON Schema messages into rotini's
// vocabulary, derived from the instance pointer, schema definition and anyOf causes.
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
			want: "no anyOf branch matched",
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

// TestSchemaMessagesAreNotJargon pins that real validator output for common mistakes uses no
// JSON Schema jargon and quotes what is wrong.
func TestSchemaMessagesAreNotJargon(t *testing.T) {
	// Not parallel: validateSpecText uses t.Chdir.
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
				if !strings.Contains(msg, `"`) {
					t.Errorf("message names nothing in quotes: %q", msg)
				}
			}
		})
	}
}

// validateSpecText runs the real validate stage over a spec document and returns each
// reported problem line.
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
		for line := range strings.SplitSeq(failure.Error(), "\n") {
			if line = strings.TrimSpace(line); line != "" {
				msgs = append(msgs, line)
			}
		}
	}
	return msgs
}

// TestHumanizeSchemaError_patternQuotesExamples pins that a pattern failure quotes the key's
// examples (and x-hint), never the regex.
func TestHumanizeSchemaError_patternQuotesExamples(t *testing.T) {
	for _, tc := range []struct {
		err  jsonschema.ValidationError
		want string
	}{
		{jsonschema.ValidationError{Keyword: "pattern", InstanceLocation: "/command/flags/0/schema/$ref",
			KeywordLocation: "#/definitions/BaseSchema/properties/$ref/pattern"},
			`"$ref" must look like "DB" or "#/schemas/DB"`},
		{jsonschema.ValidationError{Keyword: "pattern", InstanceLocation: "/command/flags/0/identifiers/1",
			KeywordLocation: "#/definitions/FlagInput/properties/identifiers/items/pattern"},
			`"identifiers" item 1 must look like "-o", "--output" or "--dry-run"; one or two dashes, then a letter, then letters, digits, - or _; or a single dash and one digit (-4), as in ssh -4. No dots: --db.host is how a user sets field host of an object flag --db (declare --db with $ref to a named object schema), so a declared --db.host would compete with it; to name a flag after a nested setting, write --db-host`},
		{jsonschema.ValidationError{Keyword: "anyOf", InstanceLocation: "/command/flags/0/schema/enum/1",
			KeywordLocation: "#/definitions/BaseSchema/properties/enum/items/anyOf"},
			`"enum" item 1 must be a value (json) or a value with a one-line summary ({value: yaml, summary: human-friendly}), whose only keys are value and summary`},
		{jsonschema.ValidationError{Keyword: "pattern", InstanceLocation: "/generate/packages/0/package",
			KeywordLocation: "#/definitions/PackageConfig/allOf/0/properties/package/pattern"},
			`"package" must look like "app"`},
	} {
		if got := humanizeSchemaError(&tc.err); got != tc.want {
			t.Errorf("got %q, want %q", got, tc.want)
		}
	}
}

// TestSchemaPatternsHaveMatchingExamples pins that every patterned schema key declares
// examples that match its pattern.
func TestSchemaPatternsHaveMatchingExamples(t *testing.T) {
	for name, raw := range map[string][]byte{"spec": schemaSpecFileBytes, "conf": schemaConfFileBytes} {
		var doc any
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatal(err)
		}
		var walk func(v any, path string)
		walk = func(v any, path string) {
			switch n := v.(type) {
			case map[string]any:
				if pat, ok := n["pattern"].(string); ok {
					ex, _ := n["examples"].([]any)
					if len(ex) == 0 {
						t.Errorf("%s %s: pattern %q has no examples", name, path, pat)
					}
					re := regexp.MustCompile(pat)
					for _, e := range ex {
						if s, _ := e.(string); !re.MatchString(s) {
							t.Errorf("%s %s: example %q does not match pattern %q", name, path, s, pat)
						}
					}
				}
				for k, c := range n {
					walk(c, path+"/"+k)
				}
			case []any:
				for i, c := range n {
					walk(c, fmt.Sprintf("%s/%d", path, i))
				}
			}
		}
		walk(doc, "")
	}
}

// TestHumanizeSchemaError_article pins "a"/"an" selection for definition nouns.
func TestHumanizeSchemaError_article(t *testing.T) {
	for loc, want := range map[string]string{
		"#/definitions/ArgumentInput/additionalProperties": `unknown key "x" on an argument input`,
		"#/definitions/FlagInput/additionalProperties":     `unknown key "x" on a flag input`,
		"#/definitions/EnvInput/additionalProperties":      `unknown key "x" on an env input`,
	} {
		ve := jsonschema.ValidationError{Keyword: "false", InstanceLocation: "/command/x", KeywordLocation: loc}
		if got := humanizeSchemaError(&ve); got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}
}

// TestHumanizeSchemaError_typeHint pins that a type error appends the key's x-hint, using the
// two forms of `required` as the case.
func TestHumanizeSchemaError_typeHint(t *testing.T) {
	for loc, want := range map[string]string{
		"#/definitions/InputSchema/allOf/1/properties/required/type": `"required" must be of type boolean; on an input, required is true or false; a list of property names (required: [host]) belongs on an object schema under schemas`,
		"#/definitions/Schema/allOf/1/properties/required/type":      `"required" must be of type array; on an object schema, required lists property names (required: [host]); true or false belongs on an input's own schema (flags, arguments, env, config, stdin)`,
	} {
		typ := "boolean"
		if strings.Contains(loc, "/Schema/") {
			typ = "array"
		}
		ve := jsonschema.ValidationError{Keyword: "type", InstanceLocation: "/x/required", KeywordLocation: loc, Message: "value is not of type " + typ}
		if got := humanizeSchemaError(&ve); got != want {
			t.Errorf("got  %q\nwant %q", got, want)
		}
	}
}
