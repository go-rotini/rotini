package rtk

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/go-rotini/rotini"
)

// testDef mirrors a root "app" with a sub-command "run", matching the runInputs
// scopes used by the binder tests.
func testDef() rotini.Definition {
	return rotini.Definition{
		Name:    "app",
		Handler: "App",
		Flags:   []rotini.FlagDef{{Name: "verbose", Identifiers: []string{"-v", "--verbose"}, Type: "bool"}},
		Commands: []rotini.CommandDef{
			{
				Name:    "run",
				Handler: "AppRun",
				Aliases: []string{"r"},
				Flags:   []rotini.FlagDef{{Name: "count", Identifiers: []string{"-c", "--count"}, Type: "int"}},
				Arguments: []rotini.ArgDef{
					{Name: "name", Type: "string"},
					{Name: "rest", Type: "[]string", Variadic: true},
				},
			},
		},
	}
}

func TestParse_bindsInputs(t *testing.T) {
	rtx := rotini.NewContextFor(testDef(), []string{"--verbose", "run", "alice", "x", "y", "--count", "3"})
	var in runInputs
	if err := NewParser().Parse(rtx, &in); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !in.App.Flags.Verbose || in.Run.Flags.Count != 3 || in.Run.Arguments.Name != "alice" {
		t.Errorf("bound inputs unexpected: %+v", in)
	}
	if r := in.Run.Arguments.Rest; len(r) != 2 || r[0] != "x" || r[1] != "y" {
		t.Errorf("variadic Rest = %v, want [x y]", r)
	}
}

// TestParse_viaRegistryGet exercises the full handler flow: the parser is bound to
// the registry, retrieved via rtx.Get (ctx.Value style), then used to parse.
func TestParse_viaRegistryGet(t *testing.T) {
	rtx := rotini.NewContextFor(testDef(), []string{"run", "alice"})
	rtx.Bind("parser", NewParser())

	parser, ok := rtx.Value("parser").(*Parser)
	if !ok {
		t.Fatal("parser not retrievable from registry")
	}
	var in runInputs
	if err := parser.Parse(rtx, &in); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if in.Run.Arguments.Name != "alice" {
		t.Errorf("Name = %q, want alice", in.Run.Arguments.Name)
	}
}

func TestParse_nilParser(t *testing.T) {
	var p *Parser
	var in runInputs
	if err := p.Parse(rotini.NewContextFor(testDef(), []string{"run"}), &in); err == nil || !strings.Contains(err.Error(), "nil parser") {
		t.Errorf("Parse on nil parser = %v, want nil-parser error", err)
	}
}

func TestParse_outMustBePointer(t *testing.T) {
	rtx := rotini.NewContextFor(testDef(), []string{"run"})
	var in runInputs
	if err := NewParser().Parse(rtx, in); err == nil || !strings.Contains(err.Error(), "pointer") {
		t.Errorf("Parse with non-pointer out = %v, want pointer error", err)
	}
}

func TestParse_unresolvedContext(t *testing.T) {
	var in runInputs
	if err := NewParser().Parse(rotini.NewContext(), &in); err == nil {
		t.Error("Parse on an unresolved context should error")
	}
	if in.Run.Flags.Count != 0 || in.App.Flags.Verbose {
		t.Errorf("out should stay zero on error, got %+v", in)
	}
}

func TestParse_unknownFlag(t *testing.T) {
	rtx := rotini.NewContextFor(testDef(), []string{"run", "--nope"})
	var in runInputs
	if err := NewParser().Parse(rtx, &in); err == nil || !strings.Contains(err.Error(), `unknown flag "--nope"`) {
		t.Errorf("Parse error = %v, want unknown-flag", err)
	}
}

func TestParse_flagNeedsValue(t *testing.T) {
	rtx := rotini.NewContextFor(testDef(), []string{"run", "--count"})
	var in runInputs
	if err := NewParser().Parse(rtx, &in); err == nil || !strings.Contains(err.Error(), "needs a value") {
		t.Errorf("Parse error = %v, want needs-a-value", err)
	}
}

