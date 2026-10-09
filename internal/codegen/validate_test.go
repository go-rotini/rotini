package codegen

import (
	"encoding/json"
	"errors"
	"maps"
	"strings"
	"testing"
)

// validateInModule runs the real Validate workflow over a spec+conf pair in a temp
// module, returning the fatal error (nil when the documents are clean) and the
// non-fatal warnings.
func validateInModule(t *testing.T, spec, conf, failMode string) (err error, warnings []error) {
	t.Helper()
	dir := t.TempDir()
	writeTestFile(t, dir, "go.mod", "module example.com/v\n\ngo 1.26\n")
	writeTestFile(t, dir, ".rotini.spec.yaml", spec)
	writeTestFile(t, dir, ".rotini.conf.yaml", conf)
	t.Chdir(dir)
	err = NewProcessor("0.0.0").Validate(".rotini.spec.yaml", ".rotini.conf.yaml", false, failMode, "",
		func(string, error) {},
		func(w []error) { warnings = append(warnings, w...) })
	return err, warnings
}

// TestValidate_accepts pins that a schema-valid, lint-clean pair passes with no error or
// warnings.
func TestValidate_accepts(t *testing.T) {
	err, warnings := validateInModule(t, goldenSpec, goldenConf, "")
	if err != nil {
		t.Errorf("Validate(valid documents) = %v, want nil", err)
	}
	if len(warnings) != 0 {
		t.Errorf("Validate(valid documents) warnings = %v, want none", warnings)
	}
}

// TestValidate_reportsSchemaProblemsWithPosition pins that a schema problem names its
// source line:col.
func TestValidate_reportsSchemaProblemsWithPosition(t *testing.T) {
	const bad = `version: 0.0.0
command:
  name: demo
  bogus_key: nope
`
	err, _ := validateInModule(t, bad, goldenConf, "")
	if err == nil {
		t.Fatal("Validate(unknown command key) = nil, want an error")
	}
	got := err.Error()
	if !strings.Contains(got, ".rotini.spec.yaml:") {
		t.Errorf("problem %q does not name the source file:line:col", got)
	}
	if !strings.Contains(got, "spec:") {
		t.Errorf("problem %q is not tagged with its document kind", got)
	}
}

// TestValidate_failFastStopsAtFirst pins that `collect` (the default) reports every problem
// and `fast` stops at the first.
func TestValidate_failFastStopsAtFirst(t *testing.T) {
	const twoProblems = `version: 0.0.0
command:
  name: demo
  bogus_one: nope
  bogus_two: nope
`
	collectErr, _ := validateInModule(t, twoProblems, goldenConf, "collect")
	fastErr, _ := validateInModule(t, twoProblems, goldenConf, "fast")
	if collectErr == nil || fastErr == nil {
		t.Fatalf("both modes must fail: collect=%v fast=%v", collectErr, fastErr)
	}
	if collectLines, fastLines := strings.Count(collectErr.Error(), "\n"), strings.Count(fastErr.Error(), "\n"); collectLines <= fastLines {
		t.Errorf("collect reported %d extra lines, fast %d — collect must report more", collectLines, fastLines)
	}
}

// TestValidate_confProblemsAreTagged pins that conf problems are reported and tagged "conf".
func TestValidate_confProblemsAreTagged(t *testing.T) {
	const badConf = `version: 0.0.0
generate:
  packages:
    - type: not-a-target
      file: internal/cmd/demo/zz_demo.go
`
	err, _ := validateInModule(t, goldenSpec, badConf, "")
	if err == nil {
		t.Fatal("Validate(bad conf) = nil, want an error")
	}
	if got := err.Error(); !strings.Contains(got, "conf:") {
		t.Errorf("conf problem %q is not tagged as a conf finding", got)
	}
}

// TestProblem_ErrorAndUnwrap pins problem rendering with and without a position, and that a
// typed cause is reachable via errors.As.
func TestProblem_ErrorAndUnwrap(t *testing.T) {
	positioned := &problem{kind: "spec", loc: "/command/name", pos: "spec.yaml:3:9", msg: "bad"}
	if got, want := positioned.Error(), "spec: spec.yaml:3:9: /command/name: bad"; got != want {
		t.Errorf("positioned problem = %q, want %q", got, want)
	}
	bare := &problem{kind: "conf", loc: "command app deploy", msg: "bad"}
	if got, want := bare.Error(), "conf: command app deploy: bad"; got != want {
		t.Errorf("unpositioned problem = %q, want %q", got, want)
	}

	sentinel := errors.New("underlying")
	withCause := &problem{kind: "spec", loc: "/x", msg: "bad", cause: sentinel}
	if !errors.Is(withCause, sentinel) {
		t.Error("a problem's typed cause is not reachable via errors.Is")
	}
	if bare.Unwrap() != nil {
		t.Error("a problem with no cause must unwrap to nil")
	}
}

