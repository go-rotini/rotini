package codegen

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// examplesDataSpec declares examples in both forms: a plain line, one with a summary, the
// program alone, one with a placeholder, prose, and another program's command.
const examplesDataSpec = `version: 0.0.0
command:
  name: acme
  summary: a demo
  examples:
    - acme
    - Run acme with no arguments.
    - git status
  commands:
    - name: list
      summary: list tasks
      examples:
        - acme list
        - run: acme list --status open
          summary: List the open tasks
        - acme list --status <status>
      flags:
        - name: status
          summary: the status
          schema: { type: string, enum: [open, done] }
`

// TestExamples_contract pins each example's contract entry: its line, its summary, and the
// words it passes the program, an empty list for the program alone and none for a line that
// runs it with a placeholder or doesn't run it.
func TestExamples_contract(t *testing.T) {
	t.Parallel()
	_, commands := contractJSON(t, examplesDataSpec)
	got := func(name string) string {
		var b strings.Builder
		enc := json.NewEncoder(&b)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(commands[name]["examples"]); err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(b.String())
	}
	if want := `[{"argv":[],"run":"acme"},{"run":"Run acme with no arguments."},{"run":"git status"}]`; got("acme") != want {
		t.Errorf("acme examples = %s, want %s", got("acme"), want)
	}
	if want := `[{"argv":["list"],"run":"acme list"},{"argv":["list","--status","open"],"run":"acme list --status open","summary":"List the open tasks"},{"run":"acme list --status <status>"}]`; got("acme list") != want {
		t.Errorf("acme list examples = %s, want %s", got("acme list"), want)
	}
}

// TestExamples_pagesAndVariable pins that a summary shows as a `# summary` line above its
// example on the help page, that the generated Examples variable lists the runnable examples,
// and that the generated package builds.
func TestExamples_pagesAndVariable(t *testing.T) {
	dir, _ := emitModule(t, examplesDataSpec, featureConfInline)
	gen := readEmitted(t, dir, "internal/cmd/acme/zz_acme.go")
	for _, want := range []string{
		`\n  # List the open tasks\n  acme list --status open\n`, // help
		`\n# List the open tasks\nacme list \\-\\-status open\n`, // man
		`{Command: "acme", Run: "acme", Argv: []string{}},`,
		`{Command: "acme list", Run: "acme list --status open", Summary: "List the open tasks", Argv: []string{"list", "--status", "open"}},`,
	} {
		if !strings.Contains(gen, want) {
			t.Errorf("generated file lacks %q", want)
		}
	}
	if strings.Contains(gen, `Run: "acme list --status <status>"`) || strings.Contains(gen, `Run: "git status"`) {
		t.Error("Examples lists an example it can't run")
	}
	buildEmitted(t, dir)
}

// TestExamples_noVariableWithoutRunnableExamples pins that a spec with no example that runs
// the program generates no Examples variable, so existing programs' code is unchanged.
func TestExamples_noVariableWithoutRunnableExamples(t *testing.T) {
	dir, _ := emitModule(t, "version: 0.0.0\ncommand:\n  name: acme\n  summary: s\n  examples: [\"see the docs\"]\n", featureConfInline)
	if gen := readEmitted(t, dir, "internal/cmd/acme/zz_acme.go"); strings.Contains(gen, "var Examples") {
		t.Error("Examples generated with no runnable example")
	}
}

// toolExamplesSpec exercises the mapping of example words onto tool parameters.
const toolExamplesSpec = `version: 0.0.0
command:
  name: acme
  commands:
    - name: add
      examples:
        - acme add "buy milk" --tag home,work -vv --no-color --prio 0x1f --state finished --label team=core
        - acme add x --token hunter2
        - acme add x --debug
        - { run: acme add x --tag a --tag b, summary: two tags }
      flags:
        - name: tag
          schema: { type: "[]string", separator: "," }
        - name: verbose
          identifiers: [-v, --verbose]
          schema: { type: count }
        - name: color
          schema: { type: bool, negatable: true }
        - name: prio
          schema: { type: int, base: prefixed }
        - name: state
          schema: { type: string, enum: [open, { value: done, aliases: [finished] }] }
        - name: label
          schema: { type: "map[string]string" }
        - name: token
          schema: { type: string, secret: true }
        - name: debug
          hidden: true
          schema: { type: bool }
      arguments:
        - name: title
          schema: { type: string, required: true }
`

// TestExamples_toolInputExamples pins each example's tool call: typed values, an enum alias
// written as its value, a count, a negated bool, a list split on its separator or repeated,
// and a map; an example giving a literal secret or an input that isn't a parameter is left
// out.
func TestExamples_toolInputExamples(t *testing.T) {
	t.Parallel()
	spec := decodeSpecYAML(t, toolExamplesSpec)
	gp, err := resolveTree(spec, t.TempDir()+"/.rotini.spec.yaml", "example.com/app")
	if err != nil {
		t.Fatal(err)
	}
	gp.spec = spec
	a, err := gp.agentProgram()
	if err != nil {
		t.Fatal(err)
	}
	tools, _, err := buildTools(a, false)
	if err != nil {
		t.Fatal(err)
	}
	uses := gp.exampleUses()
	var got []any
	var shown []Example
	for _, tl := range tools {
		if strings.Join(tl.invoke.Path, " ") == "add" {
			got, shown = tl.inputExamples(uses["add"])
		}
	}
	wantText := "Add a task.\n\nExamples:\n" +
		"  acme add \"buy milk\" --tag home,work -vv --no-color --prio 0x1f --state finished --label team=core\n" +
		"  acme add x --tag a --tag b  # two tags"
	if text := describedExamples("Add a task.", shown); text != wantText {
		t.Errorf("described examples =\n%s\nwant\n%s", text, wantText)
	}
	raw, _ := json.Marshal(got)
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	var want any
	if err := json.Unmarshal([]byte(`[
		{"title": "buy milk", "tag": ["home", "work"], "verbose": 2, "color": false, "prio": 31, "state": "done", "label": {"team": "core"}},
		{"title": "x", "tag": ["a", "b"]}
	]`), &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded, want) {
		t.Errorf("input examples = %s", raw)
	}
}
