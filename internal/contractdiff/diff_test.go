package contractdiff

import (
	"bytes"
	"encoding/json"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// errorsV14 stands in for the error-line schema of rotini 1.4, which lists candidates.
const errorsV14 = `{"properties": {"error": {"properties": {"candidates": {}}}}}`

// contract wraps commands, and any more top-level fields, into a contract document.
func contract(commands string, extra ...string) []byte {
	doc := `{"format": "rotini-contract/1", "name": "app", "errors": ` + errorsV14 + `, "commands": [` + commands + `]`
	for _, e := range extra {
		doc += ", " + e
	}
	return []byte(doc + "}")
}

// root is the root command with the given fields.
func root(fields string) string {
	if fields != "" {
		fields = ", " + fields
	}
	return `{"name": "app", "path": []` + fields + `}`
}

func lines(r Report) []string {
	out := make([]string, 0, len(r.Findings))
	for _, f := range r.Findings {
		out = append(out, string(f.Severity)+" "+f.Rule+" "+f.Where)
	}
	slices.Sort(out)
	return out
}

type rawCase struct {
	name     string
	old, new []byte
	opts     Options
	want     []string
}

func runRaw(t *testing.T, cases []rawCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, err := Diff(tc.old, tc.new, tc.opts)
			if err != nil {
				t.Fatal(err)
			}
			want := slices.Clone(tc.want)
			slices.Sort(want)
			if got := lines(r); !slices.Equal(got, want) {
				t.Errorf("findings:\n  got  %s\n  want %s", strings.Join(got, "\n       "), strings.Join(want, "\n       "))
			}
		})
	}
}

// flag is a root flag entry with the given fields beside its identifier and kind.
func flag(fields string) string {
	return root(`"flags": [{"name": "v", "identifiers": ["--v"], "kind": "scalar", "type": "string"` + fields + `}]`)
}

func TestDiff_documentRules(t *testing.T) {
	runRaw(t, []rawCase{
		{
			name: "errors changed",
			old:  contract(root("")),
			new:  []byte(strings.Replace(string(contract(root(""))), `"candidates": {}`, `"candidates": {}, "schema_version": {}`, 1)),
			want: []string{"safe ERRORS_CHANGED errors"},
		},
		{
			name: "completion envs",
			old:  contract(root(""), `"completion": {"messages_env": "A", "descriptions_env": "B"}`),
			new:  contract(root(""), `"completion": {"messages_env": "C"}`),
			want: []string{"possibly_breaking COMPLETION_ENV_CHANGED completion.messages_env", "possibly_breaking COMPLETION_ENV_REMOVED completion.descriptions_env"},
		},
		{
			name: "completion env added",
			old:  contract(root("")),
			new:  contract(root(""), `"completion": {"messages_env": "A"}`),
			want: []string{"safe COMPLETION_ENV_ADDED completion.messages_env"},
		},
	})
}