// ── version + fail-mode ─────────────────────────────────────.

// TestVersionProblem pins that a document's `version` is a minimum within one major.
func TestVersionProblem(t *testing.T) {
	cases := []struct {
		name        string
		doc, binary string
		wantProblem bool
	}{
		{"exact match", "1.2.3", "1.2.3", false},
		{"v prefix on the binary", "1.2.3", "v1.2.3", false},

		// Same major, binary at or ahead of the document.
		{"binary a patch ahead", "1.2.3", "1.2.9", false},
		{"binary a minor ahead", "1.2.3", "1.4.0", false},
		{"binary far ahead, same major", "1.0.0", "1.99.99", false},
		{"document at zero, binary ahead", "0.0.0", "0.4.1", false},

		// Binary behind the document: it may not know the keys the document uses.
		{"binary a patch behind", "1.2.3", "1.2.2", true},
		{"binary a minor behind", "1.4.0", "1.2.9", true},

		// Different majors, either direction.
		{"document major behind", "1.0.0", "2.0.0", true},
		{"document major ahead", "2.0.0", "1.9.9", true},

		// Unknown on either side is never judged.
		{"doc empty skips", "", "2.0.0", false},
		{"binary empty skips", "1.0.0", "", false},
		{"dev build skips", "1.0.0", "dev", false},
		{"unreleased 0.0.0 build skips", "1.2.3", "0.0.0", false},
		{"non-semver doc skips", "not-a-version", "1.0.0", false},

		// Pre-release and build metadata are ignored, not rejected.
		{"binary prerelease of the same version", "1.2.3", "1.2.3-rc.1", false},
		{"binary prerelease ahead", "1.2.3", "1.3.0-rc.1", false},
		{"binary build metadata", "1.2.3", "1.2.3+deadbeef", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := versionProblem("spec", tc.doc, tc.binary)
			if (got != nil) != tc.wantProblem {
				t.Errorf("versionProblem(%q,%q) problem=%v, want %v", tc.doc, tc.binary, got != nil, tc.wantProblem)
			}
			if got == nil {
				return
			}
			if got.loc != "version" {
				t.Errorf("version problem loc = %q, want \"version\"", got.loc)
			}
			// The message names both versions and the remedy.
			for _, want := range []string{tc.doc, tc.binary} {
				if !strings.Contains(got.msg, strings.TrimPrefix(want, "v")) {
					t.Errorf("message %q does not name %q", got.msg, want)
				}
			}
			if !strings.Contains(got.msg, "upgrade") && !strings.Contains(got.msg, "install") {
				t.Errorf("message %q does not say what to do: %q", tc.name, got.msg)
			}
		})
	}
}

// TestParseSemver covers the shapes versionProblem must not choke on.
func TestParseSemver(t *testing.T) {
	ok := map[string]semver{
		"1.2.3":          {1, 2, 3},
		"v1.2.3":         {1, 2, 3},
		"0.0.0":          {0, 0, 0},
		"10.20.30":       {10, 20, 30},
		"1.2.3-rc.1":     {1, 2, 3},
		"1.2.3+build.99": {1, 2, 3},
		" 1.2.3 ":        {1, 2, 3},
	}
	for in, want := range ok {
		got, valid := parseSemver(in)
		if !valid || got != want {
			t.Errorf("parseSemver(%q) = %v,%v; want %v,true", in, got, valid, want)
		}
	}
	for _, in := range []string{"", "dev", "1.2", "1.2.3.4", "1.2.x", "a.b.c", "1..3", "-1.2.3"} {
		if got, valid := parseSemver(in); valid {
			t.Errorf("parseSemver(%q) = %v,true; want not ok", in, got)
		}
	}
}

func TestFailFast(t *testing.T) {
	confFail := func(mode string) *reconciledConf {
		return &reconciledConf{conf: &Conf{Validate: &ValidateConfig{Fail: mode}}}
	}
	cases := []struct {
		name     string
		failMode string
		rc       *reconciledConf
		want     bool
	}{
		{"flag override fast wins", "fast", confFail("collect"), true},
		{"flag override collect", "collect", confFail("fast"), false},
		{"conf fast", "", confFail("fast"), true},
		{"conf collect", "", confFail("collect"), false},
		{"no override, no conf validate", "", &reconciledConf{conf: &Conf{}}, false},
		{"defaulted conf", "", &reconciledConf{conf: &Conf{}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := failFast(tc.failMode, tc.rc); got != tc.want {
				t.Errorf("failFast(%q, ...) = %v, want %v", tc.failMode, got, tc.want)
			}
		})
	}
}

// ── strictness inside schema blocks ──────────────────────────────────────────

