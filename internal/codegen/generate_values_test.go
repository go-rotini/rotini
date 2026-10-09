package codegen

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

const valuesSpec = `version: 0.0.0
command:
  name: app
  summary: a values fixture
  flags:
    - name: color
      summary: color output
      identifiers: [--color]
      schema: { type: bool, negatable: --plain }
    - name: fancy
      summary: fancy output
      identifiers: [--fancy]
      schema: { type: bool, negatable: true }
    - name: format
      summary: output format
      identifiers: [--format]
      schema:
        type: string
        enum:
          - json
          - { value: yaml, summary: human-friendly, aliases: [yml] }
          - { value: xml, hidden: true }
          - { value: ini, deprecated: going away, deprecated_since: 1.4.0, removed_in: 2.0.0, replaced_by: json }
    - name: since
      summary: since when
      identifiers: [--since]
      schema: { type: time, layout: ['2006-01-02 15:04', '2006-01-02'], relative: past }
    - name: on
      summary: on which day
      identifiers: [--on]
      schema: { type: date, layout: '2006-01-02' }
    - name: re
      summary: a filter
      identifiers: [--re]
      schema: { type: regexp }
    - name: glob
      summary: files to include
      identifiers: [--glob]
      schema: { type: '[]glob' }
  arguments:
    - name: target
      summary: what to act on
      schema: { type: string, variable: APP_TARGET, from: [file, stdin] }
  env:
    - name: paths
      summary: search paths
      schema: { type: '[]string', separator: ':' }
    - name: mode
      summary: the mode
      schema:
        type: string
        enum: [fast, { value: slow, aliases: [s] }, { value: old, deprecated: use fast }]
`

func valuesProgram(t *testing.T) *program {
	t.Helper()
	gp, err := resolveTree(decodeSpecYAML(t, valuesSpec), filepath.Join(t.TempDir(), ".rotini.spec.yaml"), "example.com/app")
	if err != nil {
		t.Fatal(err)
	}
	return gp
}

// The generated Definition carries the custom negation, the layout list, the relative
// direction, the enum's aliases, hidden and deprecated values, and an argument's acquisition.
func TestLiterals_valueKeys(t *testing.T) {
	gp := valuesProgram(t)
	flags := flagDefsLiteral(gp.rootInputs, nil)
	for _, want := range []string{
		`Name: "color", Identifiers: []string{"--color"}, Summary: "color output", Type: "bool", Negatable: true, Negation: "--plain"`,
		`Name: "fancy", Identifiers: []string{"--fancy"}, Summary: "fancy output", Type: "bool", Negatable: true}`,
		`EnumValues: []rotini.EnumValue{`,
		`{Value: "yaml", Summary: "human-friendly", Aliases: []string{"yml"}}`,
		`{Value: "xml", Hidden: true}`,
		`{Value: "ini", Deprecated: "going away", DeprecatedSince: "1.4.0", RemovedIn: "2.0.0", ReplacedBy: "json"}`,
		`Layout: "2006-01-02 15:04", Layouts: []string{"2006-01-02 15:04", "2006-01-02"}, Relative: "past"`,
		`Type: "*regexp.Regexp"`,
		`Type: "[]rotini.Glob"`,
	} {
		if !strings.Contains(flags, want) {
			t.Errorf("flag literal is missing\n%s\n--- literal ---\n%s", want, flags)
		}
	}
	args := argDefsLiteral(gp.rootInputs, nil)
	if want := `From: []string{"file", "stdin"}`; !strings.Contains(args, want) {
		t.Errorf("argument literal is missing %s:\n%s", want, args)
	}
}

// Generated fields carry an argument's fallback tags, an env list's separator, and an env
// enum's aliases and unlisted values, which the input reader reads.
func TestFields_valueTags(t *testing.T) {
	gp := valuesProgram(t)
	f := gp.inputFieldsOf(gp.rootInputs)
	if a := f.args[0]; a.Recon != "target" || a.EnvVar != "APP_TARGET" {
		t.Errorf("argument field: recon %q env %q", a.Recon, a.EnvVar)
	}
	if p := f.env[0]; p.Recon != "paths,separator=:" {
		t.Errorf("env list recon tag %q", p.Recon)
	}
	m := f.env[1].Constraint
	for _, want := range []string{`enumalias:"{\"s\":\"slow\"}"`, `enumunlisted:"[\"old\"]"`} {
		if !strings.Contains(m, want) {
			t.Errorf("env enum tags %s are missing %s", m, want)
		}
	}
	if got := constraintTags(gp.rootInputs.Flags[3].Schema); !strings.Contains(got, `layouts:"[\"2006-01-02 15:04\",\"2006-01-02\"]" relative:"past"`) {
		t.Errorf("time tags %s", got)
	}
}