func TestDiff_inputFacts(t *testing.T) {
	runRaw(t, []rawCase{
		{
			name: "relative_to added", old: contract(flag(`, "schema": {}`)), new: contract(flag(`, "relative_to": "config", "schema": {}`)),
			want: []string{"breaking INPUT_RELATIVE_TO_CHANGED app --v"},
		},
		{
			name: "config key added", old: contract(flag(`, "schema": {}`)), new: contract(flag(`, "config_key": "v", "schema": {}`)),
			want: []string{"safe INPUT_CONFIG_KEY_ADDED app --v"},
		},
		{
			name: "config key changed", old: contract(flag(`, "config_key": "v", "schema": {}`)), new: contract(flag(`, "config_key": "w", "schema": {}`)),
			want: []string{"breaking INPUT_CONFIG_KEY_CHANGED app --v"},
		},
		{
			name: "config source changed", old: contract(flag(`, "config_source": "main", "schema": {}`)), new: contract(flag(`, "config_source": "other", "schema": {}`)),
			want: []string{"breaking INPUT_CONFIG_SOURCE_CHANGED app --v"},
		},
		{
			name: "dotted keys changed", old: contract(flag(`, "dotted_keys": true, "schema": {}`)), new: contract(flag(`, "schema": {}`)),
			want: []string{"breaking INPUT_DOTTED_KEYS_CHANGED app --v"},
		},
		{
			name: "chdir role added", old: contract(flag(`, "schema": {}`)), new: contract(flag(`, "role": "chdir", "schema": {}`)),
			want: []string{"possibly_breaking FLAG_ROLE_CHANGED app --v"},
		},
		{
			name: "format changed", old: contract(flag(`, "schema": {"type": "string"}`)), new: contract(flag(`, "schema": {"type": "string", "format": "uri"}`)),
			want: []string{"possibly_breaking INPUT_FORMAT_CHANGED app --v"},
		},
		{
			name: "format removed", old: contract(flag(`, "schema": {"type": "string", "format": "uri"}`)), new: contract(flag(`, "schema": {"type": "string"}`)),
			want: []string{"safe INPUT_FORMAT_REMOVED app --v"},
		},
		{
			name: "type now described", old: contract(flag(`, "schema": {}`)), new: contract(flag(`, "schema": {"type": "string", "pattern": "^a"}`)),
			want: []string{"safe INPUT_TYPE_NOW_DESCRIBED app --v"},
		},
		{
			name: "nullable", old: contract(root(`"flags": [{"name": "v", "identifiers": ["--v"], "kind": "object", "schema": {"type": "string"}}]`)),
			new:  contract(root(`"flags": [{"name": "v", "identifiers": ["--v"], "kind": "object", "schema": {"type": ["string", "null"]}}]`)),
			want: []string{"safe INPUT_TYPE_WIDENED app --v"},
		},
		{
			name: "object property removed from a closed object",
			old:  contract(flag(`, "schema": {"type": "object", "additionalProperties": false, "properties": {"a": {"type": "string"}, "b": {"type": "string"}}}`)),
			new:  contract(flag(`, "schema": {"type": "object", "additionalProperties": false, "properties": {"a": {"type": "string"}, "c": {"type": "string"}}}`)),
			want: []string{"breaking INPUT_PROPERTY_NO_DELETE app --v.b", "safe INPUT_PROPERTY_ADDED app --v.c"},
		},
		{
			name: "object closed",
			old:  contract(flag(`, "schema": {"type": "object", "properties": {"a": {"type": "string"}}}`)),
			new:  contract(flag(`, "schema": {"type": "object", "additionalProperties": false, "properties": {"a": {"type": "string"}}}`)),
			want: []string{"breaking INPUT_ADDITIONAL_PROPERTIES_REMOVED app --v"},
		},
		{
			name: "composition changed",
			old:  contract(flag(`, "schema": {"anyOf": [{"type": "string"}, {"type": "integer"}]}`)),
			new:  contract(flag(`, "schema": {"anyOf": [{"type": "string"}]}`)),
			want: []string{"possibly_breaking SCHEMA_COMPOSITION_CHANGED app --v"},
		},
		{
			name: "composition branch narrowed",
			old:  contract(flag(`, "schema": {"anyOf": [{"type": "string"}, {"type": "integer", "maximum": 9}]}`)),
			new:  contract(flag(`, "schema": {"anyOf": [{"type": "string"}, {"type": "integer", "maximum": 5}]}`)),
			want: []string{"breaking INPUT_BOUND_NARROWED app --v.anyOf[1]"},
		},
	})
}

func TestDiff_valuesFrom(t *testing.T) {
	fieldsFlag := func(from, enum string) string {
		if from != "" {
			from = `, "values_from": "` + from + `"`
		}
		return flag(from + `, "schema": {"type": "string"` + enum + `}`)
	}
	runRaw(t, []rawCase{
		{
			name: "added to an input without an enum", old: contract(fieldsFlag("", "")), new: contract(fieldsFlag("output", `, "enum": ["id", "name"]`)),
			want: []string{"breaking INPUT_VALUES_FROM_ADDED app --v"},
		},
		{
			name: "removed", old: contract(fieldsFlag("output", `, "enum": ["id", "name"]`)), new: contract(fieldsFlag("", "")),
			want: []string{"safe INPUT_VALUES_FROM_REMOVED app --v"},
		},
		{
			name: "path changed", old: contract(fieldsFlag("output", `, "enum": ["id"]`)), new: contract(fieldsFlag("output.tasks", `, "enum": ["id"]`)),
			want: []string{"possibly_breaking INPUT_VALUES_FROM_CHANGED app --v"},
		},
		{
			name: "derived value removed and added", old: contract(fieldsFlag("output", `, "enum": ["id", "name"]`)), new: contract(fieldsFlag("output", `, "enum": ["id", "title"]`)),
			want: []string{"breaking ENUM_VALUE_NO_DELETE app --v value name", "safe ENUM_VALUE_ADDED app --v value title"},
		},
	})
	r, _ := Diff(contract(fieldsFlag("output", `, "enum": ["id", "name"]`)), contract(fieldsFlag("output", `, "enum": ["id"]`)), Options{})
	if r.Findings[0].Note != "follows output" {
		t.Errorf("note = %q, want it to name the output path", r.Findings[0].Note)
	}
}