func TestParse_unknownCommand(t *testing.T) {
	// "ru" is a stray positional on a branch-only root: a mistyped sub-command.
	rtx := rotini.NewContextFor(testDef(), []string{"ru"})
	var in runInputs
	err := NewParser().Parse(rtx, &in)
	if err == nil || !strings.Contains(err.Error(), `unknown command "ru"`) {
		t.Fatalf("Parse error = %v, want unknown-command", err)
	}
	if !strings.Contains(err.Error(), `Did you mean "run"?`) {
		t.Errorf("Parse error missing suggestion: %v", err)
	}
}

func TestParse_missingRequired(t *testing.T) {
	def := rotini.Definition{
		Name: "app", Handler: "App",
		Flags: []rotini.FlagDef{{Name: "token", Identifiers: []string{"--token"}, Type: "string", Required: true}},
	}
	rtx := rotini.NewContextFor(def, []string{})
	var in struct{}
	err := NewParser().Parse(rtx, &in)
	if err == nil || !strings.Contains(err.Error(), "missing required") || !strings.Contains(err.Error(), "--token") {
		t.Errorf("Parse error = %v, want missing-required --token", err)
	}
}

func TestParse_badEnumValue(t *testing.T) {
	def := rotini.Definition{
		Name: "app", Handler: "App",
		Flags: []rotini.FlagDef{{Name: "level", Identifiers: []string{"--level"}, Type: "string", Enum: []string{"low", "high"}}},
	}
	rtx := rotini.NewContextFor(def, []string{"--level", "medium"})
	var in struct{}
	err := NewParser().Parse(rtx, &in)
	if err == nil || !strings.Contains(err.Error(), "invalid value") || !strings.Contains(err.Error(), "one of: low, high") {
		t.Errorf("Parse error = %v, want bad-enum", err)
	}
}

func TestParse_defaultsApplied(t *testing.T) {
	def := rotini.Definition{
		Name: "app", Handler: "App",
		Flags: []rotini.FlagDef{{Name: "count", Identifiers: []string{"--count"}, Type: "int", Default: "9"}},
	}
	type inputs struct {
		App struct {
			Flags struct {
				Count int `rotini:"count"`
			}
			Arguments struct{}
		}
	}
	rtx := rotini.NewContextFor(def, []string{})
	var in inputs
	if err := NewParser().Parse(rtx, &in); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if in.App.Flags.Count != 9 {
		t.Errorf("default not applied: Count = %d, want 9", in.App.Flags.Count)
	}
}

// assertConstraint checks that err satisfies wantErr: when wantErr is "" the value
// must pass; otherwise the error must contain wantErr.
func assertConstraint(t *testing.T, val string, err error, wantErr string) {
	t.Helper()
	switch {
	case wantErr == "" && err != nil:
		t.Errorf("value %q: unexpected error %v", val, err)
	case wantErr != "" && (err == nil || !strings.Contains(err.Error(), wantErr)):
		t.Errorf("value %q: error = %v, want containing %q", val, err, wantErr)
	}
}

func TestParse_numericConstraints(t *testing.T) {
	def := rotini.Definition{
		Name: "app", Handler: "App",
		Flags: []rotini.FlagDef{{
			Name: "port", Identifiers: []string{"--port"}, Type: "int",
			Constraints: rotini.Constraints{Minimum: 1, Maximum: 65535},
		}},
	}
	for _, c := range []struct{ val, wantErr string }{
		{"8080", ""},
		{"0", "must be >= 1"},
		{"70000", "must be <= 65535"},
	} {
		var in struct{}
		err := NewParser().Parse(rotini.NewContextFor(def, []string{"--port", c.val}), &in)
		assertConstraint(t, c.val, err, c.wantErr)
	}
}

