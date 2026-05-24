package rtk

import (
	"reflect"
	"testing"
	"time"

	"github.com/go-rotini/rotini"
)

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

func TestInputs_unresolvedReturnsZero(t *testing.T) {
	// A fresh context has no resolved command chain, so Inputs (which ignores the
	// Parse error) yields the zero value rather than panicking.
	in := Inputs[runInputs](rotini.NewRtx())
	if in.Run.Flags.Count != 0 || in.App.Flags.Verbose || in.Run.Arguments.Name != "" {
		t.Errorf("expected zero value for an unresolved context, got %+v", in)
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