// Help shows a custom negated form beside the flag, keeps the short list of values to use, and
// notes what a time input accepts; man and markdown list each value's aliases and deprecation.
func TestPages_valueKeys(t *testing.T) {
	gp := valuesProgram(t)
	var data templateHelpData
	for _, n := range flattenFeature(gp, helpFeatureDesc) {
		if len(n.path) == 0 {
			data = n.data
		}
	}
	data.Headings = resolveHeadings(cmdHelp{})
	render := func(name, text string, man bool) string {
		t.Helper()
		tmpl, err := parseDocTemplate(name, text)
		if err != nil {
			t.Fatal(err)
		}
		var page string
		if man {
			page, err = renderManText(tmpl, data)
		} else {
			page, err = renderDocText(tmpl, data)
		}
		if err != nil {
			t.Fatal(err)
		}
		return page
	}
	help := render("help", templateHelp, false)
	for _, want := range []string{
		"--color, --plain",
		"--[no-]fancy",
		"[json|yaml]",
		"human-friendly",
		"(accepts 2006-01-02 15:04 or a time ago: 2h, 3d, yesterday)",
		"(accepts 2006-01-02)",
		"--re regexp",
		"--glob []glob",
	} {
		if !strings.Contains(help, want) {
			t.Errorf("help is missing %q\n%s", want, help)
		}
	}
	for _, unwanted := range []string{"xml", "ini"} {
		if strings.Contains(help, unwanted) {
			t.Errorf("help shows %q\n%s", unwanted, help)
		}
	}
	md := render("markdown", templateMarkdown, false)
	for _, want := range []string{
		"(one of json, yaml)",
		"- `yaml` — human-friendly (also yml)",
		"- `ini` (deprecated since 1.4.0, removed in 2.0.0: going away; use json instead)",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown is missing %q\n%s", want, md)
		}
	}
	if strings.Contains(md, "xml") {
		t.Errorf("markdown shows the hidden value\n%s", md)
	}
	if man := render("man", templateMan, true); !strings.Contains(man, `(also yml)`) {
		t.Errorf("man is missing the alias\n%s", man)
	}
}

// The contract carries an argument's fallback, what each enum value declares, regex as a
// format, and no date-time format where a relative time or another layout is accepted.
func TestContract_valueKeys(t *testing.T) {
	gp := valuesProgram(t)
	doc, err := gp.contract(gp.contractNodes())
	if err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		Commands []struct {
			Arguments []struct {
				Name string   `json:"name"`
				Env  []string `json:"env"`
			} `json:"arguments"`
			Flags []struct {
				Name       string                     `json:"name"`
				EnumValues map[string]json.RawMessage `json:"enum_values"`
				Schema     map[string]any             `json:"schema"`
			} `json:"flags"`
		} `json:"commands"`
	}
	if err := json.Unmarshal(doc, &parsed); err != nil {
		t.Fatal(err)
	}
	root := parsed.Commands[0]
	if a := root.Arguments[0]; len(a.Env) != 1 || a.Env[0] != "APP_TARGET" {
		t.Errorf("argument env %v", a.Env)
	}
	byName := map[string]map[string]any{}
	for _, f := range root.Flags {
		byName[f.Name] = f.Schema
		if f.Name == "format" {
			if got := compactJSON(t, f.EnumValues["yaml"]); got != `{"summary":"human-friendly","aliases":["yml"]}` {
				t.Errorf("yaml enum value %s", got)
			}
			if got := compactJSON(t, f.EnumValues["xml"]); got != `{"hidden":true}` {
				t.Errorf("xml enum value %s", got)
			}
		}
	}
	if enum := byName["format"]["enum"].([]any); len(enum) != 3 {
		t.Errorf("format enum %v, want the hidden value left out", enum)
	}
	if f := byName["re"]["format"]; f != "regex" {
		t.Errorf("regexp format %v", f)
	}
	if _, has := byName["since"]["format"]; has {
		t.Errorf("a relative time keeps format %v", byName["since"]["format"])
	}
	if f := byName["on"]["format"]; f != "date" {
		t.Errorf("a date in its own layout lost format: %v", f)
	}
}

func compactJSON(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var b bytes.Buffer
	if err := json.Compact(&b, raw); err != nil {
		t.Fatal(err)
	}
	return b.String()
}