func TestDiff_entryRules(t *testing.T) {
	cmd := func(fields string) string { return root(fields) }
	runRaw(t, []rawCase{
		{
			name: "env variable removed and added",
			old:  contract(cmd(`"env": [{"name": "a", "variables": ["A", "B"], "kind": "scalar", "schema": {}}]`)),
			new:  contract(cmd(`"env": [{"name": "a", "variables": ["A", "C"], "kind": "scalar", "schema": {}}]`)),
			want: []string{"breaking ENV_VARIABLE_NO_DELETE app $B", "safe ENV_VARIABLE_ADDED app $C"},
		},
		{
			name: "env no longer required",
			old:  contract(cmd(`"env": [{"name": "a", "variables": ["A"], "kind": "scalar", "required": true, "schema": {}}]`)),
			new:  contract(cmd(`"env": [{"name": "a", "variables": ["A"], "kind": "scalar", "schema": {}}]`)),
			want: []string{"safe ENV_REQUIRED_REMOVED app $A"},
		},
		{
			name: "config no longer required",
			old:  contract(cmd(`"config": [{"name": "a", "key": "a", "kind": "scalar", "required": true, "schema": {}}]`)),
			new:  contract(cmd(`"config": [{"name": "a", "key": "a", "kind": "scalar", "schema": {}}]`)),
			want: []string{"safe CONFIG_REQUIRED_REMOVED app config a"},
		},
		{
			name: "argument no longer required",
			old:  contract(cmd(`"arguments": [{"name": "a", "kind": "scalar", "required": true, "schema": {}}]`)),
			new:  contract(cmd(`"arguments": [{"name": "a", "kind": "scalar", "schema": {}}]`)),
			want: []string{"safe ARGUMENT_REQUIRED_REMOVED app <a>"},
		},
		{
			name: "stdin type changed",
			old:  contract(cmd(`"stdin": {"format": "json", "type": "[]string"}`)),
			new:  contract(cmd(`"stdin": {"format": "json", "type": "map[string]string"}`)),
			want: []string{"breaking STDIN_TYPE_CHANGED app stdin"},
		},
		{
			name: "stdin unless_argument changed",
			old:  contract(cmd(`"stdin": {"format": "text"}`)),
			new:  contract(cmd(`"stdin": {"format": "text", "unless_argument": "file"}`)),
			want: []string{"possibly_breaking STDIN_UNLESS_ARGUMENT_CHANGED app stdin"},
		},
		{
			name: "output enum added and removed",
			old:  contract(cmd(`"output": {"type": "object", "properties": {"a": {"type": "string"}, "b": {"type": "string", "enum": ["x"]}}}`)),
			new:  contract(cmd(`"output": {"type": "object", "properties": {"a": {"type": "string", "enum": ["x"]}, "b": {"type": "string"}}}`)),
			want: []string{"safe OUTPUT_ENUM_ADDED app output.a", "possibly_breaking OUTPUT_ENUM_REMOVED app output.b"},
		},
	})
}

