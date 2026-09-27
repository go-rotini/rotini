package rotini

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

// parserTestDef mirrors a root "app" with a sub-command "run", matching the runInputs
// scopes used by the binder tests.
func parserTestDef() Definition {
	return Definition{
		Name:    "app",
		Handler: "App",
		Flags:   []FlagDef{{Name: "verbose", Identifiers: []string{"-v", "--verbose"}, Type: "bool"}},
		Commands: []CommandDef{
			{
				Name:    "run",
				Handler: "AppRun",
				Aliases: []string{"r"},
				Flags:   []FlagDef{{Name: "count", Identifiers: []string{"-c", "--count"}, Type: "int"}},
				Arguments: []ArgDef{
					{Name: "name", Type: "string"},
					{Name: "rest", Type: "[]string", Variadic: true},
				},
			},
		},
	}
}

func TestParse_bindsInputs(t *testing.T) {
	rtx := NewContextFor(parserTestDef(), []string{"--verbose", "run", "alice", "x", "y", "--count", "3"})
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
// The parser a handler reaches is the one the program supplied — and Context.Parser never
// returns nil, so a handler that wants to parse argv itself does not have to ask whether one
// exists, nor supply one to make the answer yes.
func TestParse_viaContextParser(t *testing.T) {
	rtx := NewContextFor(parserTestDef(), []string{"run", "alice"})
	supplied := NewParser()
	rtx.WithParser(supplied)

	parser := rtx.Parser()
	if parser != supplied {
		t.Fatalf("Parser() = %p, want the supplied %p", parser, supplied)
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
	if err := p.Parse(NewContextFor(parserTestDef(), []string{"run"}), &in); err == nil || !strings.Contains(err.Error(), "nil parser") {
		t.Errorf("Parse on nil parser = %v, want nil-parser error", err)
	}
}

func TestParse_outMustBePointer(t *testing.T) {
	rtx := NewContextFor(parserTestDef(), []string{"run"})
	var in runInputs
	if err := NewParser().Parse(rtx, in); err == nil || !strings.Contains(err.Error(), "pointer") {
		t.Errorf("Parse with non-pointer out = %v, want pointer error", err)
	}
}

func TestParse_unresolvedContext(t *testing.T) {
	var in runInputs
	if err := NewParser().Parse(newContext(), &in); err == nil {
		t.Error("Parse on an unresolved context should error")
	}
	if in.Run.Flags.Count != 0 || in.App.Flags.Verbose {
		t.Errorf("out should stay zero on error, got %+v", in)
	}
}

func TestParse_unknownFlag(t *testing.T) {
	rtx := NewContextFor(parserTestDef(), []string{"run", "--nope"})
	var in runInputs
	if err := NewParser().Parse(rtx, &in); err == nil || !strings.Contains(err.Error(), `unknown flag "--nope"`) {
		t.Errorf("Parse error = %v, want unknown-flag", err)
	}
}

func TestParse_flagNeedsValue(t *testing.T) {
	rtx := NewContextFor(parserTestDef(), []string{"run", "--count"})
	var in runInputs
	if err := NewParser().Parse(rtx, &in); err == nil || !strings.Contains(err.Error(), "needs a value") {
		t.Errorf("Parse error = %v, want needs-a-value", err)
	}
}

func TestParse_unknownCommand(t *testing.T) {
	// "ru" is a stray positional on a branch-only root: a mistyped sub-command.
	// The error is data, not presentation: no baked-in suggestion text — the
	// structured fields carry the token and the sibling vocabulary so a handler
	// composes its own response (typically with a bound [Suggestor]).
	rtx := NewContextFor(parserTestDef(), []string{"ru"})
	var in runInputs
	err := NewParser().Parse(rtx, &in)
	if err == nil || !strings.Contains(err.Error(), `unknown command "ru"`) {
		t.Fatalf("Parse error = %v, want unknown-command", err)
	}
	if strings.Contains(err.Error(), "Did you mean") {
		t.Errorf("Parse error carries baked-in suggestion text: %v", err)
	}
	var pe *ParseError
	if !errors.As(err, &pe) {
		t.Fatalf("Parse error is not a *ParseError: %T", err)
	}
	if pe.Token != "ru" || pe.Command != "app" {
		t.Errorf("ParseError fields = {Token:%q Command:%q}, want {ru app}", pe.Token, pe.Command)
	}
	if !slices.Contains(pe.Candidates, "run") {
		t.Errorf("ParseError.Candidates = %v, want to contain \"run\"", pe.Candidates)
	}
	// (The ParseError → Suggestor integration is exercised in the Suggestor tests; here we
	// only pin that the parser populates Token/Candidates.)
}

func TestParse_missingRequired(t *testing.T) {
	def := Definition{
		Name: "app", Handler: "App",
		Flags: []FlagDef{{Name: "token", Identifiers: []string{"--token"}, Type: "string", Required: true}},
	}
	rtx := NewContextFor(def, []string{})
	var in struct{}
	err := NewParser().Parse(rtx, &in)
	if err == nil || !strings.Contains(err.Error(), "missing required") || !strings.Contains(err.Error(), "--token") {
		t.Errorf("Parse error = %v, want missing-required --token", err)
	}
}

func TestParse_badEnumValue(t *testing.T) {
	def := Definition{
		Name: "app", Handler: "App",
		Flags: []FlagDef{{Name: "level", Identifiers: []string{"--level"}, Type: "string", Enum: []string{"low", "high"}}},
	}
	rtx := NewContextFor(def, []string{"--level", "medium"})
	var in struct{}
	err := NewParser().Parse(rtx, &in)
	if err == nil || !strings.Contains(err.Error(), "invalid value") || !strings.Contains(err.Error(), "one of: low, high") {
		t.Errorf("Parse error = %v, want bad-enum", err)
	}
}

func TestParse_defaultsApplied(t *testing.T) {
	def := Definition{
		Name: "app", Handler: "App",
		Flags: []FlagDef{{Name: "count", Identifiers: []string{"--count"}, Type: "int", Default: "9"}},
	}
	type inputs struct {
		App struct {
			Flags struct {
				Count int `rotini:"count"`
			}
			Arguments struct{}
		}
	}
	rtx := NewContextFor(def, []string{})
	var in inputs
	if err := NewParser().Parse(rtx, &in); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if in.App.Flags.Count != 9 {
		t.Errorf("default not applied: Count = %d, want 9", in.App.Flags.Count)
	}
}

// FuzzParse drives the full argv grammar (descent, long/short/inline/cluster
// flags, the -- terminator, negative numbers, repeats, typed coercion across
// every vocabulary type) with arbitrary token streams. Errors are expected and
// fine — a panic is the only failure. The fuzzed definitions deliberately
// declare no from: modes, so the fuzzer cannot make the parser read arbitrary
// files; the stdin reader is pinned.
func FuzzParse(f *testing.F) {
	for _, seed := range []string{
		"run alice x y --count 3",
		"--verbose run -- -5 --flag=x",
		"-v run --count=2 -- --",
		"run --count --count 2 -",
		"--b=yep --ai 1 --ai x --mi k=v --t bad -- -0.5",
		"--label a.b=c --label = --label =x",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, argline string) {
		argv := strings.Fields(argline)

		var chained runInputs
		rtx := NewContextFor(parserTestDef(), argv)
		rtx.Stdin = strings.NewReader("payload")
		_ = NewParser().Parse(rtx, &chained)

		var typed matrixInputs
		rtx2 := NewContextFor(matrixDef(), argv)
		rtx2.Stdin = strings.NewReader("payload")
		_ = NewParser().Parse(rtx2, &typed)
	})
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
	def := Definition{
		Name: "app", Handler: "App",
		Flags: []FlagDef{{
			Name: "port", Identifiers: []string{"--port"}, Type: "int",
			Minimum: Ptr(1.0), Maximum: Ptr(65535.0),
		}},
	}
	for _, c := range []struct{ val, wantErr string }{
		{"8080", ""},
		{"0", "must be >= 1"},
		{"70000", "must be <= 65535"},
	} {
		var in struct{}
		err := NewParser().Parse(NewContextFor(def, []string{"--port", c.val}), &in)
		assertConstraint(t, c.val, err, c.wantErr)
	}
}

// TestParse_constraintsWidenedTypes pins the R1 fix: bounds enforce across
// The R2-S7 constraint set: presence-carrying bounds make minimum: 0 a real,
// enforced bound (the F4 zero-sentinel rejection is reversed), and the
// exclusive bounds + multipleOf land with it — per-element on repeatables,
// like every numeric constraint.
func TestParse_constraintsExclusiveAndZero(t *testing.T) {
	def := Definition{
		Name: "app", Handler: "App",
		Flags: []FlagDef{
			{Name: "delta", Identifiers: []string{"--delta"}, Type: "int",
				Minimum: Ptr(0.0)}, // the once-rejected zero bound
			{Name: "rate", Identifiers: []string{"--rate"}, Type: "float64",
				ExclusiveMinimum: Ptr(0.0), ExclusiveMaximum: Ptr(1.0)},
			{Name: "step", Identifiers: []string{"--step"}, Type: "int",
				MultipleOf: Ptr(5.0)},
			{Name: "ports", Identifiers: []string{"--ports"}, Type: "[]int",
				MultipleOf: Ptr(2.0)},
		},
	}
	cases := []struct {
		argv    []string
		wantErr string // "" = must parse clean
	}{
		{[]string{"--delta", "0"}, ""},
		{[]string{"--delta", "-1"}, "must be >= 0"},
		{[]string{"--rate", "0.5"}, ""},
		{[]string{"--rate", "0"}, "must be > 0"},
		{[]string{"--rate", "1"}, "must be < 1"},
		{[]string{"--step", "15"}, ""},
		{[]string{"--step", "7"}, "must be a multiple of 5"},
		{[]string{"--ports", "4", "--ports", "6"}, ""},
		{[]string{"--ports", "4", "--ports", "5"}, "must be a multiple of 2"}, // per element
	}
	for _, c := range cases {
		var in struct {
			App struct {
				Flags struct {
					Delta int     `rotini:"delta"`
					Rate  float64 `rotini:"rate"`
					Step  int     `rotini:"step"`
					Ports []int   `rotini:"ports"`
				}
				Arguments struct{}
			}
		}
		err := NewParser().Parse(NewContextFor(def, c.argv), &in)
		if c.wantErr == "" {
			if err != nil {
				t.Errorf("Parse(%v) = %v, want nil", c.argv, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), c.wantErr) {
			t.Errorf("Parse(%v) = %v, want an error containing %q", c.argv, err, c.wantErr)
		}
	}
}

// the full numeric family (the old allowlist was int|float64 only — uint,
// int64, float32 bounds were silently ignored), and per-value constraints
// apply to a repeatable input's ELEMENTS.
func TestParse_constraintsWidenedTypes(t *testing.T) {
	def := Definition{
		Name: "app", Handler: "App",
		Flags: []FlagDef{
			{Name: "workers", Identifiers: []string{"--workers"}, Type: "uint",
				Minimum: Ptr(1.0), Maximum: Ptr(64.0)},
			{Name: "offset", Identifiers: []string{"--offset"}, Type: "int64",
				Minimum: Ptr(-100.0), Maximum: Ptr(100.0)},
			{Name: "rate", Identifiers: []string{"--rate"}, Type: "float32",
				Maximum: Ptr(1.0)},
			{Name: "port", Identifiers: []string{"--port"}, Type: "[]int",
				Minimum: Ptr(1.0), Maximum: Ptr(65535.0), MaxItems: 3},
			{Name: "tag", Identifiers: []string{"--tag"}, Type: "[]string",
				MinLength: 2},
		},
	}
	cases := []struct {
		argv    []string
		wantErr string // "" = must pass
	}{
		{[]string{"--workers", "8"}, ""},
		{[]string{"--workers", "0"}, "must be >= 1"},    // was silently accepted pre-R1
		{[]string{"--workers", "100"}, "must be <= 64"}, //
		{[]string{"--offset", "-200"}, "must be >= -100"},
		{[]string{"--rate", "1.5"}, "must be <= 1"},
		{[]string{"--port", "80", "--port", "443"}, ""},
		{[]string{"--port", "80", "--port", "0"}, "must be >= 1"}, // element bounds
		{[]string{"--port", "1", "--port", "2", "--port", "3", "--port", "4"}, "at most 3"},
		{[]string{"--tag", "ok", "--tag", "x"}, "at least 2 characters"}, // element length
	}
	for _, c := range cases {
		var in struct{}
		err := NewParser().Parse(NewContextFor(def, c.argv), &in)
		assertConstraint(t, strings.Join(c.argv, " "), err, c.wantErr)
	}
}

func TestParse_stringLengthConstraints(t *testing.T) {
	def := Definition{
		Name: "app", Handler: "App",
		Flags: []FlagDef{{
			Name: "name", Identifiers: []string{"--name"}, Type: "string",
			MinLength: 2, MaxLength: 5,
		}},
	}
	for _, c := range []struct{ val, wantErr string }{
		{"abc", ""},
		{"a", "at least 2"},
		{"toolong", "at most 5"},
	} {
		var in struct{}
		err := NewParser().Parse(NewContextFor(def, []string{"--name", c.val}), &in)
		assertConstraint(t, c.val, err, c.wantErr)
	}
}

func TestParse_patternConstraint(t *testing.T) {
	def := Definition{
		Name: "app", Handler: "App",
		Flags: []FlagDef{{
			Name: "id", Identifiers: []string{"--id"}, Type: "string",
			Pattern: "^[a-z]+$",
		}},
	}
	for _, c := range []struct{ val, wantErr string }{
		{"abc", ""},
		{"ABC", "must match"},
	} {
		var in struct{}
		err := NewParser().Parse(NewContextFor(def, []string{"--id", c.val}), &in)
		assertConstraint(t, c.val, err, c.wantErr)
	}
}

func TestParse_itemCountConstraints(t *testing.T) {
	def := Definition{
		Name: "app", Handler: "App",
		Flags: []FlagDef{{
			Name: "tag", Identifiers: []string{"--tag"}, Type: "[]string",
			MinItems: 1, MaxItems: 2,
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
		err := NewParser().Parse(NewContextFor(def, c.argv), &in)
		assertConstraint(t, strings.Join(c.argv, " "), err, c.wantErr)
	}
}

// A MinItems bound applies to a variadic argument even when it receives no values
// (a distinct code path from a repeatable flag).
func TestParse_variadicArgItemCount(t *testing.T) {
	def := Definition{
		Name: "app", Handler: "App",
		Arguments: []ArgDef{{
			Name: "files", Type: "[]string", Variadic: true,
			MinItems: 2,
		}},
	}
	var in struct{}
	if err := NewParser().Parse(NewContextFor(def, []string{"only-one"}), &in); err == nil || !strings.Contains(err.Error(), "at least 2") {
		t.Errorf("error = %v, want at-least-2 for variadic <files>", err)
	}
	if err := NewParser().Parse(NewContextFor(def, []string{"a", "b"}), &in); err != nil {
		t.Errorf("unexpected error for two files: %v", err)
	}
}

// TestParse_doubleDashTerminator pins FLAG-07 through Parser.Parse (resolveChain's
// handling has its own test): everything after "--" is positional — even tokens
// that look like flags — and bare "-" is an ordinary positional value anywhere
// (its stdin-sentinel *meaning* belongs to the handler; the grammar just passes
// it through).
func TestParse_doubleDashTerminator(t *testing.T) {
	def := Definition{
		Name: "app", Handler: "App",
		Flags:     []FlagDef{{Name: "verbose", Identifiers: []string{"--verbose", "-v"}, Type: "bool"}},
		Arguments: []ArgDef{{Name: "files", Type: "[]string", Variadic: true}},
	}
	type inputs struct {
		App struct {
			Flags struct {
				Verbose bool `rotini:"verbose"`
			}
			Arguments struct {
				Files []string `rotini:"files"`
			}
		}
	}

	var in inputs
	if err := NewParser().Parse(NewContextFor(def, []string{"--verbose", "--", "--not-a-flag", "-", "-v"}), &in); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !in.App.Flags.Verbose {
		t.Error("flag before the -- terminator not parsed")
	}
	if want := []string{"--not-a-flag", "-", "-v"}; !reflect.DeepEqual(in.App.Arguments.Files, want) {
		t.Errorf("post-terminator positionals = %v, want %v (flag-looking tokens included)", in.App.Arguments.Files, want)
	}

	var alone inputs
	if err := NewParser().Parse(NewContextFor(def, []string{"-"}), &alone); err != nil {
		t.Fatalf("Parse(bare -): %v", err)
	}
	if want := []string{"-"}; !reflect.DeepEqual(alone.App.Arguments.Files, want) {
		t.Errorf("bare - = %v, want %v (a value, never a flag)", alone.App.Arguments.Files, want)
	}
}

func TestParse_negativeNumberArguments(t *testing.T) {
	def := Definition{
		Name: "app", Handler: "App",
		Arguments: []ArgDef{{Name: "delta", Type: "float64"}},
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
		if err := NewParser().Parse(NewContextFor(def, c.argv), &in); err != nil {
			t.Fatalf("Parse(%v): %v", c.argv, err)
		}
		if in.App.Arguments.Delta != c.want {
			t.Errorf("Delta for %v = %v, want %v", c.argv, in.App.Arguments.Delta, c.want)
		}
	}
}

func TestParse_negativeNumberFlagValue(t *testing.T) {
	def := Definition{
		Name: "app", Handler: "App",
		Flags: []FlagDef{{Name: "offset", Identifiers: []string{"--offset"}, Type: "int"}},
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
	if err := NewParser().Parse(NewContextFor(def, []string{"--offset", "-5"}), &in); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if in.App.Flags.Offset != -5 {
		t.Errorf("Offset = %d, want -5", in.App.Flags.Offset)
	}
}

func TestParse_secretValueRedactedInErrors(t *testing.T) {
	def := Definition{
		Name: "app", Handler: "App",
		Flags: []FlagDef{
			{Name: "token", Identifiers: []string{"--token"}, Type: "string", Secret: true, Enum: []string{"a", "b"}},
		},
	}
	var in struct{}
	err := NewParser().Parse(NewContextFor(def, []string{"--token", "topsecret"}), &in)
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
	def := Definition{
		Name: "app", Handler: "App",
		Flags: []FlagDef{
			{Name: "pin", Identifiers: []string{"--pin"}, Type: "string", Secret: true,
				Pattern: "^[0-9]{4}$"},
		},
	}
	var in struct{}
	err := NewParser().Parse(NewContextFor(def, []string{"--pin", "supersecretpin"}), &in)
	if err == nil || strings.Contains(err.Error(), "supersecretpin") {
		t.Errorf("secret value should be redacted in the pattern error: %v", err)
	}
}

func TestParse_nonSecretValueStillShown(t *testing.T) {
	def := Definition{
		Name: "app", Handler: "App",
		Flags: []FlagDef{{Name: "color", Identifiers: []string{"--color"}, Type: "string", Enum: []string{"red", "blue"}}},
	}
	var in struct{}
	err := NewParser().Parse(NewContextFor(def, []string{"--color", "green"}), &in)
	if err == nil || !strings.Contains(err.Error(), "green") {
		t.Errorf("a non-secret value should appear in the error: %v", err)
	}
}

func TestParse_flagGroups(t *testing.T) {
	def := func(kind FlagGroupKind) Definition {
		return Definition{
			Name: "app", Handler: "App",
			Flags: []FlagDef{
				{Name: "json", Identifiers: []string{"--json"}, Type: "bool"},
				{Name: "yaml", Identifiers: []string{"--yaml"}, Type: "bool"},
			},
			FlagGroups: []FlagGroup{{Kind: kind, Flags: []string{"json", "yaml"}}},
		}
	}
	cases := []struct {
		name    string
		kind    FlagGroupKind
		argv    []string
		wantErr string // "" = should pass
	}{
		{"exclusive: both → err", FlagGroupMutuallyExclusive, []string{"--json", "--yaml"}, "mutually exclusive"},
		{"exclusive: one → ok", FlagGroupMutuallyExclusive, []string{"--json"}, ""},
		{"exclusive: none → ok", FlagGroupMutuallyExclusive, nil, ""},
		{"together: one → err", FlagGroupRequiredTogether, []string{"--json"}, "must be used together"},
		{"together: both → ok", FlagGroupRequiredTogether, []string{"--json", "--yaml"}, ""},
		{"together: none → ok", FlagGroupRequiredTogether, nil, ""},
		{"one_of: none → err", FlagGroupOneOf, nil, "exactly one"},
		{"one_of: one → ok", FlagGroupOneOf, []string{"--yaml"}, ""},
		{"one_of: both → err", FlagGroupOneOf, []string{"--json", "--yaml"}, "mutually exclusive"},
		{"at_least_one: none → err", FlagGroupAtLeastOne, nil, "at least one"},
		{"at_least_one: one → ok", FlagGroupAtLeastOne, []string{"--json"}, ""},
		{"at_least_one: both → ok", FlagGroupAtLeastOne, []string{"--json", "--yaml"}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var in struct{}
			err := NewParser().Parse(NewContextFor(def(c.kind), c.argv), &in)
			switch {
			case c.wantErr == "" && err != nil:
				t.Errorf("Parse(%v) = %v, want ok", c.argv, err)
			case c.wantErr != "" && (err == nil || !strings.Contains(err.Error(), c.wantErr)):
				t.Errorf("Parse(%v) = %v, want error containing %q", c.argv, err, c.wantErr)
			}
		})
	}
}

func TestParser_deprecations(t *testing.T) {
	// A migration: command 'build' renamed to 'compile' (old name kept as a deprecated
	// alias); flag --conf renamed to --config (old identifier kept but deprecated).
	def := Definition{
		Name: "app", Handler: "App",
		Commands: []CommandDef{{
			Name: "compile", Handler: "AppCompile",
			Aliases:               []string{"build"},
			DeprecatedIdentifiers: []string{"build"},
			Flags: []FlagDef{
				{Name: "config", Identifiers: []string{"--config", "--conf"}, Type: "string", DeprecatedIdentifiers: []string{"--conf"}},
			},
		}},
	}
	type inputs struct {
		App     struct{ Flags, Arguments struct{} }
		Compile struct {
			Flags struct {
				Config string `rotini:"config"`
			}
			Arguments struct{}
		}
	}
	depsFor := func(argv ...string) []Deprecation {
		rtx := NewContextFor(def, argv)
		var in inputs
		p := NewParser()
		if err := p.Parse(rtx, &in); err != nil {
			t.Fatalf("parse %v: %v", argv, err)
		}
		return Deprecations(rtx)
	}

	// Deprecation implements error, so a handler can return/print it.
	var _ error = Deprecation{}

	// Old alias 'build' + old flag spelling '--conf' → both reported, with the exact token.
	deps := depsFor("build", "--conf", "x")
	if len(deps) != 2 {
		t.Fatalf("Deprecations(build --conf) = %v, want 2", deps)
	}
	var sawCmd, sawFlag bool
	for _, d := range deps {
		if d.Kind == "command" && d.Name == "compile" && d.Identifier == "build" {
			sawCmd = true
		}
		if d.Kind == "flag" && d.Name == "config" && d.Identifier == "--conf" {
			sawFlag = true
		}
	}
	if !sawCmd {
		t.Errorf("deprecated command alias 'build' not reported: %v", deps)
	}
	if !sawFlag {
		t.Errorf("deprecated flag identifier '--conf' not reported: %v", deps)
	}

	// The current name + current flag spelling → nothing deprecated.
	if got := depsFor("compile", "--config", "x"); len(got) != 0 {
		t.Errorf("Deprecations(compile --config) = %v, want none", got)
	}
}

// TestDeprecations_needsNoParserBound is the point of making Deprecations a function.
//
// It used to be a method, so a handler had to pull a *Parser out of the registry to get a
// receiver it never used. That made `Bind(KeyParser, NewParser())` look mandatory in every
// entrypoint — and a user who removed the line, reasonably, since nothing else read it,
// SILENTLY lost deprecation reporting: the conventional `Get`+ok guard simply skipped the
// loop. Nothing failed and nothing said so.
//
// The chain here is resolved with no services bound at all.
func TestDeprecations_needsNoParserBound(t *testing.T) {
	def := Definition{
		Name: "app", Handler: "App",
		Commands: []CommandDef{{
			Name: "compile", Handler: "AppCompile",
			Aliases:               []string{"build"},
			DeprecatedIdentifiers: []string{"build"},
		}},
	}

	rtx := NewContextFor(def, []string{"build"})
	if len(rtx.services) != 0 {
		t.Fatal("something is bound; this test is meaningless unless the registry is empty")
	}

	deps := Deprecations(rtx)
	if len(deps) != 1 {
		t.Fatalf("Deprecations = %v, want the one deprecated alias", deps)
	}
	if deps[0].Kind != "command" || deps[0].Identifier != "build" || deps[0].Name != "compile" {
		t.Errorf("Deprecations = %+v, want command compile via \"build\"", deps[0])
	}
}

// TestDeprecations_nilContext keeps the defensive path covered now that the receiver is gone.
func TestDeprecations_nilContext(t *testing.T) {
	if got := Deprecations(nil); got != nil {
		t.Errorf("Deprecations(nil) = %v, want nil", got)
	}
}

func TestParse_coercionErrors(t *testing.T) {
	def := Definition{
		Name: "app", Handler: "App",
		Flags: []FlagDef{
			{Name: "count", Identifiers: []string{"-c", "--count"}, Type: "int"},
			{Name: "ttl", Identifiers: []string{"--ttl"}, Type: "time.Duration"},
			{Name: "when", Identifiers: []string{"--when"}, Type: "time.Time"}, // custom: TextUnmarshaler
		},
		Arguments: []ArgDef{{Name: "n", Type: "int"}},
	}
	type inputs struct {
		App struct {
			Flags struct {
				Count int           `rotini:"count"`
				TTL   time.Duration `rotini:"ttl"`
				When  time.Time     `rotini:"when"`
			}
			Arguments struct {
				N int `rotini:"n"`
			}
		}
	}
	parse := func(argv ...string) error {
		var in inputs
		return NewParser().Parse(NewContextFor(def, argv), &in)
	}

	cases := []struct {
		argv []string
		want string // substring; "" = should parse
	}{
		{[]string{"--count", "abc"}, "-c"},         // built-in int: was silently 0, now errors (label = first identifier)
		{[]string{"--ttl", "soon"}, "--ttl"},       // built-in duration
		{[]string{"--when", "tomorrow"}, "--when"}, // custom type (TextUnmarshaler error surfaced)
		{[]string{"notanint"}, "<n>"},              // positional argument coercion
		{[]string{"-c", "5", "42"}, ""},            // all valid → no error
	}
	for _, c := range cases {
		err := parse(c.argv...)
		switch {
		case c.want == "" && err != nil:
			t.Errorf("parse %v = %v, want ok", c.argv, err)
		case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want) || !strings.Contains(err.Error(), "is not a valid")):
			t.Errorf("parse %v = %v, want a coercion error mentioning %q", c.argv, err, c.want)
		}
	}
}

func TestParse_mapFlag(t *testing.T) {
	def := Definition{
		Name: "app", Handler: "App",
		Flags: []FlagDef{
			{Name: "label", Identifiers: []string{"--label", "-l"}, Type: "map[string]string"},
			{Name: "port", Identifiers: []string{"--port"}, Type: "map[string]int"},
		},
	}
	type inputs struct {
		App struct {
			Flags struct {
				Label map[string]string `rotini:"label"`
				Port  map[string]int    `rotini:"port"`
			}
			Arguments struct{}
		}
	}
	parse := func(argv ...string) (inputs, error) {
		var in inputs
		err := NewParser().Parse(NewContextFor(def, argv), &in)
		return in, err
	}

	// Repeated occurrences accumulate into the map; a duplicate key's last value wins.
	in, err := parse("--label", "env=prod", "-l", "team=core", "--label", "env=dev")
	if err != nil {
		t.Fatalf("parse map flag: %v", err)
	}
	if m := in.App.Flags.Label; len(m) != 2 || m["env"] != "dev" || m["team"] != "core" {
		t.Errorf("Label = %v, want {env:dev team:core}", m)
	}

	// The value may contain '=' — only the first '=' splits key from value.
	if in, _ := parse("--label", "url=http://x?a=b"); in.App.Flags.Label["url"] != "http://x?a=b" {
		t.Errorf("value with '=' = %q, want %q", in.App.Flags.Label["url"], "http://x?a=b")
	}

	// Typed map values are coerced into the element type.
	if in, _ := parse("--port", "web=8080"); in.App.Flags.Port["web"] != 8080 {
		t.Errorf("Port[web] = %d, want 8080", in.App.Flags.Port["web"])
	}

	// A pair without '=' is rejected with a clear message.
	if _, err := parse("--label", "oops"); err == nil || !strings.Contains(err.Error(), "key=value") {
		t.Errorf("malformed pair: want a key=value error, got %v", err)
	}
}

func TestParse_flagDependencies(t *testing.T) {
	def := Definition{
		Name: "app", Handler: "App",
		Flags: []FlagDef{
			{Name: "tls", Identifiers: []string{"--tls"}, Type: "bool"},
			{Name: "cert", Identifiers: []string{"--cert"}, Type: "string"},
			{Name: "key", Identifiers: []string{"--key"}, Type: "string"},
		},
		FlagDependencies: []FlagDependency{{When: "tls", Requires: []string{"cert", "key"}}},
	}
	cases := []struct {
		name    string
		argv    []string
		wantErr string // "" = should pass
	}{
		{"trigger absent → ok", nil, ""},
		{"trigger absent, a required one set → ok", []string{"--cert", "c"}, ""},
		{"trigger set, all required set → ok", []string{"--tls", "--cert", "c", "--key", "k"}, ""},
		{"trigger set, one missing → err", []string{"--tls", "--cert", "c"}, "--key is required when --tls is set"},
		{"trigger set, both missing → err", []string{"--tls"}, "required when --tls is set"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var in struct{}
			err := NewParser().Parse(NewContextFor(def, c.argv), &in)
			switch {
			case c.wantErr == "" && err != nil:
				t.Errorf("Parse(%v) = %v, want ok", c.argv, err)
			case c.wantErr != "" && (err == nil || !strings.Contains(err.Error(), c.wantErr)):
				t.Errorf("Parse(%v) = %v, want error containing %q", c.argv, err, c.wantErr)
			}
		})
	}
}

// TestParse_clusteredShortFlags covers POSIX short-flag grouping in every shape:
// joined booleans, separate flags, an attached value, a next-token value, and
// mixes — they should all "just work".
func TestParse_clusteredShortFlags(t *testing.T) {
	def := Definition{
		Name: "app", Handler: "App",
		Flags: []FlagDef{
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
		if err := NewParser().Parse(NewContextFor(def, c.argv), &got); err != nil {
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
	def := Definition{
		Name: "app", Handler: "App",
		Flags: []FlagDef{
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
	if err := NewParser().Parse(NewContextFor(def, []string{"-vh"}), &got); err != nil {
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
	def := Definition{
		Name: "app", Handler: "App",
		Flags: []FlagDef{{Name: "verbose", Identifiers: []string{"-v"}, Type: "bool"}},
	}
	var got struct{}
	err := NewParser().Parse(NewContextFor(def, []string{"-vx"}), &got)
	if err == nil || !strings.Contains(err.Error(), `unknown flag "-x"`) {
		t.Errorf("err = %v, want unknown flag -x", err)
	}
}

// TestParse_multiCharShortVsCluster pins behavior when -x, -y, and -xy all exist,
// none bool, with different types. The exact multi-char identifier wins for the
// literal token; clustering is single-char and only on an exact miss.
func TestParse_multiCharShortVsCluster(t *testing.T) {
	def := Definition{
		Name: "app", Handler: "App",
		Flags: []FlagDef{
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
		err := NewParser().Parse(NewContextFor(def, argv), &in)
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
	def := Definition{
		Name: "app", Handler: "App",
		Commands: []CommandDef{{
			Name: "cmd1", Handler: "AppCmd1",
			Flags: []FlagDef{{Name: "mid", Identifiers: []string{"--mid"}, Type: "string"}},
			Commands: []CommandDef{{
				Name: "cmd1", Handler: "AppCmd1Cmd1",
				Flags: []FlagDef{{Name: "leaf", Identifiers: []string{"--leaf"}, Type: "string"}},
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

	rtx := NewContextFor(def, []string{"cmd1", "cmd1", "--mid", "M", "--leaf", "L"})
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
	def := Definition{
		Name: "app", Handler: "App",
		Commands: []CommandDef{
			{
				Name: "cmd1", Handler: "AppCmd1",
				Flags: []FlagDef{{Name: "m1", Identifiers: []string{"--m1"}, Type: "string"}},
				Commands: []CommandDef{{
					Name: "cmd1", Handler: "AppCmd1Cmd1",
					Flags: []FlagDef{{Name: "leaf1", Identifiers: []string{"--leaf1"}, Type: "string"}},
				}},
			},
			{
				Name: "cmd2", Handler: "AppCmd2",
				Flags: []FlagDef{{Name: "m2", Identifiers: []string{"--m2"}, Type: "string"}},
				Commands: []CommandDef{{
					Name: "cmd1", Handler: "AppCmd2Cmd1",
					Flags: []FlagDef{{Name: "leaf2", Identifiers: []string{"--leaf2"}, Type: "string"}},
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

	// app cmd1 — descends the cmd1 branch; binds --m1 and --leaf1.
	var in1 cmd1Inputs
	if err := NewParser().Parse(NewContextFor(def, []string{"cmd1", "cmd1", "--m1", "M1", "--leaf1", "L1"}), &in1); err != nil {
		t.Fatalf("cmd1 cmd1: Parse: %v", err)
	}
	if in1.AppCmd1.Flags.M1 != "M1" || in1.AppCmd1Cmd1.Flags.Leaf1 != "L1" {
		t.Errorf("cmd1 bound wrong: %+v", in1)
	}

	// app cmd2 cmd1 — descends the cmd2 branch; binds --m2 and --leaf2, with no
	// bleed from the identically-named cmd1-branch leaf.
	var in2 cmd2Inputs
	if err := NewParser().Parse(NewContextFor(def, []string{"cmd2", "cmd1", "--m2", "M2", "--leaf2", "L2"}), &in2); err != nil {
		t.Fatalf("cmd2 cmd1: Parse: %v", err)
	}
	if in2.AppCmd2.Flags.M2 != "M2" || in2.AppCmd2Cmd1.Flags.Leaf2 != "L2" {
		t.Errorf("cmd2 cmd1 bound wrong: %+v", in2)
	}

	// Isolation: the cmd1-branch leaf flag is not a flag of the cmd2-branch leaf.
	var in3 cmd2Inputs
	if err := NewParser().Parse(NewContextFor(def, []string{"cmd2", "cmd1", "--leaf1", "X"}), &in3); err == nil || !strings.Contains(err.Error(), `unknown flag "--leaf1"`) {
		t.Errorf("cross-branch flag should be unknown under cmd2 cmd1, got: %v", err)
	}
}

// The structs below mirror the shape the generated cmd package emits for a
// root command "app" with a sub-command "run".

type appFlags struct {
	Verbose bool `rotini:"verbose"`
}

// TestParse_rejectsTooManyPositionals pins command-level argument-count validation:
// with no variadic argument to absorb them, extra positionals are a usage error rather
// than silently dropped — both for a command that declares fewer args and for one that
// declares none.
func TestParse_rejectsTooManyPositionals(t *testing.T) {
	def := Definition{
		Name: "app", Handler: "App",
		Commands: []CommandDef{
			{Name: "greet", Handler: "AppGreet", Arguments: []ArgDef{{Name: "name", Type: "string"}}},
			{Name: "ping", Handler: "AppPing"},
		},
	}
	type tmpInputs struct {
		App   struct{ Flags, Arguments struct{} }
		Greet struct {
			Flags     struct{}
			Arguments struct {
				Name string `rotini:"name"`
			}
		}
		Ping struct{ Flags, Arguments struct{} }
	}
	parse := func(argv ...string) error {
		var in tmpInputs
		return NewParser().Parse(NewContextFor(def, argv), &in)
	}

	if err := parse("greet", "alice"); err != nil {
		t.Fatalf("one declared arg should parse: %v", err)
	}
	if err := parse("greet", "alice", "bob"); err == nil || !strings.Contains(err.Error(), "accepts at most 1 argument") {
		t.Fatalf("two args for a one-arg command: want too-many error, got %v", err)
	}
	if err := parse("ping", "oops"); err == nil || !strings.Contains(err.Error(), "takes no arguments") {
		t.Fatalf("positional for a no-arg command: want takes-no-arguments error, got %v", err)
	}
	if err := parse("ping"); err != nil {
		t.Fatalf("no-arg command with no positionals should parse: %v", err)
	}
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
	chain := make([]ResolvedCommand, len(store.scopes))
	v := reflect.ValueOf(&out).Elem()
	_ = bindInputs(v, store, chain, frameAnchor(v, chain, false))
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

// Typed-slice shapes: array flags/arguments whose items: declares a non-string
// element type (W4-S1) — generated as []int / []time.Duration / named-string
// slices, each element coerced individually.
type tsFlags struct {
	Ports  []int           `rotini:"ports"`
	Waits  []time.Duration `rotini:"waits"`
	Levels []logLevel      `rotini:"levels"` // named string kind — must not panic
}
type logLevel string
type tsArgs struct {
	Counts []int `rotini:"counts"` // typed variadic
}
type tsInputs struct {
	App struct {
		Flags     tsFlags
		Arguments tsArgs
	}
}

func tsDef() Definition {
	return Definition{
		Name: "app", Handler: "App",
		Flags: []FlagDef{
			{Name: "ports", Identifiers: []string{"--port"}, Type: "[]int"},
			{Name: "waits", Identifiers: []string{"--wait"}, Type: "[]time.Duration"},
			{Name: "levels", Identifiers: []string{"--level"}, Type: "[]logLevel"},
		},
		Arguments: []ArgDef{{Name: "counts", Type: "[]int", Variadic: true}},
	}
}

func TestParse_typedSlices(t *testing.T) {
	rtx := NewContextFor(tsDef(), []string{
		"--port", "80", "--port", "443",
		"--wait", "1s", "--wait", "2m",
		"--level", "info", "--level", "warn",
		"1", "2", "3",
	})
	var in tsInputs
	if err := NewParser().Parse(rtx, &in); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := in.App.Flags.Ports; len(got) != 2 || got[0] != 80 || got[1] != 443 {
		t.Errorf("Ports = %v, want [80 443]", got)
	}
	if got := in.App.Flags.Waits; len(got) != 2 || got[0] != time.Second || got[1] != 2*time.Minute {
		t.Errorf("Waits = %v, want [1s 2m]", got)
	}
	if got := in.App.Flags.Levels; len(got) != 2 || got[0] != "info" || got[1] != "warn" {
		t.Errorf("Levels = %v, want [info warn]", got)
	}
	if got := in.App.Arguments.Counts; len(got) != 3 || got[0] != 1 || got[2] != 3 {
		t.Errorf("variadic Counts = %v, want [1 2 3]", got)
	}

	// An element that doesn't parse is a usage error naming the flag, not a zeroed slice.
	rtx2 := NewContextFor(tsDef(), []string{"--port", "80", "--port", "oops"})
	var in2 tsInputs
	err := NewParser().Parse(rtx2, &in2)
	if err == nil || !strings.Contains(err.Error(), "--port") || !strings.Contains(err.Error(), "oops") {
		t.Errorf("Parse(bad slice element) = %v, want a usage error naming --port and the value", err)
	}
}

// ── the type conformance matrix (W4-S4) ─────────────────────────────────────
//
// One regression wall for "spec types to Go types is solid": every type in the
// spec vocabulary, exercised through argv text → coerce → generated field. The
// spec-alias half (boolean→bool, array+items→[]T, …) is pinned by the internal
// package's TestJSONSchemaTypeToGo/TestGetSchemaType_arrayItems; this is the
// runtime half over the resulting FlagDef.Type + field. Channel boundaries:
// argv accepts every type below; env/config are scalar-only (recon coerces
// them — binder_test pins int/string/secret/constraints; repeatable
// arrays/maps have no env/config form); stdin is a document decode, not a
// per-value coercion (binder_test pins yaml/json + schema validation).

// upperString is the matrix's custom TextUnmarshaler: parse = uppercase,
// rejecting "bad" — proving the contract's parse+validate hook both ways.
type upperString string

func (u *upperString) UnmarshalText(text []byte) error {
	if string(text) == "bad" {
		return fmt.Errorf("upperString rejects %q", text)
	}
	*u = upperString(strings.ToUpper(string(text)))
	return nil
}

type matrixFlags struct {
	B   bool              `rotini:"b"`
	I   int               `rotini:"i"`
	F   float64           `rotini:"f"`
	S   string            `rotini:"s"`
	A   []string          `rotini:"a"`
	AI  []int             `rotini:"ai"`
	AD  []time.Duration   `rotini:"ad"`
	M   map[string]any    `rotini:"m"`
	MS  map[string]string `rotini:"ms"`
	MI  map[string]int    `rotini:"mi"`
	D   time.Duration     `rotini:"d"`
	T   time.Time         `rotini:"t"`
	U   upperString       `rotini:"u"`
	NI  *int              `rotini:"ni"`
	E   string            `rotini:"e"`
	Def string            `rotini:"def"`
}
type matrixInputs struct {
	App struct {
		Flags     matrixFlags
		Arguments struct{}
	}
}

func matrixDef() Definition {
	return Definition{
		Name: "app", Handler: "App",
		Flags: []FlagDef{
			{Name: "b", Identifiers: []string{"--b"}, Type: "bool"},
			{Name: "i", Identifiers: []string{"--i"}, Type: "int"},
			{Name: "f", Identifiers: []string{"--f"}, Type: "float64"},
			{Name: "s", Identifiers: []string{"--s"}, Type: "string"},
			{Name: "a", Identifiers: []string{"--a"}, Type: "[]string"},
			{Name: "ai", Identifiers: []string{"--ai"}, Type: "[]int"},
			{Name: "ad", Identifiers: []string{"--ad"}, Type: "[]time.Duration"},
			{Name: "m", Identifiers: []string{"--m"}, Type: "map[string]any"},
			{Name: "ms", Identifiers: []string{"--ms"}, Type: "map[string]string"},
			{Name: "mi", Identifiers: []string{"--mi"}, Type: "map[string]int"},
			{Name: "d", Identifiers: []string{"--d"}, Type: "time.Duration"},
			{Name: "t", Identifiers: []string{"--t"}, Type: "time.Time"},
			{Name: "u", Identifiers: []string{"--u"}, Type: "upperString"},
			{Name: "ni", Identifiers: []string{"--ni"}, Type: "*int"},
			{Name: "e", Identifiers: []string{"--e"}, Type: "string", Enum: []string{"red", "green"}},
			{Name: "def", Identifiers: []string{"--def"}, Type: "string", Default: "fallback"},
		},
	}
}

func TestTypeConformanceMatrix(t *testing.T) {
	parse := func(t *testing.T, argv ...string) (matrixInputs, error) {
		t.Helper()
		var in matrixInputs
		err := NewParser().Parse(NewContextFor(matrixDef(), argv), &in)
		return in, err
	}

	t.Run("every type parses from argv text", func(t *testing.T) {
		in, err := parse(t,
			"--b", "--i", "42", "--f", "2.5", "--s", "hi",
			"--a", "x", "--a", "y",
			"--ai", "1", "--ai", "2",
			"--ad", "1s", "--ad", "2m",
			"--m", "k=v", "--ms", "k=v", "--mi", "k=7",
			"--d", "1m30s", "--t", "2026-05-22T00:00:00Z",
			"--u", "loud", "--ni", "4", "--e", "red",
		)
		if err != nil {
			t.Fatalf("Parse: %v", err)
		}
		ni := 4
		want := matrixFlags{
			B: true, I: 42, F: 2.5, S: "hi",
			A:   []string{"x", "y"},
			AI:  []int{1, 2},
			AD:  []time.Duration{time.Second, 2 * time.Minute},
			M:   map[string]any{"k": "v"},
			MS:  map[string]string{"k": "v"},
			MI:  map[string]int{"k": 7},
			D:   90 * time.Second,
			T:   time.Date(2026, 5, 22, 0, 0, 0, 0, time.UTC),
			U:   "LOUD",
			NI:  &ni,
			E:   "red",
			Def: "fallback", // not supplied — the declared default fills it
		}
		if !reflect.DeepEqual(in.App.Flags, want) {
			t.Errorf("matrix mismatch:\n got %+v\nwant %+v", in.App.Flags, want)
		}
	})

	t.Run("nothing supplied: defaults fill, nullable stays nil, rest zero", func(t *testing.T) {
		in, err := parse(t)
		if err != nil {
			t.Fatalf("Parse: %v", err)
		}
		if in.App.Flags.Def != "fallback" {
			t.Errorf("Def = %q, want the declared default", in.App.Flags.Def)
		}
		if in.App.Flags.NI != nil {
			t.Errorf("NI = %v, want nil — a nullable input not provided stays nil", in.App.Flags.NI)
		}
		if in.App.Flags.I != 0 || in.App.Flags.A != nil || in.App.Flags.M != nil {
			t.Errorf("unsupplied fields should stay zero: %+v", in.App.Flags)
		}
	})

	// Per-type rejection: each bad value is a usage error naming the flag.
	// (A bool's value is inline-only — "--b yep" parses --b true plus the
	// positional "yep" by design — so the bad-bool spelling is the = form.)
	rejects := []struct {
		argv []string
		name string
	}{
		{[]string{"--b=yep"}, "--b"},
		{[]string{"--i", "4.5"}, "--i"},
		{[]string{"--f", "x"}, "--f"},
		{[]string{"--ai", "x"}, "--ai"},
		{[]string{"--ad", "fast"}, "--ad"},
		{[]string{"--mi", "k=x"}, "--mi"},
		{[]string{"--d", "90"}, "--d"},
		{[]string{"--t", "yesterday"}, "--t"},
		{[]string{"--u", "bad"}, "--u"},  // the type's own UnmarshalText error
		{[]string{"--e", "blue"}, "--e"}, // outside the enum
		{[]string{"--ni", "x"}, "--ni"},  // bad value through the nullable pointer
	}
	for _, r := range rejects {
		t.Run("rejects "+strings.Join(r.argv, " "), func(t *testing.T) {
			_, err := parse(t, r.argv...)
			if err == nil || !strings.Contains(err.Error(), r.name) {
				t.Errorf("Parse(%v) = %v, want a usage error naming %s", r.argv, err, r.name)
			}
		})
	}
}

// from: shapes (spec from:): a token flag resolving @file values (the secret
// token-file idiom), a payload flag resolving the bare "-" stdin sentinel, and
// a plain flag where both stay literal.
type fromInputs struct {
	App struct {
		Flags struct {
			Token   string `rotini:"token"`
			Payload string `rotini:"payload"`
			Plain   string `rotini:"plain"`
		}
		Arguments struct{}
	}
}

func fromDef() Definition {
	return Definition{
		Name: "app", Handler: "App",
		Flags: []FlagDef{
			{Name: "token", Identifiers: []string{"--token"}, Type: "string", Secret: true, From: []string{"value", "file"}},
			{Name: "payload", Identifiers: []string{"-f", "--payload"}, Type: "string", From: []string{"value", "stdin"}},
			{Name: "plain", Identifiers: []string{"--plain"}, Type: "string"},
		},
	}
}

func TestParse_fromFile(t *testing.T) {
	tokenFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenFile, []byte("sk-12345\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var in fromInputs
	if err := NewParser().Parse(NewContextFor(fromDef(), []string{"--token", "@" + tokenFile}), &in); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if in.App.Flags.Token != "sk-12345" {
		t.Errorf("Token = %q, want the file's trimmed contents", in.App.Flags.Token)
	}

	// The inline form resolves too.
	var in2 fromInputs
	if err := NewParser().Parse(NewContextFor(fromDef(), []string{"--token=@" + tokenFile}), &in2); err != nil {
		t.Fatalf("Parse(inline): %v", err)
	}
	if in2.App.Flags.Token != "sk-12345" {
		t.Errorf("inline Token = %q, want the file's contents", in2.App.Flags.Token)
	}

	// An unreadable file is a usage error naming the flag and the @path — never
	// a silent literal.
	var in3 fromInputs
	err := NewParser().Parse(NewContextFor(fromDef(), []string{"--token", "@/nonexistent/nope"}), &in3)
	if err == nil || !strings.Contains(err.Error(), "--token") || !strings.Contains(err.Error(), "@/nonexistent/nope") {
		t.Errorf("Parse(missing file) = %v, want a cannot-read usage error", err)
	}

	// STDIN-04 conformance: a plain path (no '@') stays literal even on a
	// from:-enabled flag — the handler opens it itself.
	var inPath fromInputs
	if err := NewParser().Parse(NewContextFor(fromDef(), []string{"--token", tokenFile}), &inPath); err != nil {
		t.Fatalf("Parse(plain path): %v", err)
	}
	if inPath.App.Flags.Token != tokenFile {
		t.Errorf("Token = %q, want the literal path %q", inPath.App.Flags.Token, tokenFile)
	}

	// Without from: file, '@' is an ordinary character.
	var in4 fromInputs
	if err := NewParser().Parse(NewContextFor(fromDef(), []string{"--plain", "@literal"}), &in4); err != nil {
		t.Fatalf("Parse(plain @): %v", err)
	}
	if in4.App.Flags.Plain != "@literal" {
		t.Errorf("Plain = %q, want the literal @ value (from: is opt-in)", in4.App.Flags.Plain)
	}
}

func TestParse_fromStdin(t *testing.T) {
	rtx := NewContextFor(fromDef(), []string{"-f", "-"})
	rtx.Stdin = strings.NewReader("kind: Widget\n")
	var in fromInputs
	if err := NewParser().Parse(rtx, &in); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if in.App.Flags.Payload != "kind: Widget" {
		t.Errorf("Payload = %q, want the trimmed piped text", in.App.Flags.Payload)
	}

	// Giving "-" demands a pipe: empty stdin is a usage error (the required-
	// stdin error path).
	rtx2 := NewContextFor(fromDef(), []string{"-f", "-"})
	rtx2.Stdin = strings.NewReader("")
	var in2 fromInputs
	err := NewParser().Parse(rtx2, &in2)
	if err == nil || !strings.Contains(err.Error(), "stdin is empty") {
		t.Errorf("Parse(empty stdin) = %v, want a stdin-is-empty usage error", err)
	}

	// Without from: stdin, "-" is an ordinary value.
	rtx3 := NewContextFor(fromDef(), []string{"--plain", "-"})
	var in3 fromInputs
	if err := NewParser().Parse(rtx3, &in3); err != nil {
		t.Fatalf("Parse(plain -): %v", err)
	}
	if in3.App.Flags.Plain != "-" {
		t.Errorf("Plain = %q, want the literal -", in3.App.Flags.Plain)
	}
}

// Dotted-key shapes (spec dotted_keys): a map[string]any flag whose key=value
// keys are '.'-separated paths into nested maps, next to a plain map flag where
// '.' stays a literal key character.
type dkInputs struct {
	App struct {
		Flags struct {
			Set    map[string]any    `rotini:"set"`
			Labels map[string]string `rotini:"labels"`
		}
		Arguments struct{}
	}
}

func dkDef() Definition {
	return Definition{
		Name: "app", Handler: "App",
		Flags: []FlagDef{
			{Name: "set", Identifiers: []string{"--set"}, Type: "map[string]any", DottedKeys: true},
			{Name: "labels", Identifiers: []string{"--label"}, Type: "map[string]string"},
		},
	}
}

func TestParse_dottedKeys(t *testing.T) {
	parse := func(t *testing.T, argv ...string) (dkInputs, error) {
		t.Helper()
		var in dkInputs
		err := NewParser().Parse(NewContextFor(dkDef(), argv), &in)
		return in, err
	}

	in, err := parse(t,
		"--set", "image.tag=v2", "--set", "image.pull=Always", "--set", "replicas=3",
		"--label", "team.name=core",
	)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	image, ok := in.App.Flags.Set["image"].(map[string]any)
	if !ok || image["tag"] != "v2" || image["pull"] != "Always" {
		t.Errorf("Set[image] = %#v, want nested {tag: v2, pull: Always}", in.App.Flags.Set["image"])
	}
	if in.App.Flags.Set["replicas"] != "3" {
		t.Errorf("Set[replicas] = %#v, want %q", in.App.Flags.Set["replicas"], "3")
	}
	if in.App.Flags.Labels["team.name"] != "core" {
		t.Errorf("plain map = %v, want the literal key team.name (dotted_keys is opt-in)", in.App.Flags.Labels)
	}

	// Later assignments overwrite whatever sits at their path — both a scalar
	// replaced by a subtree and a subtree replaced by a scalar.
	if in, err := parse(t, "--set", "a=1", "--set", "a.b=2"); err != nil {
		t.Errorf("Parse: %v", err)
	} else if sub, ok := in.App.Flags.Set["a"].(map[string]any); !ok || sub["b"] != "2" {
		t.Errorf("scalar→subtree: Set[a] = %#v, want map[b:2]", in.App.Flags.Set["a"])
	}
	if in, err := parse(t, "--set", "a.b=2", "--set", "a=1"); err != nil {
		t.Errorf("Parse: %v", err)
	} else if in.App.Flags.Set["a"] != "1" {
		t.Errorf("subtree→scalar: Set[a] = %#v, want %q", in.App.Flags.Set["a"], "1")
	}

	// An empty path segment is a usage error naming the flag.
	if _, err := parse(t, "--set", "a..b=1"); err == nil || !strings.Contains(err.Error(), "--set") || !strings.Contains(err.Error(), "empty segment") {
		t.Errorf("Parse(a..b=1) = %v, want an empty-segment usage error naming --set", err)
	}
	// The key=value shape requirement still applies to dotted maps.
	if _, err := parse(t, "--set", "novalue"); err == nil || !strings.Contains(err.Error(), "key=value") {
		t.Errorf("Parse(novalue) = %v, want the key=value usage error", err)
	}
}

func TestCoerce_unsupportedTypeIsLoud(t *testing.T) {
	// A type coerce has no rule for must error (the TextUnmarshaler contract),
	// never silently zero the field.
	var s struct{ X struct{ A int } }
	err := coerce(reflect.ValueOf(&s.X).Elem(), []string{"v"})
	if err == nil || !strings.Contains(err.Error(), "encoding.TextUnmarshaler") {
		t.Errorf("coerce(plain struct) = %v, want the TextUnmarshaler contract error", err)
	}

	// An `any` field stores the raw string.
	var a any
	if err := coerce(reflect.ValueOf(&a).Elem(), []string{"raw"}); err != nil || a != "raw" {
		t.Errorf("coerce(any) = %v / %v, want raw stored", a, err)
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

// ── ParseKind ───────────────────────────────────────────────────.

// TestParseError_kindPerPath drives each parse/validate failure path and asserts
// the *ParseError carries the right ParseKind — so a funnel can branch on Kind
// instead of matching the message. Every kind still classifies as CategoryUsage
// (even the API-misuse Internal kind is a usage-shaped *ParseError).
func TestParseError_kindPerPath(t *testing.T) {
	min1 := Ptr(1.0)
	cases := []struct {
		name string
		def  Definition
		argv []string
		out  any
		want ParseKind
	}{
		{
			name: "unknown flag",
			def:  Definition{Name: "app", Handler: "App"},
			argv: []string{"--nope"},
			want: ParseKindUnknownFlag,
		},
		{
			name: "unknown command (stray positional on a branch)",
			def:  Definition{Name: "app", Handler: "App", Commands: []CommandDef{{Name: "run", Handler: "Run"}}},
			argv: []string{"ru"},
			want: ParseKindUnknownCommand,
		},
		{
			name: "flag needs a value",
			def:  Definition{Name: "app", Handler: "App", Flags: []FlagDef{{Name: "count", Identifiers: []string{"--count"}, Type: "int"}}},
			argv: []string{"--count"},
			want: ParseKindNeedsValue,
		},
		{
			name: "value on a count flag (invalid value)",
			def:  Definition{Name: "app", Handler: "App", Flags: []FlagDef{{Name: "c", Identifiers: []string{"--c"}, Type: "count"}}},
			argv: []string{"--c=5"},
			want: ParseKindInvalidValue,
		},
		{
			name: "enum violation",
			def:  Definition{Name: "app", Handler: "App", Flags: []FlagDef{{Name: "level", Identifiers: []string{"--level"}, Type: "string", Enum: []string{"low", "high"}}}},
			argv: []string{"--level", "medium"},
			want: ParseKindEnumViolation,
		},
		{
			name: "constraint violation (minimum)",
			def:  Definition{Name: "app", Handler: "App", Flags: []FlagDef{{Name: "n", Identifiers: []string{"--n"}, Type: "int", Minimum: min1}}},
			argv: []string{"--n", "0"},
			want: ParseKindConstraintViolation,
		},
		{
			name: "missing required",
			def:  Definition{Name: "app", Handler: "App", Flags: []FlagDef{{Name: "token", Identifiers: []string{"--token"}, Type: "string", Required: true}}},
			argv: []string{},
			want: ParseKindMissingRequired,
		},
		{
			name: "command takes no arguments",
			def:  Definition{Name: "app", Handler: "App"},
			argv: []string{"stray"},
			want: ParseKindNoArguments,
		},
		{
			name: "too many arguments",
			def:  Definition{Name: "app", Handler: "App", Arguments: []ArgDef{{Name: "name", Type: "string"}}},
			argv: []string{"a", "b"},
			want: ParseKindTooManyArguments,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rtx := NewContextFor(tc.def, tc.argv)
			out := tc.out
			if out == nil {
				out = &struct{}{}
			}
			err := NewParser().Parse(rtx, out)
			if err == nil {
				t.Fatalf("Parse(%v) = nil, want a %s error", tc.argv, tc.want)
			}
			var pe *ParseError
			if !errors.As(err, &pe) {
				t.Fatalf("err is not a *ParseError: %T (%v)", err, err)
			}
			if pe.Kind != tc.want {
				t.Errorf("Kind = %s, want %s (msg: %q)", pe.Kind, tc.want, pe.Msg)
			}
			// Every parse failure is usage-categorized regardless of kind.
			if CategoryOf(err) != CategoryUsage {
				t.Errorf("CategoryOf = %v, want usage", CategoryOf(err))
			}
		})
	}
}

// TestParseError_internalKind: a parser API misuse (a non-pointer out) is the
// Internal kind — still a usage-shaped *ParseError.
func TestParseError_internalKind(t *testing.T) {
	rtx := NewContextFor(Definition{Name: "app", Handler: "App"}, nil)
	err := NewParser().Parse(rtx, 42) // not a pointer
	var pe *ParseError
	if !errors.As(err, &pe) {
		t.Fatalf("err is not a *ParseError: %T (%v)", err, err)
	}
	if pe.Kind != ParseKindInternal {
		t.Errorf("Kind = %s, want internal", pe.Kind)
	}
}

// TestParseKind_String pins the stable labels (and the zero-value default).
func TestParseKind_String(t *testing.T) {
	if got := ParseKindUnspecified.String(); got != "unspecified" {
		t.Errorf("ParseKindUnspecified = %q, want unspecified", got)
	}
	if got := ParseKindEnumViolation.String(); got != "enum-violation" {
		t.Errorf("ParseKindEnumViolation = %q, want enum-violation", got)
	}
}

// ── usage rendering ─────────────────────────────────────────────.

// rtk's parse/bind failures (usageError) carry the usage category, so a single
// CategoryOf call in an OnError funnel classifies them as the end-user's fault.
func TestUsageError_categorizedAsUsage(t *testing.T) {
	rtx := NewContextFor(Definition{Name: "app", Handler: "App"}, nil)

	// A non-pointer out is the simplest parse-time usageError.
	err := NewParser().Parse(rtx, 42)
	if err == nil {
		t.Fatal("expected a usage error from Parse with a non-pointer out")
	}
	if got := CategoryOf(err); got != CategoryUsage {
		t.Errorf("CategoryOf(parse error) = %v, want usage", got)
	}
	if !errors.Is(err, ErrUsage) {
		t.Error("a parse usageError should match ErrUsage")
	}
	if errors.Is(err, ErrInternal) {
		t.Error("a usage error must not match ErrInternal")
	}
}

// negatableDef is a command with one negatable bool flag defaulting to on, plus a genuinely
// declared --no-cache on a DIFFERENT flag, so the precedence between a declared identifier
// and a derived negated one is exercised rather than assumed.
func negatableDef() Definition {
	return Definition{
		Name: "app", Handler: "App",
		Flags: []FlagDef{
			{Name: "color", Identifiers: []string{"--color", "-c"}, Type: "bool", Negatable: true, Default: "true"},
			{Name: "verify", Identifiers: []string{"--verify"}, Type: "bool", Negatable: true},
		},
	}
}

type negatableInputs struct {
	App struct {
		Flags struct {
			Color  bool `rotini:"color"`
			Verify bool `rotini:"verify"`
		}
		Arguments struct{}
	}
}

// TestParse_negatableBool covers the direction a plain bool cannot express: turning something
// off for one run when a default, a config file or an environment variable already turned it
// on. Without it, an author can only ever say "on".
func TestParse_negatableBool(t *testing.T) {
	cases := []struct {
		name         string
		argv         []string
		color        bool
		verify       bool
		wantErr      string
		wantErrToken string
	}{
		{name: "the default stands", argv: nil, color: true},
		{name: "the positive form", argv: []string{"--color"}, color: true},
		{name: "the negated form sets false", argv: []string{"--no-color"}, color: false},
		{name: "negating a flag that was off leaves it off", argv: []string{"--no-verify"}, color: true},
		{name: "positive then negated: last wins", argv: []string{"--color", "--no-color"}, color: false},
		{name: "negated then positive: last wins", argv: []string{"--no-color", "--color"}, color: true},
		{name: "the short form has no negated spelling", argv: []string{"-c"}, color: true},
		{
			// Short flags have no negated spelling, so "-no-c" is read as a POSIX
			// cluster (-n -o -c) and fails on the first unknown letter. The point is
			// that rotini does not invent one, not which token the cluster blames.
			name:    "a negated short form is not invented",
			argv:    []string{"-no-c"},
			wantErr: "unknown flag",
		},
		{
			name:    "the negated form takes no value",
			argv:    []string{"--no-color=true"},
			wantErr: "negated form and takes no value",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var in negatableInputs
			rtx := NewContextFor(negatableDef(), tc.argv)
			err := NewParser().Parse(rtx, &in)

			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error = %v, want containing %q", err, tc.wantErr)
				}
				if tc.wantErrToken != "" {
					var pe *ParseError
					if !errors.As(err, &pe) || pe.Token != tc.wantErrToken {
						t.Errorf("ParseError token = %v, want %q", err, tc.wantErrToken)
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if in.App.Flags.Color != tc.color {
				t.Errorf("color = %v, want %v", in.App.Flags.Color, tc.color)
			}
			if in.App.Flags.Verify != tc.verify {
				t.Errorf("verify = %v, want %v", in.App.Flags.Verify, tc.verify)
			}
		})
	}
}

// TestParse_declaredIdentifierBeatsNegatedForm: an author who genuinely declares --no-cache
// keeps it, even when another flag's negatable would derive the same token. Silently shadowing
// a declared identifier is the one outcome that must not happen.
func TestParse_declaredIdentifierBeatsNegatedForm(t *testing.T) {
	def := Definition{
		Name: "app", Handler: "App",
		Flags: []FlagDef{
			{Name: "cache", Identifiers: []string{"--cache"}, Type: "bool", Negatable: true},
			{Name: "nocache", Identifiers: []string{"--no-cache"}, Type: "string"},
		},
	}
	var in struct {
		App struct {
			Flags struct {
				Cache   bool   `rotini:"cache"`
				Nocache string `rotini:"nocache"`
			}
			Arguments struct{}
		}
	}
	rtx := NewContextFor(def, []string{"--no-cache", "hello"})
	if err := NewParser().Parse(rtx, &in); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if in.App.Flags.Nocache != "hello" {
		t.Errorf("the declared --no-cache did not win: nocache=%q cache=%v", in.App.Flags.Nocache, in.App.Flags.Cache)
	}
}

// TestParse_negatedFormIsSuggestable: the negated spelling joins the flag vocabulary a
// ParseError carries, so a Suggestor can offer it for a near miss.
func TestParse_negatedFormIsSuggestable(t *testing.T) {
	var in negatableInputs
	rtx := NewContextFor(negatableDef(), []string{"--no-colour"})
	err := NewParser().Parse(rtx, &in)

	var pe *ParseError
	if !errors.As(err, &pe) {
		t.Fatalf("error = %v, want a *ParseError", err)
	}
	if !slices.Contains(pe.Candidates, "--no-color") {
		t.Errorf("candidates %v do not include the negated form", pe.Candidates)
	}
	if got := NewSuggestor().Suggest(pe.Token, pe.Candidates); !slices.Contains(got, "--no-color") {
		t.Errorf("suggestions %v do not offer --no-color for %q", got, pe.Token)
	}
}

// TestParse_pathTypes covers existingfile / existingdir: a value that is not there, or is the
// wrong kind of thing, is a usage error at PARSE time naming the flag the user typed —
// instead of an *os.PathError surfacing three layers into a handler, naming only a path.
func TestParse_pathTypes(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "real.txt")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(dir, "absent.txt")

	def := Definition{
		Name: "app", Handler: "App",
		Flags: []FlagDef{
			{Name: "config", Identifiers: []string{"--config"}, Type: "existingfile"},
			{Name: "out", Identifiers: []string{"--out"}, Type: "existingdir"},
		},
	}
	type inputs struct {
		App struct {
			Flags struct {
				Config string `rotini:"config"`
				Out    string `rotini:"out"`
			}
			Arguments struct{}
		}
	}

	cases := []struct {
		name    string
		argv    []string
		wantErr string
	}{
		{name: "an existing file passes", argv: []string{"--config", file}},
		{name: "an existing directory passes", argv: []string{"--out", dir}},
		{name: "a missing file is named", argv: []string{"--config", missing}, wantErr: "no such file"},
		{name: "a missing directory says directory", argv: []string{"--out", missing}, wantErr: "no such directory"},
		{name: "a directory is not a file", argv: []string{"--config", dir}, wantErr: "is a directory, not a file"},
		{name: "a file is not a directory", argv: []string{"--out", file}, wantErr: "is not a directory"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var in inputs
			err := NewParser().Parse(NewContextFor(def, tc.argv), &in)

			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("parse: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %v, want containing %q", err, tc.wantErr)
			}
			// It is the user's mistake, so it classifies as usage — not internal.
			if CategoryOf(err) != CategoryUsage {
				t.Errorf("category = %v, want CategoryUsage", CategoryOf(err))
			}
			// The message names the flag, so the user knows which one to fix.
			var pe *ParseError
			if !errors.As(err, &pe) || !strings.Contains(pe.Msg, "--") {
				t.Errorf("message %q does not name the flag", err)
			}
		})
	}
}

// TestParse_pathTypeKeepsStringBounds: a path is still a string, so its declared pattern and
// length bounds apply. Skipping them would silently ignore a declared constraint, which is
// the single failure mode rotini's validation exists to prevent.
func TestParse_pathTypeKeepsStringBounds(t *testing.T) {
	dir := t.TempDir()
	yaml := filepath.Join(dir, "conf.yaml")
	text := filepath.Join(dir, "conf.txt")
	for _, p := range []string{yaml, text} {
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	def := Definition{
		Name: "app", Handler: "App",
		Flags: []FlagDef{{
			Name: "config", Identifiers: []string{"--config"}, Type: "existingfile",
			Constraints: Constraints{Pattern: `\.ya?ml$`},
		}},
	}
	type inputs struct {
		App struct {
			Flags struct {
				Config string `rotini:"config"`
			}
			Arguments struct{}
		}
	}

	var ok inputs
	if err := NewParser().Parse(NewContextFor(def, []string{"--config", yaml}), &ok); err != nil {
		t.Fatalf("a .yaml path matching the pattern: %v", err)
	}

	var bad inputs
	err := NewParser().Parse(NewContextFor(def, []string{"--config", text}), &bad)
	if err == nil {
		t.Fatal("a path that exists but violates the pattern was accepted — the constraint was ignored")
	}
	if !strings.Contains(err.Error(), `must match \.ya?ml$`) {
		t.Errorf("error = %v, want it to quote the pattern it violated", err)
	}
}

// TestParse_multiValueDefaults covers the capability a repeatable input did not have: a
// default with more than one value in it.
//
// A default is carried as argv occurrences, and FlagDef.Default is ONE string — so before
// Defaults, `default: [a, b]` had no representation. It stringified into a single mangled
// element (`"[a b c]"`, Go's %v), which is why lintDefaultScalar rejected it outright and told
// the author to seed the value in the handler instead. That advice worked and pushed a
// declared default back into hand-written Go, which is the thing declaring inputs exists to
// avoid.
func TestParse_multiValueDefaults(t *testing.T) {
	t.Parallel()
	def := Definition{
		Name: "app", Handler: "App",
		Flags: []FlagDef{
			{Name: "tag", Identifiers: []string{"--tag"}, Type: "[]string", Defaults: []string{"latest", "stable"}},
			{Name: "label", Identifiers: []string{"--label"}, Type: "map[string]string", Defaults: []string{"team=core", "tier=1"}},
			{Name: "port", Identifiers: []string{"--port"}, Type: "[]int", Defaults: []string{"80", "443"}},
			{Name: "plain", Identifiers: []string{"--plain"}, Type: "string", Default: "one"},
		},
	}
	type inputs struct {
		App struct {
			Flags struct {
				Tag   []string          `rotini:"tag"`
				Label map[string]string `rotini:"label"`
				Port  []int             `rotini:"port"`
				Plain string            `rotini:"plain"`
			}
			Arguments struct{}
		}
	}

	t.Run("every element is seeded when the flag is unset", func(t *testing.T) {
		t.Parallel()
		var in inputs
		if err := NewParser().Parse(NewContextFor(def, nil), &in); err != nil {
			t.Fatal(err)
		}
		f := in.App.Flags
		if !slices.Equal(f.Tag, []string{"latest", "stable"}) {
			t.Errorf("Tag = %q, want [latest stable]", f.Tag)
		}
		if !slices.Equal(f.Port, []int{80, 443}) {
			t.Errorf("Port = %v, want [80 443] — elements coerce through the element type", f.Port)
		}
		if f.Label["team"] != "core" || f.Label["tier"] != "1" {
			t.Errorf("Label = %v, want team=core tier=1", f.Label)
		}
		if f.Plain != "one" {
			t.Errorf("Plain = %q — a scalar default still works", f.Plain)
		}
	})

	t.Run("a supplied value REPLACES the default rather than adding to it", func(t *testing.T) {
		t.Parallel()
		var in inputs
		rtx := NewContextFor(def, []string{"--tag", "mine"})
		if err := NewParser().Parse(rtx, &in); err != nil {
			t.Fatal(err)
		}
		// Merging would make the default impossible to opt out of, which is the whole
		// reason a default is a fallback rather than a seed.
		if !slices.Equal(in.App.Flags.Tag, []string{"mine"}) {
			t.Errorf("Tag = %q, want [mine] — a default must not merge with a supplied value", in.App.Flags.Tag)
		}
		// ...and the flags the user did not touch still take theirs.
		if !slices.Equal(in.App.Flags.Port, []int{80, 443}) {
			t.Errorf("Port = %v, want its default", in.App.Flags.Port)
		}
	})

	t.Run("repeating the flag still accumulates", func(t *testing.T) {
		t.Parallel()
		var in inputs
		rtx := NewContextFor(def, []string{"--tag", "a", "--tag", "b"})
		if err := NewParser().Parse(rtx, &in); err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(in.App.Flags.Tag, []string{"a", "b"}) {
			t.Errorf("Tag = %q, want [a b]", in.App.Flags.Tag)
		}
	})

	t.Run("a scalar Default wins over Defaults", func(t *testing.T) {
		t.Parallel()
		// The spec cannot produce both — a default is a scalar or a list, never both —
		// but a hand-built Definition can, so the precedence is pinned rather than left
		// to map order.
		both := FlagDef{Name: "x", Default: "scalar", Defaults: []string{"a", "b"}}
		if got := flagDefaults(both); !slices.Equal(got, []string{"scalar"}) {
			t.Errorf("flagDefaults = %q, want [scalar]", got)
		}
		if got := flagDefaults(FlagDef{Name: "x"}); got != nil {
			t.Errorf("flagDefaults with no default = %q, want nil — absent stays absent", got)
		}
	})
}