func TestParse_stringLengthConstraints(t *testing.T) {
	def := rotini.Definition{
		Name: "app", Handler: "App",
		Flags: []rotini.FlagDef{{
			Name: "name", Identifiers: []string{"--name"}, Type: "string",
			Constraints: rotini.Constraints{MinLength: 2, MaxLength: 5},
		}},
	}
	for _, c := range []struct{ val, wantErr string }{
		{"abc", ""},
		{"a", "at least 2"},
		{"toolong", "at most 5"},
	} {
		var in struct{}
		err := NewParser().Parse(rotini.NewContextFor(def, []string{"--name", c.val}), &in)
		assertConstraint(t, c.val, err, c.wantErr)
	}
}

func TestParse_patternConstraint(t *testing.T) {
	def := rotini.Definition{
		Name: "app", Handler: "App",
		Flags: []rotini.FlagDef{{
			Name: "id", Identifiers: []string{"--id"}, Type: "string",
			Constraints: rotini.Constraints{Pattern: "^[a-z]+$"},
		}},
	}
	for _, c := range []struct{ val, wantErr string }{
		{"abc", ""},
		{"ABC", "must match"},
	} {
		var in struct{}
		err := NewParser().Parse(rotini.NewContextFor(def, []string{"--id", c.val}), &in)
		assertConstraint(t, c.val, err, c.wantErr)
	}
}

func TestParse_itemCountConstraints(t *testing.T) {
	def := rotini.Definition{
		Name: "app", Handler: "App",
		Flags: []rotini.FlagDef{{
			Name: "tag", Identifiers: []string{"--tag"}, Type: "[]string",
			Constraints: rotini.Constraints{MinItems: 1, MaxItems: 2},
		}},
	}
	for _, c := range []struct {
		argv    []string
		wantErr string
	}{
		{[]string{"--tag", "a"}, ""},
		{[]string{"--tag", "a", "--tag", "b"}, ""},
		{nil, "at least 1"},
		{[]string{"--tag", "a", "--tag", "b", "--tag", "c"}, "at most 2"},
	} {
		var in struct{}
		err := NewParser().Parse(rotini.NewContextFor(def, c.argv), &in)
		assertConstraint(t, strings.Join(c.argv, " "), err, c.wantErr)
	}
}

// A MinItems bound applies to a variadic argument even when it receives no values
// (a distinct code path from a repeatable flag).
func TestParse_variadicArgItemCount(t *testing.T) {
	def := rotini.Definition{
		Name: "app", Handler: "App",
		Arguments: []rotini.ArgDef{{
			Name: "files", Type: "[]string", Variadic: true,
			Constraints: rotini.Constraints{MinItems: 2},
		}},
	}
	var in struct{}
	if err := NewParser().Parse(rotini.NewContextFor(def, []string{"only-one"}), &in); err == nil || !strings.Contains(err.Error(), "at least 2") {
		t.Errorf("error = %v, want at-least-2 for variadic <files>", err)
	}
	if err := NewParser().Parse(rotini.NewContextFor(def, []string{"a", "b"}), &in); err != nil {
		t.Errorf("unexpected error for two files: %v", err)
	}
}

func TestParse_negativeNumberArguments(t *testing.T) {
	def := rotini.Definition{
		Name: "app", Handler: "App",
		Arguments: []rotini.ArgDef{{Name: "delta", Type: "float64"}},
	}
	type inputs struct {
		App struct {
			Flags     struct{}
			Arguments struct {
				Delta float64 `rotini:"delta"`
			}
		}
	}
	for _, c := range []struct {
		argv []string
		want float64
	}{
		{[]string{"-5"}, -5},
		{[]string{"-0.5"}, -0.5},
		{[]string{"-1.5e2"}, -150},
	} {
		var in inputs
		if err := NewParser().Parse(rotini.NewContextFor(def, c.argv), &in); err != nil {
			t.Fatalf("Parse(%v): %v", c.argv, err)
		}
		if in.App.Arguments.Delta != c.want {
			t.Errorf("Delta for %v = %v, want %v", c.argv, in.App.Arguments.Delta, c.want)
		}
	}
}

