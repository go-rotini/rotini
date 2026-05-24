package rotini

import (
	"strings"
	"testing"
)

// parseCtx builds the context the runtime would hand a handler: the resolved
// command chain plus the raw argv, with the default parser bound.
func parseCtx(def Definition, argv []string) Context {
	chain, _ := resolveChain(def, argv)
	rtx := NewRtx()
	rtx.args = argv
	rtx.chain = chain
	rtx.Bind(parserKey, &Parser{})
	return rtx
}

func chainNames(chain []frame) []string {
	names := make([]string, len(chain))
	for i, f := range chain {
		names[i] = f.name
	}
	return names
}

func TestResolveChain_descendsAndSkipsFlagValues(t *testing.T) {
	// --count takes a value ("3"); it must not be mistaken for a command, and the
	// first positional ("alice") stops descent at run.
	chain, remote := resolveChain(testDef(), []string{"--verbose", "run", "--count", "3", "alice", "x"})
	if remote != nil {
		t.Fatalf("unexpected remote dispatch: %+v", remote)
	}
	if got := chainNames(chain); len(got) != 2 || got[0] != "app" || got[1] != "run" {
		t.Errorf("chain = %v, want [app run]", got)
	}
}

func TestResolveChain_flagValueNotMistakenForCommand(t *testing.T) {
	def := Definition{
		Name: "app", Handler: "App",
		Flags:    []FlagDef{{Name: "out", Identifiers: []string{"--out"}, Type: "string"}},
		Commands: []CommandDef{{Name: "run", Handler: "AppRun"}},
	}
	// "--out run" — "run" is the flag's value, so the chain must stay at the root.
	chain, _ := resolveChain(def, []string{"--out", "run"})
	if got := chainNames(chain); len(got) != 1 || got[0] != "app" {
		t.Errorf("chain = %v, want [app] (run was a flag value)", got)
	}
}

func TestResolveChain_detectsRemote(t *testing.T) {
	def := Definition{
		Name: "app", Handler: "App",
		RemoteCommands: []RemoteDef{{Name: "ext", Binary: "app-ext"}},
	}
	chain, remote := resolveChain(def, []string{"ext", "a", "b"})
	if remote == nil {
		t.Fatalf("expected a remote dispatch")
	}
	if got := chainNames(chain); len(got) != 1 || got[0] != "app" {
		t.Errorf("chain = %v, want [app]", got)
	}
	if len(remote.args) != 2 || remote.args[0] != "a" || remote.args[1] != "b" {
		t.Errorf("remote args = %v, want [a b]", remote.args)
	}
}

func TestParse_bindsInputs(t *testing.T) {
	rtx := parseCtx(testDef(), []string{"--verbose", "run", "alice", "x", "y", "--count", "3"})
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
	rtx := parseCtx(testDef(), []string{"run", "--nope"})
	if _, err := Parse[runInputs](rtx); err == nil || !strings.Contains(err.Error(), `unknown flag "--nope"`) {
		t.Errorf("Parse error = %v, want unknown-flag", err)
	}
}

func TestParse_flagNeedsValue(t *testing.T) {
	rtx := parseCtx(testDef(), []string{"run", "--count"})
	if _, err := Parse[runInputs](rtx); err == nil || !strings.Contains(err.Error(), "needs a value") {
		t.Errorf("Parse error = %v, want needs-a-value", err)
	}
}

func TestParse_unknownCommand(t *testing.T) {
	// "ru" is a stray positional on a branch-only root: a mistyped sub-command.
	rtx := parseCtx(testDef(), []string{"ru"})
	_, err := Parse[runInputs](rtx)
	if err == nil || !strings.Contains(err.Error(), `unknown command "ru"`) {
		t.Fatalf("Parse error = %v, want unknown-command", err)
	}
	if !strings.Contains(err.Error(), `Did you mean "run"?`) {
		t.Errorf("Parse error missing suggestion: %v", err)
	}
}

func TestParse_missingRequired(t *testing.T) {
	def := Definition{
		Name: "app", Handler: "App",
		Flags: []FlagDef{{Name: "token", Identifiers: []string{"--token"}, Type: "string", Required: true}},
	}
	rtx := parseCtx(def, []string{})
	_, err := Parse[struct{}](rtx)
	if err == nil || !strings.Contains(err.Error(), "missing required") || !strings.Contains(err.Error(), "--token") {
		t.Errorf("Parse error = %v, want missing-required --token", err)
	}
}

func TestParse_badEnumValue(t *testing.T) {
	def := Definition{
		Name: "app", Handler: "App",
		Flags: []FlagDef{{Name: "level", Identifiers: []string{"--level"}, Type: "string", Enum: []string{"low", "high"}}},
	}
	rtx := parseCtx(def, []string{"--level", "medium"})
	_, err := Parse[struct{}](rtx)
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
		} `rotini:"scope=app"`
	}
	rtx := parseCtx(def, []string{})
	in, err := Parse[inputs](rtx)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if in.App.Flags.Count != 9 {
		t.Errorf("default not applied: Count = %d, want 9", in.App.Flags.Count)
	}
}