// Definitions are compared once, in each direction they are used; a renamed $ref is followed,
// and a recursive one ends.
func TestDiff_definitions(t *testing.T) {
	defs := func(node string) string { return `"definitions": {"Node": ` + node + `}` }
	node := `{"type": "object", "properties": {"name": {"type": "string"}, "children": {"type": "array", "items": {"$ref": "#/definitions/Node"}}}}`
	nodeNoName := `{"type": "object", "properties": {"children": {"type": "array", "items": {"$ref": "#/definitions/Node"}}}}`
	used := root(`"output": {"$ref": "#/definitions/Node"}, "flags": [{"name": "n", "identifiers": ["--n"], "kind": "object", "schema": {"$ref": "#/definitions/Node"}}]`)
	runRaw(t, []rawCase{
		{
			name: "recursive definition, both directions",
			old:  contract(used, defs(node)), new: contract(used, defs(nodeNoName)),
			want: []string{"breaking OUTPUT_PROPERTY_NO_DELETE definitions.Node.name"},
		},
		{
			name: "renamed reference compared structurally",
			old:  contract(root(`"output": {"$ref": "#/definitions/A"}`), `"definitions": {"A": {"type": "object", "properties": {"x": {"type": "string"}}}}`),
			new:  contract(root(`"output": {"$ref": "#/definitions/B"}`), `"definitions": {"B": {"type": "object", "properties": {"x": {"type": "integer"}}}}`),
			want: []string{"breaking OUTPUT_TYPE_CHANGED app output.x"},
		},
		{
			name: "renamed recursive reference ends",
			old:  contract(root(`"output": {"$ref": "#/definitions/A"}`), `"definitions": {"A": {"type": "object", "properties": {"next": {"$ref": "#/definitions/A"}}}}`),
			new:  contract(root(`"output": {"$ref": "#/definitions/B"}`), `"definitions": {"B": {"type": "object", "properties": {"next": {"$ref": "#/definitions/B"}, "x": {"type": "string"}}}}`),
			want: []string{"safe OUTPUT_PROPERTY_ADDED app output.x"},
		},
	})
	r, _ := Diff(contract(used, defs(node)), contract(used, defs(nodeNoName)), Options{})
	if !strings.Contains(r.Findings[0].Note, "used by app") {
		t.Errorf("note = %q, want the commands that use it", r.Findings[0].Note)
	}
}

// legacyContract is shaped like a contract rotini 1.3 wrote: no kind, no facts beside the
// schemas, no hidden items, and an error-line schema without candidates.
const legacyContract = `{
  "format": "rotini-contract/1",
  "name": "app",
  "errors": {"properties": {"error": {"properties": {"message": {}}}}},
  "commands": [
    {"name": "app", "path": [],
     "flags": [{"name": "level", "identifiers": ["--level"], "schema": {"type": "integer", "minimum": 1}},
               {"name": "wait", "identifiers": ["--wait"], "schema": {}}],
     "arguments": [{"name": "ids", "variadic": true, "schema": {"type": "array", "items": {"type": "integer"}}}],
     "env": [{"name": "token", "variables": ["APP_TOKEN"], "secret": true, "schema": {"type": "string"}}],
     "stdin": {"format": "text"},
     "exit_status": [{"code": 3, "summary": "not found"}]},
    {"name": "app list", "path": ["list"]}
  ]
}`

// The same program as written by rotini 1.4: every new fact, a hidden flag and command, and one
// real change (the minimum narrowed).
const currentContract = `{
  "format": "rotini-contract/1",
  "name": "app",
  "errors": ` + errorsV14 + `,
  "completion": {"messages_env": "", "descriptions_env": "APP_DESCRIPTIONS"},
  "commands": [
    {"name": "app", "path": [], "stability": "beta",
     "flags": [{"name": "level", "identifiers": ["--level"], "type": "int8", "kind": "scalar", "repeatable": false, "schema": {"type": "integer", "minimum": 2}},
               {"name": "wait", "identifiers": ["--wait"], "type": "duration", "kind": "scalar", "schema": {"type": "string"}},
               {"name": "debug", "identifiers": ["--debug"], "kind": "scalar", "hidden": true, "schema": {"type": "boolean"}}],
     "arguments": [{"name": "ids", "type": "[]int", "kind": "list", "variadic": true, "separator": ",", "schema": {"type": "array", "items": {"type": "integer"}}}],
     "env": [{"name": "token", "variables": ["APP_TOKEN"], "kind": "scalar", "secret": true, "variable_file": "APP_TOKEN_FILE", "schema": {"type": "string"}}],
     "stdin": {"format": "text", "type": "string", "separator": "nul"},
     "exit_status": [{"code": 3, "name": "not_found", "summary": "not found", "retryable": true}],
     "flag_groups": [{"kind": "mutually_exclusive", "flags": ["level", "wait"]}],
     "options_first": true},
    {"name": "app list", "path": ["list"], "stream": true, "hidden_aliases": ["ls"]},
    {"name": "app internal", "path": ["internal"], "hidden": true}
  ]
}`