func TestParse_negativeNumberFlagValue(t *testing.T) {
	def := rotini.Definition{
		Name: "app", Handler: "App",
		Flags: []rotini.FlagDef{{Name: "offset", Identifiers: []string{"--offset"}, Type: "int"}},
	}
	type inputs struct {
		App struct {
			Flags struct {
				Offset int `rotini:"offset"`
			}
			Arguments struct{}
		}
	}
	var in inputs
	if err := NewParser().Parse(rotini.NewContextFor(def, []string{"--offset", "-5"}), &in); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if in.App.Flags.Offset != -5 {
		t.Errorf("Offset = %d, want -5", in.App.Flags.Offset)
	}
}

func TestParse_secretValueRedactedInErrors(t *testing.T) {
	def := rotini.Definition{
		Name: "app", Handler: "App",
		Flags: []rotini.FlagDef{
			{Name: "token", Identifiers: []string{"--token"}, Type: "string", Secret: true, Enum: []string{"a", "b"}},
		},
	}
	var in struct{}
	err := NewParser().Parse(rotini.NewContextFor(def, []string{"--token", "topsecret"}), &in)
	if err == nil {
		t.Fatal("expected an enum error")
	}
	if strings.Contains(err.Error(), "topsecret") {
		t.Errorf("secret flag value leaked in error: %v", err)
	}
	if !strings.Contains(err.Error(), "[redacted]") {
		t.Errorf("error should redact the secret value: %v", err)
	}
}

func TestParse_secretConstraintValueRedacted(t *testing.T) {
	def := rotini.Definition{
		Name: "app", Handler: "App",
		Flags: []rotini.FlagDef{
			{Name: "pin", Identifiers: []string{"--pin"}, Type: "string", Secret: true,
				Constraints: rotini.Constraints{Pattern: "^[0-9]{4}$"}},
		},
	}
	var in struct{}
	err := NewParser().Parse(rotini.NewContextFor(def, []string{"--pin", "supersecretpin"}), &in)
	if err == nil || strings.Contains(err.Error(), "supersecretpin") {
		t.Errorf("secret value should be redacted in the pattern error: %v", err)
	}
}

func TestParse_nonSecretValueStillShown(t *testing.T) {
	def := rotini.Definition{
		Name: "app", Handler: "App",
		Flags: []rotini.FlagDef{{Name: "color", Identifiers: []string{"--color"}, Type: "string", Enum: []string{"red", "blue"}}},
	}
	var in struct{}
	err := NewParser().Parse(rotini.NewContextFor(def, []string{"--color", "green"}), &in)
	if err == nil || !strings.Contains(err.Error(), "green") {
		t.Errorf("a non-secret value should appear in the error: %v", err)
	}
}

// TestParse_clusteredShortFlags covers POSIX short-flag grouping in every shape:
// joined booleans, separate flags, an attached value, a next-token value, and
// mixes — they should all "just work".
func TestParse_clusteredShortFlags(t *testing.T) {
	def := rotini.Definition{
		Name: "app", Handler: "App",
		Flags: []rotini.FlagDef{
			{Name: "verbose", Identifiers: []string{"-v"}, Type: "bool"},
			{Name: "help", Identifiers: []string{"-h"}, Type: "bool"},
			{Name: "num", Identifiers: []string{"-n"}, Type: "int"},
		},
	}
	type inputs struct {
		App struct {
			Flags struct {
				Verbose bool `rotini:"verbose"`
				Help    bool `rotini:"help"`
				Num     int  `rotini:"num"`
			}
			Arguments struct{}
		}
	}
	cases := []struct {
		argv []string
		v, h bool
		n    int
	}{
		{[]string{"-v"}, true, false, 0},       // exact short
		{[]string{"-vh"}, true, true, 0},       // clustered booleans
		{[]string{"-v", "-h"}, true, true, 0},  // separate
		{[]string{"-n", "5"}, false, false, 5}, // exact short + next-token value
		{[]string{"-n5"}, false, false, 5},     // short + attached value
		{[]string{"-n=5"}, false, false, 5},    // short + inline value
		{[]string{"-vn5"}, true, false, 5},     // cluster ending in attached value
		{[]string{"-vn", "5"}, true, false, 5}, // cluster ending in next-token value
		{[]string{"-vn=5"}, true, false, 5},    // cluster ending in inline value
		{[]string{"-hvn5"}, true, true, 5},     // booleans then a value flag
	}
	for _, c := range cases {
		var got inputs
		if err := NewParser().Parse(rotini.NewContextFor(def, c.argv), &got); err != nil {
			t.Errorf("%v: Parse: %v", c.argv, err)
			continue
		}
		f := got.App.Flags
		if f.Verbose != c.v || f.Help != c.h || f.Num != c.n {
			t.Errorf("%v: got v=%v h=%v n=%d, want v=%v h=%v n=%d", c.argv, f.Verbose, f.Help, f.Num, c.v, c.h, c.n)
		}
	}
}