const blockSpecHead = "version: 0.0.0\ncommand:\n  name: t\n  summary: s\n"

func blockProblems(t *testing.T, yamlBody string) []error {
	t.Helper()
	instance, err := bytesToJSON(formatYAML, []byte(blockSpecHead+yamlBody))
	if err != nil {
		t.Fatalf("to json: %v", err)
	}
	return schemaBlockProblems(instance)
}

// schemaBlockLocations maps every schema-block location in the spec schema to a snippet that
// plants a bogus key there. TestSchemaBlockProblems_reachesEveryBlockLocation derives the
// locations from the schema, so a new one fails until it is added here and walked.
var schemaBlockLocations = map[string]string{
	"FlagInput.schema":         "  flags:\n    - name: f\n      identifiers: [--f]\n      summary: s\n      schema: { type: string, bogus: 1 }\n",
	"ArgumentInput.schema":     "  arguments:\n    - name: a\n      schema: { type: string, bogus: 1 }\n",
	"EnvInput.schema":          "  env:\n    - name: e\n      schema: { type: string, bogus: 1 }\n",
	"ConfigInput.schema":       "  config:\n    - name: c\n      schema: { type: string, bogus: 1 }\n",
	"StdinSpec.schema":         "  stdin:\n    format: json\n    schema: { type: object, bogus: 1 }\n",
	"Command.output":           "  output: { type: object, bogus: 1 }\n",
	"Command.schemas":          "  schemas:\n    X: { type: object, bogus: 1 }\n",
	"ConfigurationFile.schema": "  config_files:\n    - name: main\n      path: c.yaml\n      schema: { type: object, bogus: 1 }\n",
	"BaseSchema.properties":    "  output: { type: object, properties: { p: { type: string, bogus: 1 } } }\n",
	"BaseSchema.items":         "  output: { type: array, items: { type: string, bogus: 1 } }\n",
	"ExitStatusEntry.output":   "  exit_status:\n    - code: 3\n      output: { type: object, bogus: 1 }\n",
}

func TestSchemaBlockProblems_reachesEveryBlockLocation(t *testing.T) {
	var doc struct {
		Definitions map[string]struct {
			Properties map[string]map[string]any `json:"properties"`
			AllOf      []struct {
				Properties map[string]map[string]any `json:"properties"`
			} `json:"allOf"`
		} `json:"definitions"`
	}
	if err := json.Unmarshal(schemaSpecFileBytes, &doc); err != nil {
		t.Fatal(err)
	}
	isBlock := func(v any) bool {
		m, ok := v.(map[string]any)
		ref, _ := m["$ref"].(string)
		return ok && (ref == "#/definitions/Schema" || ref == "#/definitions/InputSchema")
	}
	var derived []string
	for def, d := range doc.Definitions {
		props := map[string]map[string]any{}
		maps.Copy(props, d.Properties)
		for _, b := range d.AllOf {
			maps.Copy(props, b.Properties)
		}
		for name, p := range props {
			if isBlock(p) || isBlock(p["items"]) || isBlock(p["additionalProperties"]) {
				derived = append(derived, def+"."+name)
			}
		}
	}
	if len(derived) == 0 {
		t.Fatal("derived no schema-block locations — the schema shape changed and this test no longer sees it")
	}
	for _, loc := range derived {
		snippet, ok := schemaBlockLocations[loc]
		if !ok {
			t.Errorf("%s holds a schema block but has no case here — add one, and make sure schemaBlockProblems walks it", loc)
			continue
		}
		if problems := blockProblems(t, snippet); len(problems) != 1 {
			t.Errorf("%s: a bogus key produced %d problems, want exactly 1: %v", loc, len(problems), problems)
		}
	}
}

func TestSchemaBlockProblems_acceptsEveryDefinedKey(t *testing.T) {
	sets, err := loadSchemaBlockKeys()
	if err != nil {
		t.Fatal(err)
	}
	// Every BaseSchema key is valid in both kinds of block; input blocks add input-only keys.
	for _, k := range []string{"type", "enum", "minimum", "pattern", "items", "properties", "$ref", "import"} {
		if !sets.input[k] || !sets.object[k] {
			t.Errorf("shared key %q missing from a block kind", k)
		}
	}
	for _, k := range []string{"default", "variable", "placeholder", "secret", "from"} {
		if !sets.input[k] {
			t.Errorf("input key %q missing from input blocks", k)
		}
		if sets.object[k] {
			t.Errorf("input-only key %q allowed in an object schema", k)
		}
	}
	if !sets.object["required"] || !sets.input["required"] {
		t.Error("`required` is valid in both kinds (an array in one, a boolean in the other)")
	}

	clean := blockProblems(t, "  flags:\n    - name: f\n      identifiers: [--f]\n      summary: s\n"+
		"      schema: { type: array, items: { type: string, enum: [a] }, minItems: 1, default: [a], placeholder: X }\n"+
		"  output: { type: object, required: [id], properties: { id: { type: integer, minimum: 1 } } }\n")
	if len(clean) != 0 {
		t.Errorf("valid schema blocks reported problems: %v", clean)
	}
}

