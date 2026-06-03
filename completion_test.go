package rotini

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestComplete_discoversPlugins(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"acme-foo", "acme-bar", "unrelated"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	def := Definition{
		Name: "acme", Handler: "App",
		Commands:  []CommandDef{{Name: "bar", Handler: "AcmeBar"}}, // collides with acme-bar
		Discovery: &RemoteDiscoveryDef{Prefix: "acme-", Path: dir},
	}

	got := complete(def, []string{""}, nil, nil)
	if !contains(got, "foo") {
		t.Errorf("discovered plugin %q missing from %v", "foo", got)
	}
	if !contains(got, "bar") {
		t.Errorf("declared command %q missing from %v", "bar", got)
	}
	if n := countString(got, "bar"); n != 1 {
		t.Errorf("%q should appear once (declared wins over discovered acme-bar), got %d in %v", "bar", n, got)
	}
	if contains(got, "unrelated") {
		t.Errorf("non-prefixed executable should not be discovered: %v", got)
	}

	// Hidden discovery dispatches but lists nothing.
	def.Discovery.Hidden = true
	if hidden := complete(def, []string{""}, nil, nil); contains(hidden, "foo") {
		t.Errorf("hidden discovery should not list plugins: %v", hidden)
	}
}

func countString(ss []string, want string) int {
	n := 0
	for _, s := range ss {
		if s == want {
			n++
		}
	}
	return n
}

func completionDef() Definition {
	return Definition{
		Name:    "app",
		Handler: "App",
		Flags:   []FlagDef{{Name: "verbose", Identifiers: []string{"-v", "--verbose"}, Type: "bool"}},
		Commands: []CommandDef{
			{
				Name: "build", Handler: "AppBuild", Aliases: []string{"b"},
				Flags: []FlagDef{{Name: "mode", Identifiers: []string{"-m", "--mode"}, Type: "string", Enum: []string{"debug", "release"}}},
			},
			{Name: "test", Handler: "AppTest"},
		},
		RemoteCommands: []RemoteDef{{Name: "plugin", Binary: "app-plugin"}},
	}
}

func TestComplete(t *testing.T) {
	def := completionDef()
	cases := []struct {
		name  string
		words []string
		want  []string
	}{
		{"all commands + remotes", []string{""}, []string{"b", "build", "plugin", "test"}},
		{"command prefix", []string{"bu"}, []string{"build"}},
		{"remote prefix", []string{"plu"}, []string{"plugin"}},
		{"flag names of chain (declared only, no auto -h)", []string{"build", "-"}, []string{"--mode", "--verbose", "-m", "-v"}},
		{"flag name prefix", []string{"build", "--m"}, []string{"--mode"}},
		{"enum value of preceding flag", []string{"build", "--mode", ""}, []string{"debug", "release"}},
		{"enum value prefix", []string{"build", "--mode", "r"}, []string{"release"}},
		{"no match", []string{"zzz"}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := complete(def, c.words, nil, nil)
			if len(got) == 0 && len(c.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("complete(%v) = %v, want %v", c.words, got, c.want)
			}
		})
	}
}

// TestComplete_noAutoHelpFlag pins the ethos: completion never auto-adds -h/--help.
// They appear only when the CLI declares a help flag (Pillar 1 — no framework-injected
// flags).
func TestComplete_noAutoHelpFlag(t *testing.T) {
	// No declared help flag → completion offers none.
	bare := completionDef()
	if got := complete(bare, []string{"-"}, nil, nil); contains(got, "--help") || contains(got, "-h") {
		t.Errorf("completion auto-added a help flag the CLI never declared: %v", got)
	}

	// A declared help flag is offered like any other flag.
	withHelp := completionDef()
	withHelp.Flags = append(withHelp.Flags, FlagDef{Name: "help", Identifiers: []string{"-h", "--help"}, Type: "bool"})
	got := complete(withHelp, []string{"-"}, nil, nil)
	if !contains(got, "--help") || !contains(got, "-h") {
		t.Errorf("declared help flag missing from completion: %v", got)
	}
}

// dynCompletionHandlers is an aggregate whose `build` handler supplies dynamic
// candidates for the --mode flag value (and nothing for others), exercising the
// FlagValueCompleter opt-in path.
type dynCompletionHandlers struct{}

func (dynCompletionHandlers) App() CommandHandlers      { return dynStub{} }
func (dynCompletionHandlers) AppBuild() CommandHandlers { return dynBuildHandler{} }

type dynStub struct {
	DefaultCascadingPreRun
	DefaultPreRun
	DefaultPostRun
	DefaultCascadingPostRun
}