// An exact multi-char identifier wins over cluster decomposition.
func TestParse_exactIdentifierBeatsCluster(t *testing.T) {
	def := rotini.Definition{
		Name: "app", Handler: "App",
		Flags: []rotini.FlagDef{
			{Name: "vh", Identifiers: []string{"-vh"}, Type: "bool"},
			{Name: "verbose", Identifiers: []string{"-v"}, Type: "bool"},
			{Name: "help", Identifiers: []string{"-h"}, Type: "bool"},
		},
	}
	type inputs struct {
		App struct {
			Flags struct {
				VH      bool `rotini:"vh"`
				Verbose bool `rotini:"verbose"`
				Help    bool `rotini:"help"`
			}
			Arguments struct{}
		}
	}
	var got inputs
	if err := NewParser().Parse(rotini.NewContextFor(def, []string{"-vh"}), &got); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !got.App.Flags.VH {
		t.Error("-vh should match the exact -vh identifier")
	}
	if got.App.Flags.Verbose || got.App.Flags.Help {
		t.Error("exact -vh matched, so -v/-h must not also be set")
	}
}

// An unrecognized character in a cluster is an unknown-flag error naming it.
func TestParse_clusterUnknownFlag(t *testing.T) {
	def := rotini.Definition{
		Name: "app", Handler: "App",
		Flags: []rotini.FlagDef{{Name: "verbose", Identifiers: []string{"-v"}, Type: "bool"}},
	}
	var got struct{}
	err := NewParser().Parse(rotini.NewContextFor(def, []string{"-vx"}), &got)
	if err == nil || !strings.Contains(err.Error(), `unknown flag "-x"`) {
		t.Errorf("err = %v, want unknown flag -x", err)
	}
}

// TestParse_multiCharShortVsCluster pins behavior when -x, -y, and -xy all exist,
// none bool, with different types. The exact multi-char identifier wins for the
// literal token; clustering is single-char and only on an exact miss.
func TestParse_multiCharShortVsCluster(t *testing.T) {
	def := rotini.Definition{
		Name: "app", Handler: "App",
		Flags: []rotini.FlagDef{
			{Name: "ex", Identifiers: []string{"-x"}, Type: "string"},
			{Name: "why", Identifiers: []string{"-y"}, Type: "int"},
			{Name: "exy", Identifiers: []string{"-xy"}, Type: "string"},
		},
	}
	type inputs struct {
		App struct {
			Flags struct {
				Ex  string `rotini:"ex"`
				Why int    `rotini:"why"`
				Exy string `rotini:"exy"`
			}
			Arguments struct{}
		}
	}
	parse := func(argv ...string) (inputs, error) {
		var in inputs
		err := NewParser().Parse(rotini.NewContextFor(def, argv), &in)
		return in, err
	}

	// Exact -xy wins over -x+-y; it takes its own next-token value.
	if in, err := parse("-xy", "V"); err != nil || in.App.Flags.Exy != "V" || in.App.Flags.Ex != "" || in.App.Flags.Why != 0 {
		t.Errorf("-xy V: %+v err=%v; want only Exy=V", in.App.Flags, err)
	}
	// -x and -y each take their own value.
	if in, err := parse("-x", "A", "-y", "7"); err != nil || in.App.Flags.Ex != "A" || in.App.Flags.Why != 7 {
		t.Errorf("-x A -y 7: %+v err=%v", in.App.Flags, err)
	}
	// A non-exact short token clusters single-char from the left: -x is value-taking,
	// so -xA → Ex="A" (NOT the -xy flag; multi-char ids aren't matched as prefixes).
	if in, err := parse("-xA"); err != nil || in.App.Flags.Ex != "A" || in.App.Flags.Exy != "" {
		t.Errorf("-xA: %+v err=%v; want Ex=A", in.App.Flags, err)
	}
	// Exact -xy with no value errors (it's value-taking), not silently clustered.
	if _, err := parse("-xy"); err == nil || !strings.Contains(err.Error(), "needs a value") {
		t.Errorf("-xy alone: err=%v, want needs-a-value", err)
	}
}

