package codegen

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// TestDeriveUsage pins the derived usage line for every command shape: [flags] right after the
// invocation, then the <command> slot, then the arguments.
func TestDeriveUsage(t *testing.T) {
	flag := func(hidden bool) FlagInput { return FlagInput{Name: "x", Hidden: hidden} }
	arg := func(name string, schema *InputSchema) ArgumentInput {
		return ArgumentInput{Name: name, Schema: schema}
	}
	required := &InputSchema{Required: true}
	variadic := &InputSchema{Type: "[]string"}
	for _, tt := range []struct {
		name     string
		inputs   *Inputs
		children bool
		want     string
	}{
		{"leaf with flags and arguments", &Inputs{
			Flags:     []FlagInput{flag(false)},
			Arguments: []ArgumentInput{arg("src", required), arg("dst", nil), arg("rest", variadic)},
		}, false, "demo run [flags] <src> [dst] [rest...]"},
		{"leaf with flags, no arguments", &Inputs{Flags: []FlagInput{flag(false)}}, false, "demo run [flags]"},
		{"leaf without visible flags", &Inputs{
			Flags:     []FlagInput{flag(true)},
			Arguments: []ArgumentInput{arg("items", variadic)},
		}, false, "demo run [items...]"},
		{"no inputs", nil, false, "demo run"},
		{"group", &Inputs{Flags: []FlagInput{flag(false)}}, true, "demo run [flags] <command>"},
		{"group without flags", nil, true, "demo run <command>"},
		{"group with arguments", &Inputs{
			Flags:     []FlagInput{flag(false)},
			Arguments: []ArgumentInput{arg("a", required)},
		}, true, "demo run [flags] <command> <a>"},
		{"placeholder", &Inputs{Arguments: []ArgumentInput{arg("file", &InputSchema{Placeholder: "FILE", Required: true})}}, false, "demo run <FILE>"},
		{"required variadic", &Inputs{Arguments: []ArgumentInput{arg("src", &InputSchema{Type: "[]string", Required: true})}}, false, "demo run <src...>"},
		{"hidden argument", &Inputs{Arguments: []ArgumentInput{{Name: "secret", Hidden: true}}}, false, "demo run"},
	} {
		if got := deriveUsage("demo run", tt.inputs, tt.children); got != tt.want {
			t.Errorf("%s: got %q, want %q", tt.name, got, tt.want)
		}
	}
}

