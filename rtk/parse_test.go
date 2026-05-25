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
		} `rotini:"scope=app"`
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
	App appCommandInputs `rotini:"scope=app"`
	Run runCommandInputs `rotini:"scope=run"`
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
	in := bindStore[runInputs](&parsedInputs{scopes: map[string]scopeInputs{
		"app": {flags: map[string][]string{"verbose": {"true"}}},
		"run": {
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

// A composed child's input type carries child-relative scope tags; binding must
// match by command name regardless of any parent prefix in the argv path.
func TestInputs_scopesMatchByCommandNameNotPath(t *testing.T) {
	in := bindStore[runInputs](&parsedInputs{scopes: map[string]scopeInputs{
		"app": {flags: map[string][]string{"verbose": {"true"}}},
		"run": {flags: map[string][]string{"count": {"3"}}, args: []string{"x"}},
	}})
	if !in.App.Flags.Verbose || in.Run.Flags.Count != 3 || in.Run.Arguments.Name != "x" {
		t.Errorf("composed binding failed: %+v", in)
	}
}

func TestInputs_missingFlagKeepsZero(t *testing.T) {
	in := bindStore[runInputs](&parsedInputs{scopes: map[string]scopeInputs{
		"run": {flags: map[string][]string{"count": {"5"}}}, // rate/wait/level absent
	}})
	if in.Run.Flags.Count != 5 || in.Run.Flags.Rate != 0 || in.Run.Flags.Wait != 0 || in.Run.Flags.Level != nil {
		t.Errorf("absent flags should stay zero: %+v", in.Run.Flags)
	}
}