// TestParse_repeatedNameOnPath is the collision case: "app cmd1 cmd1" is a legal
// tree where a command name repeats on a single path. Each cmd1 declares its own
// flag; positional (not name-based) scoping must keep them in separate frames so
// the middle command's flag and the leaf's never merge.
func TestParse_repeatedNameOnPath(t *testing.T) {
	def := rotini.Definition{
		Name: "app", Handler: "App",
		Commands: []rotini.CommandDef{{
			Name: "cmd1", Handler: "AppCmd1",
			Flags: []rotini.FlagDef{{Name: "mid", Identifiers: []string{"--mid"}, Type: "string"}},
			Commands: []rotini.CommandDef{{
				Name: "cmd1", Handler: "AppCmd1Cmd1",
				Flags: []rotini.FlagDef{{Name: "leaf", Identifiers: []string{"--leaf"}, Type: "string"}},
			}},
		}},
	}
	type midInputs struct {
		Flags struct {
			Mid string `rotini:"mid"`
		}
		Arguments struct{}
	}
	type leafInputs struct {
		Flags struct {
			Leaf string `rotini:"leaf"`
		}
		Arguments struct{}
	}
	// One field per command on the path: app, the middle cmd1, the leaf cmd1.
	type inputs struct {
		App         appCommandInputs
		AppCmd1     midInputs
		AppCmd1Cmd1 leafInputs
	}

	rtx := rotini.NewContextFor(def, []string{"cmd1", "cmd1", "--mid", "M", "--leaf", "L"})
	var in inputs
	if err := NewParser().Parse(rtx, &in); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if in.AppCmd1.Flags.Mid != "M" {
		t.Errorf("middle cmd1 --mid = %q, want M", in.AppCmd1.Flags.Mid)
	}
	if in.AppCmd1Cmd1.Flags.Leaf != "L" {
		t.Errorf("leaf cmd1 --leaf = %q, want L", in.AppCmd1Cmd1.Flags.Leaf)
	}
}