// TestConstraintText pins the compact help note and the man and markdown sentences for each
// kind of declared constraint.
func TestConstraintText(t *testing.T) {
	n := func(v int) *int { return &v }
	for _, tt := range []struct {
		name    string
		channel string
		schema  *InputSchema
		compact string
		rules   []string
	}{
		{"none", "flag", &InputSchema{Type: "string"}, "", nil},
		{"nil schema", "flag", nil, "", nil},
		{"both bounds", "flag", &InputSchema{Type: "int", Minimum: 1.0, Maximum: 65535.0}, "1..65535", []string{"between 1 and 65535"}},
		{"minimum", "argument", &InputSchema{Type: "int", Minimum: 1.0}, ">= 1", []string{"at least 1"}},
		{"maximum", "env", &InputSchema{Type: "int", Maximum: 10.0}, "<= 10", []string{"at most 10"}},
		{"exclusive bounds", "flag", &InputSchema{Type: "float64", ExclusiveMinimum: 0.0, ExclusiveMaximum: 1.0}, "> 0, < 1", []string{"greater than 0 and less than 1"}},
		{"exclusive with inclusive", "flag", &InputSchema{Type: "float64", ExclusiveMinimum: 0.0, Maximum: 10.0}, "> 0, <= 10", []string{"greater than 0 and at most 10"}},
		{"fraction", "flag", &InputSchema{Type: "float64", Maximum: 0.5}, "<= 0.5", []string{"at most 0.5"}},
		{"multipleOf", "config", &InputSchema{Type: "int", MultipleOf: 5.0}, "multiple of 5", []string{"a multiple of 5"}},
		{"length range", "flag", &InputSchema{Type: "string", MinLength: 3, MaxLength: n(20)}, "length 3..20", []string{"3 to 20 characters"}},
		{"min length", "flag", &InputSchema{Type: "string", MinLength: 1}, "length >= 1", []string{"at least 1 character"}},
		{"max length", "flag", &InputSchema{Type: "string", MaxLength: n(8)}, "length <= 8", []string{"at most 8 characters"}},
		{"item range", "argument", &InputSchema{Type: "[]string", MinItems: 2, MaxItems: n(5)}, "2..5 values", []string{"2 to 5 values"}},
		{"min items", "argument", &InputSchema{Type: "[]string", MinItems: 1}, ">= 1 value", []string{"at least 1 value"}},
		{"max items", "env", &InputSchema{Type: "[]string", MaxItems: n(3)}, "<= 3 values", []string{"at most 3 values"}},
		{"pattern", "flag", &InputSchema{Type: "string", Pattern: "^[a-z-]+$"}, "", []string{"matches `^[a-z-]+$`"}},
		{"pattern with a backtick", "flag", &InputSchema{Type: "string", Pattern: "a`b"}, "", []string{"matches ``a`b``"}},
		{"pattern message", "flag", &InputSchema{Type: "string", Pattern: "^x", PatternMessage: "must start with x"}, "", []string{"must start with x"}},
		{"list flag", "flag", &InputSchema{Type: "[]string"}, "repeatable", []string{"repeatable"}},
		{"list argument", "argument", &InputSchema{Type: "[]string"}, "", nil},
		{"map flag", "flag", &InputSchema{Type: "map[string]string"}, "repeatable", []string{"repeatable"}},
		{"count flag", "flag", &InputSchema{Type: "count"}, "repeatable", []string{"repeat to count"}},
		{"separator", "flag", &InputSchema{Type: "[]string", Separator: ","}, "repeatable", []string{`several values per occurrence, separated by ","`, "repeatable"}},
		{"per-value bounds on a list", "flag", &InputSchema{Type: "[]int", Minimum: 1.0, Maximum: 9.0, MaxItems: n(4)}, "<= 4 values, 1..9, repeatable", []string{"at most 4 values", "each value between 1 and 9", "repeatable"}},
		{"text bound as written", "flag", &InputSchema{Type: "string", Minimum: "1s"}, ">= 1s", []string{"at least 1s"}},
		{"duration bound", "flag", &InputSchema{Type: "duration", Maximum: 3.6e12}, "<= 1h", []string{"at most 1h"}},
	} {
		compact, rules := constraintText(tt.schema, tt.channel)
		if compact != tt.compact || !reflect.DeepEqual(rules, tt.rules) {
			t.Errorf("%s: got %q %q, want %q %q", tt.name, compact, rules, tt.compact, tt.rules)
		}
	}
}

// TestShortDuration pins that a duration bound drops the zero units Duration.String keeps.
func TestShortDuration(t *testing.T) {
	for in, want := range map[string]string{"1h0m0s": "1h", "1m0s": "1m", "1h30m0s": "1h30m", "1m30s": "1m30s", "30s": "30s", "0s": "0s", "1.5s": "1.5s"} {
		d, err := time.ParseDuration(in)
		if err != nil {
			t.Fatal(err)
		}
		if got := shortDuration(d); got != want {
			t.Errorf("shortDuration(%s) = %q, want %q", in, got, want)
		}
	}
}

// TestLabelFlags pins the help label: identifiers joined, short ones first, and a long-only
// flag indented to the long column when any flag on the page, own or inherited, has a short
// identifier.
func TestLabelFlags(t *testing.T) {
	own := []templateDocFlagRow{
		{Identifiers: shortFirst([]string{"--verbose", "-v"})},
		{Identifiers: []string{"--lang"}},
	}
	inherited := []templateDocFlagRow{{Identifiers: []string{"--color"}}}
	labelFlags(own, inherited)
	for _, tt := range []struct{ got, want string }{
		{own[0].Label, "-v, --verbose"},
		{own[1].Label, "    --lang"},
		{inherited[0].Label, "    --color"},
	} {
		if tt.got != tt.want {
			t.Errorf("label %q, want %q", tt.got, tt.want)
		}
	}

	longOnly := []templateDocFlagRow{{Identifiers: []string{"--lang"}}}
	labelFlags(longOnly)
	if longOnly[0].Label != "--lang" {
		t.Errorf("a page with no short identifier indents %q", longOnly[0].Label)
	}
}