// Facts an old contract couldn't state are unknown, not added: only the real change is found.
func TestDiff_legacyContract(t *testing.T) {
	r, err := Diff([]byte(legacyContract), []byte(currentContract), Options{})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"breaking INPUT_BOUND_NARROWED app --level", "safe ERRORS_CHANGED errors", "safe INPUT_TYPE_NOW_DESCRIBED app --wait"}
	if got := lines(r); !slices.Equal(got, want) {
		t.Errorf("findings:\n  got  %v\n  want %v", got, want)
	}
	// A current contract diffed against itself, and the legacy one against itself, find nothing.
	for _, doc := range []string{legacyContract, currentContract} {
		if r, _ := Diff([]byte(doc), []byte(doc), Options{}); len(r.Findings) > 0 {
			t.Errorf("self-diff found %v", lines(r))
		}
	}
}

func TestDiff_refusesOtherDocuments(t *testing.T) {
	for _, tc := range []struct{ name, doc, want string }{
		{"not JSON", `{`, "the old contract is not JSON"},
		{"no format", `{"name": "app"}`, "no format"},
		{"other format", `{"format": "rotini-contract/2"}`, `format "rotini-contract/2"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Diff([]byte(tc.doc), contract(root("")), Options{})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want it to say %q", err, tc.want)
			}
		})
	}
	if _, err := Diff(contract(root("")), []byte(`[]`), Options{}); err == nil || !strings.Contains(err.Error(), "new contract") {
		t.Errorf("err = %v, want it to name the new contract", err)
	}
}

func TestDiff_acceptAndFailOn(t *testing.T) {
	old := contract(root(`"flags": [{"name": "a", "identifiers": ["--a"], "kind": "scalar", "schema": {}}, {"name": "b", "identifiers": ["--b"], "kind": "scalar", "schema": {"default": 1}}]`))
	after := contract(root(`"flags": [{"name": "b", "identifiers": ["--b"], "kind": "scalar", "schema": {"default": 2}}]`))

	r, _ := Diff(old, after, Options{})
	if !r.Failed(FailOnBreaking) || !r.Failed(FailOnPossibly) || r.Failed(FailOnNever) {
		t.Errorf("an unaccepted breaking change: Failed = %v/%v/%v", r.Failed(FailOnBreaking), r.Failed(FailOnPossibly), r.Failed(FailOnNever))
	}

	r, _ = Diff(old, after, Options{Accept: []Accept{{Rule: RuleFlagNoDelete, Where: "app --a", Reason: "unused"}}})
	if r.Summary != (Summary{PossiblyBreaking: 1, Accepted: 1}) {
		t.Errorf("summary = %+v", r.Summary)
	}
	if r.Failed(FailOnBreaking) || !r.Failed(FailOnPossibly) {
		t.Error("an accepted break still fails --fail-on breaking, or a possible one passes --fail-on possibly")
	}
	i := slices.IndexFunc(r.Findings, func(f Finding) bool { return f.Rule == RuleFlagNoDelete })
	if r.Findings[i].Accepted == nil || r.Findings[i].Accepted.Reason != "unused" {
		t.Errorf("the finding isn't marked accepted: %+v", r.Findings[i])
	}

	r, _ = Diff(old, after, Options{Accept: []Accept{
		{Rule: RuleFlagNoDelete, Where: "app --a", Reason: "unused"},
		{Rule: RuleCommandNoDelete, Where: "app gone", Reason: "stale"},
	}})
	if len(r.UnmatchedAccepts) != 1 || r.UnmatchedAccepts[0].Where != "app gone" {
		t.Errorf("unmatched = %+v", r.UnmatchedAccepts)
	}
	if !r.Failed(FailOnBreaking) || r.Failed(FailOnNever) {
		t.Error("an unmatched acknowledgement must fail unless --fail-on never")
	}
}

// A removal the old contract planned is expected at or after its release, and breaking with a
// hint before or without one.
func TestDiff_plannedRemoval(t *testing.T) {
	old := contract(root(``) + `, {"name": "app old", "path": ["old"], "deprecated": "x", "removed_in": "2.0.0"}`)
	after := contract(root(``))
	for _, tc := range []struct {
		release string
		sev     Severity
		note    string
	}{
		{"", Breaking, "planned for removal in 2.0.0; pass --release"},
		{"1.9.0", Breaking, "planned for removal in 2.0.0"},
		{"2.0.0-rc.1", Expected, "planned for removal in 2.0.0"},
		{"2.1.0", Expected, "planned for removal in 2.0.0"},
	} {
		r, _ := Diff(old, after, Options{Release: tc.release})
		if f := r.Findings[0]; f.Severity != tc.sev || f.Note != tc.note {
			t.Errorf("release %q: %s (%s), want %s (%s)", tc.release, f.Severity, f.Note, tc.sev, tc.note)
		}
		if tc.sev == Expected && r.Failed(FailOnPossibly) {
			t.Errorf("release %q: an expected removal fails the run", tc.release)
		}
	}
}

// The report is the same bytes every run, however the contracts order their items.
func TestDiff_deterministic(t *testing.T) {
	var first []byte
	for range 20 {
		r, err := Diff([]byte(legacyContract), []byte(currentContract), Options{})
		if err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(r)
		var text bytes.Buffer
		if err := r.WriteText(&text); err != nil {
			t.Fatal(err)
		}
		b = append(b, text.Bytes()...)
		if first == nil {
			first = b
		} else if !bytes.Equal(first, b) {
			t.Fatal("two runs gave different reports")
		}
	}
}

func TestReport_WriteText(t *testing.T) {
	old := contract(root(`"flags": [{"name": "a", "identifiers": ["--a"], "kind": "scalar", "schema": {}}, {"name": "b", "identifiers": ["--b"], "kind": "scalar", "schema": {"default": 1}}]`))
	after := contract(root(`"flags": [{"name": "b", "identifiers": ["--b"], "kind": "scalar", "schema": {"default": 2}}, {"name": "c", "identifiers": ["--c"], "kind": "scalar", "schema": {}}]`), `"multicall": {}`)
	r, _ := Diff(old, after, Options{Accept: []Accept{{Rule: RuleFlagNoDelete, Where: "app --a", Reason: "unused"}}})
	var b bytes.Buffer
	if err := r.WriteText(&b); err != nil {
		t.Fatal(err)
	}
	want := `Possibly breaking:
  INPUT_DEFAULT_CHANGED  app --b  default 1 → 2 (the same command line behaves differently)

Safe:
  FLAG_ADDED       app --c    flag added
  MULTICALL_ADDED  multicall  the program dispatches on the name it is invoked as

Accepted:
  FLAG_NO_DELETE  app --a  flag removed (breaking; unused)

0 breaking, 1 possibly breaking, 0 expected, 2 safe, 1 accepted
`
	if b.String() != want {
		t.Errorf("text:\n%s\nwant:\n%s", b.String(), want)
	}
	var none bytes.Buffer
	if err := (Report{}).WriteText(&none); err != nil || none.String() != "no changes\n" {
		t.Errorf("an empty report wrote %q", none.String())
	}
}

// Every rule in the catalog has a fixture pair, here or in internal/codegen's spec-built
// tables.
func TestRules_everyRuleTested(t *testing.T) {
	var src []byte
	for _, path := range []string{"diff_test.go", "../codegen/diff_test.go", "../codegen/diff_agent_test.go"} {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		src = append(src, b...)
	}
	tested := map[string]bool{}
	for _, m := range regexp.MustCompile(`"(?:breaking|possibly_breaking|expected|safe) ([A-Z_]+) `).FindAllSubmatch(src, -1) {
		tested[string(m[1])] = true
	}
	for _, rule := range Rules() {
		if !tested[rule] {
			t.Errorf("rule %s has no fixture pair", rule)
		}
	}
	if len(Rules()) != len(slices.Compact(slices.Sorted(slices.Values(Rules())))) {
		t.Error("the catalog lists a rule twice")
	}
}

func TestDiff_argumentFrom(t *testing.T) {
	arg := func(from string) string {
		if from != "" {
			from = `, "from": [` + from + `]`
		}
		return root(`"arguments": [{"name": "token", "kind": "scalar", "type": "string", "required": true` + from + `, "schema": {"type": "string"}}]`)
	}
	runRaw(t, []rawCase{
		{
			name: "removed", old: contract(arg(`"file", "stdin"`)), new: contract(arg("")),
			want: []string{"breaking INPUT_FROM_NO_DELETE app <token>", "breaking INPUT_FROM_NO_DELETE app <token>"},
		},
		{
			name: "narrowed", old: contract(arg(`"file", "stdin"`)), new: contract(arg(`"file"`)),
			want: []string{"breaking INPUT_FROM_NO_DELETE app <token>"},
		},
		{
			name: "added", old: contract(arg("")), new: contract(arg(`"file"`)),
			want: []string{"possibly_breaking INPUT_FROM_ADDED app <token>"},
		},
	})
}