// TestParse_sameLeafNameDifferentPaths covers the other shape from the original
// question: a tree with BOTH "app cmd1 cmd1" and "app cmd2 cmd1" — two distinct
// leaf commands that happen to share the name "cmd1". Each invocation must resolve
// down its own branch and bind that branch's flags only; the other branch's leaf
// flag must not be accepted.
func TestParse_sameLeafNameDifferentPaths(t *testing.T) {
	def := rotini.Definition{
		Name: "app", Handler: "App",
		Commands: []rotini.CommandDef{
			{
				Name: "cmd1", Handler: "AppCmd1",
				Flags: []rotini.FlagDef{{Name: "m1", Identifiers: []string{"--m1"}, Type: "string"}},
				Commands: []rotini.CommandDef{{
					Name: "cmd1", Handler: "AppCmd1Cmd1",
					Flags: []rotini.FlagDef{{Name: "leaf1", Identifiers: []string{"--leaf1"}, Type: "string"}},
				}},
			},
			{
				Name: "cmd2", Handler: "AppCmd2",
				Flags: []rotini.FlagDef{{Name: "m2", Identifiers: []string{"--m2"}, Type: "string"}},
				Commands: []rotini.CommandDef{{
					Name: "cmd1", Handler: "AppCmd2Cmd1",
					Flags: []rotini.FlagDef{{Name: "leaf2", Identifiers: []string{"--leaf2"}, Type: "string"}},
				}},
			},
		},
	}
	type cmd1Inputs struct {
		App     appCommandInputs
		AppCmd1 struct {
			Flags struct {
				M1 string `rotini:"m1"`
			}
			Arguments struct{}
		}
		AppCmd1Cmd1 struct {
			Flags struct {
				Leaf1 string `rotini:"leaf1"`
			}
			Arguments struct{}
		}
	}
	type cmd2Inputs struct {
		App     appCommandInputs
		AppCmd2 struct {
			Flags struct {
				M2 string `rotini:"m2"`
			}
			Arguments struct{}
		}
		AppCmd2Cmd1 struct {
			Flags struct {
				Leaf2 string `rotini:"leaf2"`
			}
			Arguments struct{}
		}
	}

	// app cmd1 cmd1 — descends the cmd1 branch; binds --m1 and --leaf1.
	var in1 cmd1Inputs
	if err := NewParser().Parse(rotini.NewContextFor(def, []string{"cmd1", "cmd1", "--m1", "M1", "--leaf1", "L1"}), &in1); err != nil {
		t.Fatalf("cmd1 cmd1: Parse: %v", err)
	}
	if in1.AppCmd1.Flags.M1 != "M1" || in1.AppCmd1Cmd1.Flags.Leaf1 != "L1" {
		t.Errorf("cmd1 cmd1 bound wrong: %+v", in1)
	}

	// app cmd2 cmd1 — descends the cmd2 branch; binds --m2 and --leaf2, with no
	// bleed from the identically-named cmd1-branch leaf.
	var in2 cmd2Inputs
	if err := NewParser().Parse(rotini.NewContextFor(def, []string{"cmd2", "cmd1", "--m2", "M2", "--leaf2", "L2"}), &in2); err != nil {
		t.Fatalf("cmd2 cmd1: Parse: %v", err)
	}
	if in2.AppCmd2.Flags.M2 != "M2" || in2.AppCmd2Cmd1.Flags.Leaf2 != "L2" {
		t.Errorf("cmd2 cmd1 bound wrong: %+v", in2)
	}

	// Isolation: the cmd1-branch leaf flag is not a flag of the cmd2-branch leaf.
	var in3 cmd2Inputs
	if err := NewParser().Parse(rotini.NewContextFor(def, []string{"cmd2", "cmd1", "--leaf1", "X"}), &in3); err == nil || !strings.Contains(err.Error(), `unknown flag "--leaf1"`) {
		t.Errorf("cross-branch flag should be unknown under cmd2 cmd1, got: %v", err)
	}
}

// The structs below mirror the shape the framework package (rtg) generates for a
// root command "app" with a sub-command "run".

type appFlags struct {
	Verbose bool `rotini:"verbose"`
}

type appCommandInputs struct {
	Flags     appFlags
	Arguments struct{}
}

type runFlags struct {
	Count int           `rotini:"count"`
	Rate  float64       `rotini:"rate"`
	Wait  time.Duration `rotini:"wait"`
	When  time.Time     `rotini:"when"`  // encoding.TextUnmarshaler (RFC3339)
	Level *int          `rotini:"level"` // nullable pointer
	Tags  []string      `rotini:"tags"`
}

type runArguments struct {
	Name string   `rotini:"name"`
	Rest []string `rotini:"rest"` // variadic
}

type runCommandInputs struct {
	Flags     runFlags
	Arguments runArguments
}

type runInputs struct {
	App appCommandInputs
	Run runCommandInputs
}

