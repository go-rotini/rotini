package rtk

import (
	"strings"
	"testing"

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
	rtx := rotini.NewContext(testDef(), []string{"--verbose", "run", "alice", "x", "y", "--count", "3"})
	in, err := Parse[runInputs](rtx)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !in.App.Flags.Verbose || in.Run.Flags.Count != 3 || in.Run.Arguments.Name != "alice" {
		t.Errorf("bound inputs unexpected: %+v", in)
	}
	if r := in.Run.Arguments.Rest; len(r) != 2 || r[0] != "x" || r[1] != "y" {
		t.Errorf("variadic Rest = %v, want [x y]", r)
	}
}

func TestParse_unknownFlag(t *testing.T) {
	rtx := rotini.NewContext(testDef(), []string{"run", "--nope"})
	if _, err := Parse[runInputs](rtx); err == nil || !strings.Contains(err.Error(), `unknown flag "--nope"`) {
		t.Errorf("Parse error = %v, want unknown-flag", err)
	}
}

func TestParse_flagNeedsValue(t *testing.T) {
	rtx := rotini.NewContext(testDef(), []string{"run", "--count"})
	if _, err := Parse[runInputs](rtx); err == nil || !strings.Contains(err.Error(), "needs a value") {
		t.Errorf("Parse error = %v, want needs-a-value", err)
	}
}

func TestParse_unknownCommand(t *testing.T) {
	// "ru" is a stray positional on a branch-only root: a mistyped sub-command.
	rtx := rotini.NewContext(testDef(), []string{"ru"})
	_, err := Parse[runInputs](rtx)
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
	rtx := rotini.NewContext(def, []string{})
	_, err := Parse[struct{}](rtx)
	if err == nil || !strings.Contains(err.Error(), "missing required") || !strings.Contains(err.Error(), "--token") {
		t.Errorf("Parse error = %v, want missing-required --token", err)
	}
}

func TestParse_badEnumValue(t *testing.T) {
	def := rotini.Definition{
		Name: "app", Handler: "App",
		Flags: []rotini.FlagDef{{Name: "level", Identifiers: []string{"--level"}, Type: "string", Enum: []string{"low", "high"}}},
	}
	rtx := rotini.NewContext(def, []string{"--level", "medium"})
	_, err := Parse[struct{}](rtx)
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
	rtx := rotini.NewContext(def, []string{})
	in, err := Parse[inputs](rtx)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if in.App.Flags.Count != 9 {
		t.Errorf("default not applied: Count = %d, want 9", in.App.Flags.Count)
	}
}
