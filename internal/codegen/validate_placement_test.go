package codegen

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/go-rotini/jsonschema"
)

// TestPlacementHint_schemaKeys pins the message for every schema key written on every input
// entry: a key that works on that channel says it belongs under schema:, and one that doesn't
// gets no hint, since a later rule would reject it there too.
func TestPlacementHint_schemaKeys(t *testing.T) {
	sets, err := loadSchemaBlockKeys()
	if err != nil {
		t.Fatal(err)
	}
	definitions := map[string]string{
		"flag": "FlagInput", "argument": "ArgumentInput", "env": "EnvInput",
		"config": "ConfigInput", "stdin": "StdinSpec",
	}
	for _, channel := range slices.Sorted(maps.Keys(definitions)) {
		def := definitions[channel]
		for _, key := range slices.Sorted(maps.Keys(sets.input)) {
			ve := jsonschema.ValidationError{
				Keyword: "false", InstanceLocation: "/command/x/" + key,
				KeywordLocation: "#/definitions/" + def + "/additionalProperties",
			}
			want := fmt.Sprintf("unknown key %q on %s", key, definitionNoun(ve.KeywordLocation))
			if chans, limited := schemaKeyChannels[key]; !limited || slices.Contains(chans, channel) {
				want += fmt.Sprintf("; %q belongs under schema: (schema: {%s: …})", key, key)
			}
			if got := humanizeSchemaError(&ve); got != want {
				t.Errorf("%s %s:\n got %q\nwant %q", channel, key, got, want)
			}
		}
	}
}

// TestPlacementHint_channelKeys pins the keys that work on some channels only, matching the
// lint rules that reject them elsewhere.
func TestPlacementHint_channelKeys(t *testing.T) {
	for _, tt := range []struct {
		channel, key string
		fits         bool
	}{
		{"flag", "key", true}, {"config", "key", true}, {"env", "key", false}, {"argument", "key", false},
		{"flag", "variable", true}, {"env", "variable", true}, {"argument", "variable", false}, {"config", "variable", false},
		{"config", "file", true}, {"flag", "file", false},
		{"flag", "from", true}, {"env", "from", false},
		{"argument", "separator", true}, {"env", "separator", false},
		{"env", "nesting", true}, {"flag", "nesting", false},
		{"stdin", "required", true}, {"stdin", "properties", true}, {"stdin", "negatable", false},
		{"flag", "default", true}, {"argument", "minimum", true},
		{"flag", "summary", false}, {"flag", "nonsense", false},
	} {
		if got := schemaKeyFits(tt.channel, tt.key); got != tt.fits {
			t.Errorf("%s %s: fits %v, want %v", tt.channel, tt.key, got, tt.fits)
		}
	}
}

// TestPlacementHint_entryKeys pins the reverse case: an entry key written inside schema: says
// it belongs on the entry itself, ahead of the JSON Schema keyword note.
func TestPlacementHint_entryKeys(t *testing.T) {
	for _, tt := range []struct{ key, noun, channel, want string }{
		{"summary", "a flag schema", "flag", `unknown key "summary" in a flag schema; "summary" belongs on the flag itself, beside schema:`},
		{"hidden", "an argument schema", "argument", `unknown key "hidden" in an argument schema; "hidden" belongs on the argument itself, beside schema:`},
		{"deprecated", "an env schema", "env", `unknown key "deprecated" in an env schema; "deprecated" belongs on the env input itself, beside schema:`},
		{"identifiers", "a flag schema", "flag", `unknown key "identifiers" in a flag schema; "identifiers" belongs on the flag itself, beside schema:`},
		{"format", "the stdin schema", "stdin", `unknown key "format" in the stdin schema; "format" belongs on stdin itself, beside schema:`},
		{"identifiers", "a config schema", "config", `unknown key "identifiers" in a config schema`},
		{"deprecated", "a named schema", "", `unknown key "deprecated" in a named schema; "deprecated" is a JSON Schema keyword rotini's schema blocks do not implement, so it would have done nothing`},
	} {
		if got := unknownSchemaKey(tt.key, tt.noun, tt.channel); got != tt.want {
			t.Errorf("%s in %s:\n got %q\nwant %q", tt.key, tt.noun, got, tt.want)
		}
	}
}

// TestPlacementHint_validate pins the hints end to end through validation.
func TestPlacementHint_validate(t *testing.T) {
	got := validateSpecText(t, `version: 0.0.0
command:
  name: app
  flags:
    - name: port
      key: port
      schema:
        type: int
        summary: the port
`)
	for _, want := range []string{
		`unknown key "key" on a flag input; "key" belongs under schema: (schema: {key: …})`,
		`unknown key "summary" in a flag schema; "summary" belongs on the flag itself, beside schema:`,
	} {
		if !slices.ContainsFunc(got, func(m string) bool { return strings.HasSuffix(m, ": "+want) }) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
}