func (dynStub) Run(ctx context.Context, rtx *Context) {}

type dynBuildHandler struct{ dynStub }

func (dynBuildHandler) CompleteFlagValue(rtx *Context, flag, partial string) []string {
	if flag == "mode" {
		return []string{"fast", "slow"} // dynamic, deliberately != the static enum
	}
	return nil // every other flag falls back to its enum
}

func dynCompletionDef() Definition {
	return Definition{
		Name: "app", Handler: "App",
		Commands: []CommandDef{{
			Name: "build", Handler: "AppBuild",
			Flags: []FlagDef{
				{Name: "mode", Identifiers: []string{"--mode"}, Type: "string", Enum: []string{"debug", "release"}},
				{Name: "level", Identifiers: []string{"--level"}, Type: "string", Enum: []string{"low", "high"}},
			},
		}},
	}
}

// TestComplete_dynamicFlagValue covers the FlagValueCompleter opt-in: a completer's
// candidates take precedence over the static enum and are prefix-filtered; a flag with
// no completer (or a nil return) falls back to the enum; and with no handlers bound the
// behavior is the pure-static enum (backward compatible).
func TestComplete_dynamicFlagValue(t *testing.T) {
	def := dynCompletionDef()
	agg := dynCompletionHandlers{}
	cases := []struct {
		name     string
		words    []string
		handlers any
		want     []string
	}{
		{"dynamic overrides enum", []string{"build", "--mode", ""}, agg, []string{"fast", "slow"}},
		{"dynamic prefix-filtered", []string{"build", "--mode", "f"}, agg, []string{"fast"}},
		{"nil return falls back to enum", []string{"build", "--level", ""}, agg, []string{"high", "low"}},
		{"no handlers → static enum", []string{"build", "--mode", ""}, nil, []string{"debug", "release"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := complete(def, c.words, c.handlers, NewContext())
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("complete(%v) = %v, want %v", c.words, got, c.want)
			}
		})
	}
}

// TestComplete_dynamicReceivesContext proves the completer is handed the resolved chain
// and the completion words via rtx, and can read services bound on the Program.
func TestComplete_dynamicReceivesContext(t *testing.T) {
	def := dynCompletionDef()
	rtx := NewContext()
	got := complete(def, []string{"build", "--mode", "x"}, dynCtxHandlers{t: t}, rtx)
	if !reflect.DeepEqual(got, []string{"x-leaf-build"}) {
		t.Errorf("completer did not observe rtx chain/args: %v", got)
	}
}

type dynCtxHandlers struct{ t *testing.T }

func (dynCtxHandlers) App() CommandHandlers        { return dynStub{} }
func (h dynCtxHandlers) AppBuild() CommandHandlers { return dynCtxBuild{t: h.t} }

type dynCtxBuild struct {
	dynStub
	t *testing.T
}

func (h dynCtxBuild) CompleteFlagValue(rtx *Context, flag, partial string) []string {
	chain := rtx.Chain()
	if len(chain) == 0 {
		h.t.Fatal("rtx.Chain() empty inside completer")
	}
	leaf := chain[len(chain)-1].Name
	return []string{partial + "-leaf-" + leaf}
}

func TestComplete_runIntercept(t *testing.T) {
	out := &bytes.Buffer{}
	p := NewProgram(completionDef(), &testHandlers{log: new([]string)}).WithArguments([]string{"__complete", "te"})
	p.stdout, p.stderr = out, &bytes.Buffer{}
	if code := p.run(p.args); code != 0 {
		t.Fatalf("__complete run = %d, want 0", code)
	}
	if got := strings.TrimSpace(out.String()); got != "test" {
		t.Errorf("__complete output = %q, want %q", got, "test")
	}
}

// TestComplete_dynamicViaProgram exercises the full path the shell scripts hit: the
// __complete intercept threads p.handlers + p.rtx into completion, so a bound handler's
// FlagValueCompleter drives the candidates printed to stdout.
func TestComplete_dynamicViaProgram(t *testing.T) {
	out := &bytes.Buffer{}
	p := NewProgram(dynCompletionDef(), dynCompletionHandlers{}).WithArguments([]string{"__complete", "build", "--mode", "s"})
	p.stdout, p.stderr = out, &bytes.Buffer{}
	if code := p.run(p.args); code != 0 {
		t.Fatalf("__complete run = %d, want 0", code)
	}
	if got := strings.TrimSpace(out.String()); got != "slow" {
		t.Errorf("dynamic __complete output = %q, want %q", got, "slow")
	}
}