// TestGroupOrder pins how buckets are ordered and described: first appearance without
// declared groups; with them, the ungrouped bucket first, then the declared groups in their
// order, then any others.
func TestGroupOrder(t *testing.T) {
	rows := []templateDocCommandRow{{Name: "a", Group: "Z"}, {Name: "b"}, {Name: "c", Group: "Y"}, {Name: "d", Group: "X"}}
	titles := func(groups []templateDocCommandGroup) []string {
		var out []string
		for _, g := range groups {
			out = append(out, g.Title+"="+g.Description)
		}
		return out
	}
	if got, want := titles(groupCommands(rows, nil)), []string{"Z=", "=", "Y=", "X="}; !reflect.DeepEqual(got, want) {
		t.Errorf("undeclared: %q, want %q", got, want)
	}
	declared := []HelpGroup{{Name: "X", Description: "the x group"}, {Name: "Z"}}
	if got, want := titles(groupCommands(rows, declared)), []string{"=", "X=the x group", "Z=", "Y="}; !reflect.DeepEqual(got, want) {
		t.Errorf("declared: %q, want %q", got, want)
	}
}

// TestStdinDoc pins the Stdin section's data for each format: a referenced document, an
// inline object, the raw formats with and without a schema, and the json default.
func TestStdinDoc(t *testing.T) {
	schemas := map[string]Schema{"Task": {
		BaseSchema:  BaseSchema{Type: "object", Properties: map[string]Schema{"title": {Type: "string", Description: "what"}}},
		Description: "a task",
		Required:    []string{"title"},
	}}
	ref := stdinDoc(&StdinSpec{Format: "yaml", Schema: &InputSchema{Ref: "#/schemas/Task", Required: true}}, schemas)
	want := &templateDocStdin{Format: "yaml", Type: "Task", Description: "a task", Required: true,
		Fields: []templateDocOutputField{{Name: "title", Type: "string", Description: "what", Required: true}}}
	if !reflect.DeepEqual(ref, want) {
		t.Errorf("ref: %+v, want %+v", ref, want)
	}
	inline := stdinDoc(&StdinSpec{Schema: &InputSchema{Type: "object", Properties: map[string]Schema{"n": {Type: "int"}}}}, nil)
	if inline.Format != "json" || inline.Type != "object" || len(inline.Fields) != 1 {
		t.Errorf("inline: %+v", inline)
	}
	for format, typ := range map[string]string{"text": "string", "lines": "[]string"} {
		for _, s := range []*InputSchema{nil, {Required: true}} {
			got := stdinDoc(&StdinSpec{Format: format, Schema: s}, nil)
			if got.Type != typ || got.Fields != nil || got.Required != (s != nil) {
				t.Errorf("%s: %+v", format, got)
			}
		}
	}
	if stdinDoc(nil, nil) != nil {
		t.Error("no stdin should give no section")
	}
}

// TestStdinHeadingOverride pins headings.stdin.
func TestStdinHeadingOverride(t *testing.T) {
	if got := resolveHeadings(cmdHelp{}).Stdin; got != "Stdin:" {
		t.Errorf("default %q", got)
	}
	got := resolveHeadings(cmdHelp{Headings: &HelpHeadings{Stdin: "Input"}}).Stdin
	if got != "Input" {
		t.Errorf("override %q", got)
	}
	tmpl, err := parseDocTemplate("help", templateHelp)
	if err != nil {
		t.Fatal(err)
	}
	page, err := renderDocText(tmpl, templateHelpData{
		Headings: resolveHeadings(cmdHelp{Headings: &HelpHeadings{Stdin: "Input"}}),
		Stdin:    &templateDocStdin{Format: "lines", Type: "[]string", Required: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(page, "Input\n  lines []string (required)") {
		t.Errorf("page:\n%s", page)
	}
}

// TestCustomTemplateWithoutNewFields pins that an editable template written before the label,
// constraint, group description and stdin fields existed still renders.
func TestCustomTemplateWithoutNewFields(t *testing.T) {
	tmpl, err := parseDocTemplate("help", "{{range .FlagGroups}}{{range .Flags}}{{join .Identifiers \", \"}}\t{{.Summary}}\n{{end}}{{end}}")
	if err != nil {
		t.Fatal(err)
	}
	gp, err := resolveTree(decodeSpecYAML(t, "version: 0.0.0\ncommand:\n  name: app\n  flags:\n    - name: port\n      summary: the port\n      identifiers: [--port]\n      schema: {type: int, minimum: 1}\n"), filepath.Join(t.TempDir(), ".rotini.spec.yaml"), "example.com/app")
	if err != nil {
		t.Fatal(err)
	}
	page, err := renderDocText(tmpl, flattenFeature(gp, helpFeatureDesc)[0].data)
	if err != nil {
		t.Fatal(err)
	}
	if page != "--port    the port" {
		t.Errorf("page %q", page)
	}
}