// TestSchemaBlockProblems_explainsUnimplementedJSONSchemaKeywords pins the distinct message for
// an unimplemented JSON Schema keyword versus a typo.
func TestSchemaBlockProblems_explainsUnimplementedJSONSchemaKeywords(t *testing.T) {
	problems := blockProblems(t, "  flags:\n    - name: f\n      identifiers: [--f]\n      summary: s\n"+
		"      schema: { type: array, contains: { type: string }, defualt: [x] }\n")
	if len(problems) != 2 {
		t.Fatalf("got %d problems, want 2: %v", len(problems), problems)
	}
	msgs := problems[0].Error() + "\n" + problems[1].Error()
	if !strings.Contains(msgs, `"contains" is a JSON Schema keyword`) {
		t.Errorf("contains not explained as an unimplemented keyword:\n%s", msgs)
	}
	if strings.Contains(msgs, `"defualt" is a JSON Schema keyword`) {
		t.Errorf("a plain typo was described as a JSON Schema keyword:\n%s", msgs)
	}
}

// TestValidate_rejectsUnknownKeysInSchemaBlocksWithPosition pins that schema-block problems
// are positioned through the full workflow.
func TestValidate_rejectsUnknownKeysInSchemaBlocksWithPosition(t *testing.T) {
	err, _ := validateInModule(t, blockSpecHead+"  flags:\n    - name: f\n      identifiers: [--f]\n      summary: s\n"+
		"      schema: { type: string, totallyMadeUpKey: 42 }\n", goldenConf, "")
	if err == nil {
		t.Fatal("an unknown key in a schema block validated clean")
	}
	if !strings.Contains(err.Error(), `.rotini.spec.yaml:9:`) || !strings.Contains(err.Error(), "/command/flags/0/schema/totallyMadeUpKey") {
		t.Errorf("problem not positioned: %v", err)
	}
}

// ── mistyped values ──────────────────────────────────────────────────────────

// TestValidate_typeErrorsAreReportedByTheSchemaWithPositions pins that decode failures are
// reported as positioned schema problems, all at once, without decoder text.
func TestValidate_typeErrorsAreReportedByTheSchemaWithPositions(t *testing.T) {
	err, _ := validateInModule(t, blockSpecHead+
		"  flags:\n"+
		"    - name: a\n      identifiers: [--a]\n      summary: s\n      schema: { type: string, required: [a] }\n"+
		"    - name: b\n      identifiers: [--b]\n      summary: s\n      schema: { type: int, minimum: \"five\" }\n",
		goldenConf, "")
	if err == nil {
		t.Fatal("type errors validated clean")
	}
	msg := err.Error()
	for _, want := range []string{
		"/command/flags/0/schema/required", `"required" must be of type boolean`,
		"/command/flags/1/schema/minimum", `"minimum" must look like`,
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("missing %q — both mistakes should be reported, each by the schema:\n%s", want, msg)
		}
	}
	for _, leak := range []string{"unmarshal", "Go value of type", "!!seq"} {
		if strings.Contains(msg, leak) {
			t.Errorf("the raw decoder message leaked through (%q):\n%s", leak, msg)
		}
	}
}

// TestExplainDecodeFailure_schemaAcceptsButTypesReject pins that a schema/type disagreement is
// reported as a rotini bug.
func TestExplainDecodeFailure_schemaAcceptsButTypesReject(t *testing.T) {
	p := NewProcessor("0.0.0")
	body := blockSpecHead + "  flags:\n    - name: f\n      identifiers: [--f]\n      summary: s\n" +
		"      schema: { type: string, minLength: 99999999999999999999999 }\n"
	_, err := decodeData[Spec](formatYAML, []byte(body), "s.yaml")
	if err == nil {
		t.Skip("this YAML library accepted the overflow; no schema/type disagreement to exercise")
	}
	got := p.explainDecodeFailure("spec", err)
	if !strings.Contains(got.Error(), "rotini bug") {
		t.Errorf("a schema-valid, type-invalid document was not reported as a rotini bug: %v", got)
	}
}

// TestExplainDecodeFailure_passesOtherErrorsThrough pins that non-decode errors are returned
// unchanged.
func TestExplainDecodeFailure_passesOtherErrorsThrough(t *testing.T) {
	orig := errors.New("no such file")
	if got := NewProcessor("0.0.0").explainDecodeFailure("spec", orig); got != orig {
		t.Errorf("a non-decode error was rewritten: %v", got)
	}
}
