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
			want := fmt.Sprintf("unknown key %q on %s; ", key, definitionNoun(ve.KeywordLocation))
			if chans, limited := schemaKeyChannels[key]; !limited || slices.Contains(chans, channel) {
				want += fmt.Sprintf("%q belongs under schema: (schema: {%s: …})", key, key)
			} else {
				want += fmt.Sprintf("%q is a schema key of ", key)
			}
			if got := humanizeSchemaError(&ve); !strings.HasPrefix(got, want) {
				t.Errorf("%s %s:\n got %q\nwant %q…", channel, key, got, want)
			}
		}
	}
}

// TestPlacementHint_channelKeys derives where each channel-specific schema key works from the
// lint rules themselves: the key is written under schema: on every channel, and it fits where
// validation reports no problem naming it. A hint must never send an author somewhere a later
// rule rejects, nor withhold one where the key would work.
func TestPlacementHint_channelKeys(t *testing.T) {
	// Each key with a type and sibling keys it needs to be valid where it works.
	samples := map[string]string{
		"negatable":      "type: bool\nnegatable: true",
		"implicit_value": "type: string\nimplicit_value: x",
		"dotted_keys":    "type: map[string]any\ndotted_keys: true",
		"from":           "type: string\nfrom: [file]",
		"properties":     "type: map[string]string\nproperties: {host: {type: string}}",
		"separator":      "type: '[]string'\nseparator: ';'",
		"complete":       "type: string\ncomplete: {kind: file}",
		"variable":       "type: string\nvariable: DEMO_X",
		"variable_file":  "type: string\nvariable: DEMO_X\nvariable_file: DEMO_X_FILE",
		"config_source":  "type: string\nconfig_source: app",
		"key":            "type: string\nkey: x.y",
		"nesting":        "type: map[string]any\nnesting: '__'",
		"file":           "type: string\nfile: app",
		"repeatable":     "type: '[]string'\nrepeatable: false",
		"glob":           "type: '[]string'\nglob: true",
		"expand":         "type: string\nexpand: [home]",
		"relative":       "type: time.Time\nrelative: past",
	}
	entries := map[string]string{
		"flag":     "  flags:\n    - name: x\n      schema:\n",
		"argument": "  arguments:\n    - name: x\n      schema:\n",
		"env":      "  env:\n    - name: x\n      schema:\n",
		"config":   "  config:\n    - name: x\n      schema:\n",
		"stdin":    "  stdin:\n    format: json\n    schema:\n",
	}
	for _, key := range slices.Sorted(maps.Keys(samples)) {
		for _, channel := range slices.Sorted(maps.Keys(entries)) {
			indent := "        "
			if channel == "stdin" {
				indent = "      "
			}
			spec := "version: 0.0.0\ncommand:\n  name: demo\n  config_files:\n    - name: app\n      path: ./app.yaml\n" +
				entries[channel] + indent + strings.ReplaceAll(samples[key], "\n", "\n"+indent) + "\n"
			var named []string
			for _, m := range validateSpecText(t, spec) {
				if strings.Contains(m, "`"+key+"`") || strings.Contains(m, `"`+key+`"`) {
					named = append(named, m)
				}
			}
			works := len(named) == 0
			if fits := schemaKeyFits(channel, key); fits != works {
				t.Errorf("%s on %s: hint says fits=%v, but validation reports %q", key, channel, fits, named)
			}
		}
	}
	for _, tt := range []struct {
		channel, key string
		fits         bool
	}{
		{"stdin", "required", true}, {"flag", "default", true}, {"argument", "minimum", true},
		{"flag", "summary", false}, {"flag", "nonsense", false},
	} {
		if got := schemaKeyFits(tt.channel, tt.key); got != tt.fits {
			t.Errorf("%s %s: fits %v, want %v", tt.channel, tt.key, got, tt.fits)
		}
	}
}

// TestPlacementHint_otherChannels pins the hint for a schema key written on the entry of a
// channel that doesn't take it.
func TestPlacementHint_otherChannels(t *testing.T) {
	for _, tt := range []struct{ definition, key, want string }{
		{"ConfigInput", "variable_file", `"variable_file" is a schema key of flags, arguments and env inputs only`},
		{"EnvInput", "glob", `"glob" is a schema key of arguments only`},
		{"ArgumentInput", "nesting", `"nesting" is a schema key of env inputs only`},
		{"ArgumentInput", "summary", ""},
	} {
		if got := schemaPlacementHint(tt.definition, tt.key); got != tt.want {
			t.Errorf("%s %s:\n got %q\nwant %q", tt.definition, tt.key, got, tt.want)
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