// bindStore runs the reflective binder over a hand-built parsed store, the same
// way [Parse] does after parsing fills it. It isolates coercion/scoping from argv
// parsing.
func bindStore[T any](store *parsedInputs) T {
	var out T
	bindInputs(reflect.ValueOf(&out).Elem(), store)
	return out
}

func TestInputs_bindsAllScopesAndTypes(t *testing.T) {
	in := bindStore[runInputs](&parsedInputs{scopes: []scopeInputs{
		{flags: map[string][]string{"verbose": {"true"}}}, // app  (chain[0]) → in.App
		{ // run (chain[1]) → in.Run
			flags: map[string][]string{
				"count": {"7"},
				"rate":  {"2.5"},
				"wait":  {"1m30s"},
				"when":  {"2026-05-22T00:00:00Z"},
				"level": {"4"},
				"tags":  {"a", "b", "c"},
			},
			args: []string{"world", "extra1", "extra2"},
		},
	}})

	if !in.App.Flags.Verbose {
		t.Errorf("ancestor scope flag not bound: App.Flags.Verbose = false")
	}
	if in.Run.Flags.Count != 7 {
		t.Errorf("Count = %d, want 7", in.Run.Flags.Count)
	}
	if in.Run.Flags.Rate != 2.5 {
		t.Errorf("Rate = %v, want 2.5", in.Run.Flags.Rate)
	}
	if in.Run.Flags.Wait != 90*time.Second {
		t.Errorf("Wait = %v, want 1m30s", in.Run.Flags.Wait)
	}
	if in.Run.Flags.When.Year() != 2026 {
		t.Errorf("When not coerced via TextUnmarshaler: %v", in.Run.Flags.When)
	}
	if in.Run.Flags.Level == nil || *in.Run.Flags.Level != 4 {
		t.Errorf("nullable Level pointer not bound: %v", in.Run.Flags.Level)
	}
	if got := in.Run.Flags.Tags; len(got) != 3 || got[0] != "a" || got[2] != "c" {
		t.Errorf("Tags = %v, want [a b c]", got)
	}
	if in.Run.Arguments.Name != "world" {
		t.Errorf("Name = %q, want world", in.Run.Arguments.Name)
	}
	if got := in.Run.Arguments.Rest; len(got) != 2 || got[0] != "extra1" || got[1] != "extra2" {
		t.Errorf("Rest = %v, want [extra1 extra2]", got)
	}
}

// A composed child's input type describes only its own root→leaf path, so it has
// fewer fields than the full resolved chain when reached under a parent. Binding
// is leaf-aligned: the struct's fields map to the trailing chain frames, and any
// extra parent frame is left unbound. (This is also what makes repeated names on a
// path — e.g. "app run run" — safe: position, not name, is the key.)
func TestInputs_bindsLeafAlignedUnderComposition(t *testing.T) {
	in := bindStore[runInputs](&parsedInputs{scopes: []scopeInputs{
		{flags: map[string][]string{"verbose": {"ignored"}}},              // parent frame (chain[0]) — unbound
		{flags: map[string][]string{"verbose": {"true"}}},                 // app  (chain[1]) → in.App
		{flags: map[string][]string{"count": {"3"}}, args: []string{"x"}}, // run  (chain[2]) → in.Run
	}})
	if !in.App.Flags.Verbose || in.Run.Flags.Count != 3 || in.Run.Arguments.Name != "x" {
		t.Errorf("leaf-aligned composed binding failed: %+v", in)
	}
}

func TestInputs_missingFlagKeepsZero(t *testing.T) {
	in := bindStore[runInputs](&parsedInputs{scopes: []scopeInputs{
		{}, // app (chain[0])
		{flags: map[string][]string{"count": {"5"}}}, // run (chain[1]); rate/wait/level absent
	}})
	if in.Run.Flags.Count != 5 || in.Run.Flags.Rate != 0 || in.Run.Flags.Wait != 0 || in.Run.Flags.Level != nil {
		t.Errorf("absent flags should stay zero: %+v", in.Run.Flags)
	}
}
