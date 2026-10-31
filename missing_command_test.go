package rotini

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

type mcHandlers struct{ seen *error }

func (h mcHandlers) App() Handler           { return mcGroup{seen: h.seen} }
func (h mcHandlers) AppConfig() Handler     { return mcGroup{seen: h.seen} }
func (mcHandlers) AppConfigGet() Handler    { return funcHandler{run: func(*Context) {}} }
func (mcHandlers) AppConfigSet() Handler    { return funcHandler{run: func(*Context) {}} }
func (mcHandlers) AppConfigSecret() Handler { return funcHandler{run: func(*Context) {}} }

// mcGroup is a group's seeded stub: it prints the help and halts with MissingCommand.
type mcGroup struct {
	NoHooks
	seen *error
}

func (g mcGroup) Run(_ context.Context, rtx *Context) {
	err := MissingCommand(rtx)
	*g.seen = err
	rtx.HaltWith(err)
}

func mcDef() Definition {
	return Definition{Name: "cfgctl", Handler: "App", Commands: []CommandDef{
		{Name: "config", Handler: "AppConfig", Aliases: []string{"cfg"}, Commands: []CommandDef{
			{Name: "get", Handler: "AppConfigGet", Aliases: []string{"g"}},
			{Name: "set", Handler: "AppConfigSet"},
			{Name: "secret", Handler: "AppConfigSecret", Hidden: true},
		}},
	}}
}

// A group run bare fails with a missing-command usage error naming its visible sub-commands,
// which the default reporter prints and exits 1 for.
func TestMissingCommand(t *testing.T) {
	var seen error
	var stderr bytes.Buffer
	code, _ := NewProgram(mcDef(), mcHandlers{seen: &seen}).WithStdout(&bytes.Buffer{}).WithStderr(&stderr).Run([]string{"cfg"})
	if code != 1 {
		t.Errorf("exit = %d, want 1", code)
	}
	if want := "Error: cfgctl config: a command is required (one of: get, set)\n"; !strings.HasSuffix(stderr.String(), want) {
		t.Errorf("stderr = %q, want it to end %q", stderr.String(), want)
	}
	var pe *ParseError
	if !errors.As(seen, &pe) || pe.Kind != ParseKindMissingCommand || pe.Command != "config" || pe.Path() != "cfgctl config" ||
		!slices.Equal(pe.Candidates, []string{"get", "g", "set"}) || CategoryOf(seen) != CategoryUsage {
		t.Errorf("err = %#v, want a missing-command usage error for config with its visible names", seen)
	}
	if ParseKindMissingCommand.String() != "missing-command" {
		t.Errorf("kind = %q", ParseKindMissingCommand.String())
	}
}

// A command with no sub-commands to name still says one is required.
func TestMissingCommand_noChildren(t *testing.T) {
	err := MissingCommand(NewContextFor(Definition{Name: "app", Handler: "App"}, nil))
	if err.Error() != "app: a command is required" || err.Candidates != nil {
		t.Errorf("err = %q, candidates %v", err, err.Candidates)
	}
}
