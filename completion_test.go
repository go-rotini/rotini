package rotini

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
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

// TestDiscoveredPlugins covers the public data feed a help renderer uses to list
// runtime plugins: discovered names minus declared collisions, nil for no/hidden
// discovery, sorted + deduped.
func TestDiscoveredPlugins(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"acme-foo", "acme-bar", "acme-zip", "unrelated"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cmd := ResolvedCommand{
		Name: "acme",
		// "bar" collides with discovered acme-bar (declared wins → excluded);
		// "ext"/"x" remote shadows nothing discovered here.
		Commands:  []CommandDef{{Name: "bar", Handler: "AcmeBar"}},
		Remotes:   []RemoteDef{{Name: "ext", Aliases: []string{"x"}, Binary: "acme-ext"}},
		Discovery: &RemoteDiscoveryDef{Prefix: "acme-", Path: dir},
	}

	got := DiscoveredPlugins(cmd)
	if !reflect.DeepEqual(got, []string{"foo", "zip"}) {
		t.Errorf("DiscoveredPlugins = %v, want [foo zip] (bar shadowed by declared command; unrelated unprefixed)", got)
	}

	// Hidden discovery → nil (the section is suppressed).
	hidden := cmd
	hd := *cmd.Discovery
	hd.Hidden = true
	hidden.Discovery = &hd
	if got := DiscoveredPlugins(hidden); got != nil {
		t.Errorf("hidden discovery should yield nil, got %v", got)
	}

	// No discovery configured → nil.
	if got := DiscoveredPlugins(ResolvedCommand{Name: "acme"}); got != nil {
		t.Errorf("no discovery should yield nil, got %v", got)
	}
}

// TestDiscoveryDiagnostics surfaces a misconfigured discovery path as data: a missing or
// unreadable author-configured `path` is reported (with the offending path + cause), a
// clean path reports nothing, and listing still works (DiscoveredPlugins never errors).
func TestDiscoveryDiagnostics(t *testing.T) {
	// A bad CONFIGURED path is a real diagnostic.
	bad := ResolvedCommand{
		Name:      "acme",
		Discovery: &RemoteDiscoveryDef{Prefix: "acme-", Path: filepath.Join(t.TempDir(), "no-such-subdir")},
	}
	problems := DiscoveryDiagnostics(bad)
	if len(problems) != 1 {
		t.Fatalf("DiscoveryDiagnostics = %v, want exactly one problem for the missing path", problems)
	}
	if !errors.Is(problems[0], fs.ErrNotExist) {
		t.Errorf("problem = %v, want a not-exist error a caller can classify", problems[0])
	}
	// Listing degrades gracefully: the bad path contributes nothing but never errors.
	if got := DiscoveredPlugins(bad); got != nil {
		t.Errorf("DiscoveredPlugins with only a bad path = %v, want nil", got)
	}

	// A clean configured path reports no problems.
	clean := ResolvedCommand{
		Name:      "acme",
		Discovery: &RemoteDiscoveryDef{Prefix: "acme-", Path: t.TempDir()},
	}
	if probs := DiscoveryDiagnostics(clean); probs != nil {
		t.Errorf("clean path produced diagnostics: %v", probs)
	}

	// No discovery → nil.
	if probs := DiscoveryDiagnostics(ResolvedCommand{Name: "acme"}); probs != nil {
		t.Errorf("no discovery produced diagnostics: %v", probs)
	}
}

// Incidental scan locations are not reported: a missing $PATH entry is normal, not a
// misconfiguration, so it must never surface as a diagnostic (only the configured path does).
func TestDiscoveryDiagnostics_pathNoiseSilent(t *testing.T) {
	t.Setenv("PATH", filepath.Join(t.TempDir(), "missing-path-entry"))
	cmd := ResolvedCommand{
		Name:      "acme",
		Discovery: &RemoteDiscoveryDef{Prefix: "acme-"}, // no configured Path
	}
	if probs := DiscoveryDiagnostics(cmd); probs != nil {
		t.Errorf("a bad $PATH entry was reported as a diagnostic: %v", probs)
	}
}

// TestDiscoveredPlugins_viaChain proves the documented help-handler seam: a handler
// reads its command's discovery off rtx.Chain() and gets the runtime plugin list —
// the path that closes static help's plugin blind spot (D-REMOTE-HELP).
func TestDiscoveredPlugins_viaChain(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"acme-foo", "acme-bar"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	def := Definition{
		Name: "acme", Handler: "App",
		Discovery: &RemoteDiscoveryDef{Prefix: "acme-", Path: dir},
	}
	rtx := NewContextFor(def, nil) // what the runtime hands a handler
	chain := rtx.Chain()
	got := DiscoveredPlugins(chain[len(chain)-1])
	if !reflect.DeepEqual(got, []string{"bar", "foo"}) {
		t.Errorf("DiscoveredPlugins via rtx.Chain() = %v, want [bar foo]", got)
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

// TestComplete_nestedSubcommands exercises completion below the first level: the
// children (and aliases) of a depth-1 command, and flag-name aggregation across the
// full root→…→leaf chain.
func TestComplete_nestedSubcommands(t *testing.T) {
	def := Definition{
		Name: "app", Handler: "App",
		Flags: []FlagDef{{Name: "verbose", Identifiers: []string{"--verbose"}, Type: "bool"}},
		Commands: []CommandDef{{
			Name: "remote", Handler: "AppRemote",
			Flags: []FlagDef{{Name: "name", Identifiers: []string{"--name"}, Type: "string"}},
			Commands: []CommandDef{
				{
					Name: "add", Handler: "AppRemoteAdd",
					Flags: []FlagDef{{Name: "url", Identifiers: []string{"--url"}, Type: "string"}},
				},
				{Name: "remove", Handler: "AppRemoteRemove", Aliases: []string{"rm"}},
			},
		}},
	}
	cases := []struct {
		name  string
		words []string
		want  []string
	}{
		{"children of a nested command", []string{"remote", ""}, []string{"add", "remove", "rm"}},
		{"nested children prefix", []string{"remote", "re"}, []string{"remove"}},
		// Flag completion aggregates the leaf's flags with every ancestor's.
		{"flags across the deep chain", []string{"remote", "add", "-"}, []string{"--name", "--url", "--verbose"}},
		{"a leaf has no sub-commands", []string{"remote", "add", "x"}, nil},
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
	p := NewProgram(completionDef(), &testHandlers{log: new([]string)}).WithArgs([]string{"__complete", "te"})
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
	p := NewProgram(dynCompletionDef(), dynCompletionHandlers{}).WithArgs([]string{"__complete", "build", "--mode", "s"})
	p.stdout, p.stderr = out, &bytes.Buffer{}
	if code := p.run(p.args); code != 0 {
		t.Fatalf("__complete run = %d, want 0", code)
	}
	if got := strings.TrimSpace(out.String()); got != "slow" {
		t.Errorf("dynamic __complete output = %q, want %q", got, "slow")
	}
}
