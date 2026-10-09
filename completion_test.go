package rotini

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// progFile is the file a program named name lives in: Windows finds a program only by an
// executable extension, so a plugin there is name.exe.
func progFile(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

func TestComplete_discoversPlugins(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"acme-foo", "acme-bar", "unrelated"} {
		if err := os.WriteFile(filepath.Join(dir, progFile(n)), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	def := Definition{
		Name: "acme", Handler: "App",
		Commands:        []CommandDef{{Name: "bar", Handler: "AcmeBar"}}, // collides with acme-bar
		PluginDiscovery: &PluginDiscoveryDef{Prefix: "acme-"}, PluginPath: dir,
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
	def.PluginDiscovery.Hidden = true
	if hidden := complete(def, []string{""}, nil, nil); contains(hidden, "foo") {
		t.Errorf("hidden discovery should not list plugins: %v", hidden)
	}
}

// TestDiscoveredPlugins pins discovered names minus declared collisions, sorted and deduped,
// and nil for absent or hidden discovery.
func TestDiscoveredPlugins(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"acme-foo", "acme-bar", "acme-zip", "unrelated"} {
		if err := os.WriteFile(filepath.Join(dir, progFile(n)), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cmd := Command{
		Name: "acme",
		// "bar" collides with discovered acme-bar (declared wins → excluded);
		// "ext"/"x" plugin shadows nothing discovered here.
		Commands:        []CommandDef{{Name: "bar", Handler: "AcmeBar"}},
		Plugins:         []PluginDef{{Name: "ext", Aliases: []string{"x"}, Binary: "acme-ext"}},
		PluginDiscovery: &PluginDiscoveryDef{Prefix: "acme-"}, PluginPath: dir,
	}

	got := cmd.DiscoveredPlugins()
	want := []DiscoveredPlugin{
		{Name: "foo", Path: filepath.Join(dir, progFile("acme-foo"))},
		{Name: "zip", Path: filepath.Join(dir, progFile("acme-zip"))},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("DiscoveredPlugins = %v, want %v (bar shadowed by declared command; unrelated unprefixed)", got, want)
	}

	// The path is the one dispatch would run: an earlier copy in search order shadows a later one.
	earlier := t.TempDir()
	if err := os.WriteFile(filepath.Join(earlier, progFile("acme-foo")), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", earlier)
	if got := cmd.DiscoveredPlugins(); len(got) == 0 || got[0].Path != filepath.Join(dir, progFile("acme-foo")) {
		t.Errorf("foo = %v, want the plugin-path copy (searched before PATH)", got)
	}
	if path, ok := cmd.PluginBinary("foo"); !ok || path != filepath.Join(dir, progFile("acme-foo")) {
		t.Errorf("PluginBinary(foo) = %q, %v — must agree with DiscoveredPlugins", path, ok)
	}

	// Hidden discovery → nil.
	hidden := cmd
	hd := *cmd.PluginDiscovery
	hd.Hidden = true
	hidden.PluginDiscovery = &hd
	if got := hidden.DiscoveredPlugins(); got != nil {
		t.Errorf("hidden discovery should yield nil, got %v", got)
	}

	// No discovery configured → nil.
	if got := (Command{Name: "acme"}).DiscoveredPlugins(); got != nil {
		t.Errorf("no discovery should yield nil, got %v", got)
	}
}

// TestPluginDiscoveryErrors pins that an unusable configured plugin path is reported with its
// path and cause, a clean or not-yet-existing path reports nothing, and listing still works.
func TestPluginDiscoveryErrors(t *testing.T) {
	// A path that is not a directory is a real diagnostic.
	file := filepath.Join(t.TempDir(), "plugins")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	bad := Command{
		Name:            "acme",
		PluginDiscovery: &PluginDiscoveryDef{Prefix: "acme-"}, PluginPath: file,
	}
	problems := bad.PluginDiscoveryErrors()
	if len(problems) != 1 || !strings.Contains(problems[0].Error(), file) {
		t.Fatalf("PluginDiscoveryErrors = %v, want exactly one problem naming %s", problems, file)
	}
	// Listing degrades gracefully: the bad path contributes nothing but never errors.
	if got := bad.DiscoveredPlugins(); got != nil {
		t.Errorf("DiscoveredPlugins with only a bad path = %v, want nil", got)
	}

	// A path that does not exist yet is not a problem.
	absent := bad
	absent.PluginPath = filepath.Join(t.TempDir(), "no-such-subdir")
	if probs := absent.PluginDiscoveryErrors(); len(probs) != 0 {
		t.Errorf("a missing plugin directory produced diagnostics: %v", probs)
	}

	// A clean configured path reports no problems.
	clean := Command{
		Name:            "acme",
		PluginDiscovery: &PluginDiscoveryDef{Prefix: "acme-"}, PluginPath: t.TempDir(),
	}
	if probs := clean.PluginDiscoveryErrors(); probs != nil {
		t.Errorf("clean path produced diagnostics: %v", probs)
	}

	// No discovery → nil.
	if probs := (Command{Name: "acme"}).PluginDiscoveryErrors(); probs != nil {
		t.Errorf("no discovery produced diagnostics: %v", probs)
	}
}

// Incidental scan locations are not reported: a missing $PATH entry is not a diagnostic.
func TestDiscoveryDiagnostics_pathNoiseSilent(t *testing.T) {
	t.Setenv("PATH", filepath.Join(t.TempDir(), "missing-path-entry"))
	cmd := Command{
		Name:            "acme",
		PluginDiscovery: &PluginDiscoveryDef{Prefix: "acme-"}, // no configured PluginPath
	}
	if probs := cmd.PluginDiscoveryErrors(); probs != nil {
		t.Errorf("a bad $PATH entry was reported as a diagnostic: %v", probs)
	}
}

// TestDiscoveredPlugins_viaChain pins that a handler reads its command's discovered plugins
// off rtx.CommandChain().
func TestDiscoveredPlugins_viaChain(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"acme-foo", "acme-bar"} {
		if err := os.WriteFile(filepath.Join(dir, progFile(n)), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	def := Definition{
		Name: "acme", Handler: "App",
		PluginDiscovery: &PluginDiscoveryDef{Prefix: "acme-"}, PluginPath: dir,
	}
	rtx := NewContextFor(def, nil) // what the runtime hands a handler
	chain := rtx.CommandChain()
	got := chain[len(chain)-1].DiscoveredPlugins()
	if len(got) != 2 || got[0].Name != "bar" || got[1].Name != "foo" {
		t.Errorf("DiscoveredPlugins via rtx.CommandChain() = %v, want bar and foo", got)
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
		Plugins: []PluginDef{{Name: "plugin", Binary: "app-plugin"}},
	}
}

func TestComplete(t *testing.T) {
	def := completionDef()
	cases := []struct {
		name  string
		words []string
		want  []string
	}{
		{"all commands + plugins", []string{""}, []string{"b", "build", "plugin", "test"}},
		{"command prefix", []string{"bu"}, []string{"build"}},
		{"plugin prefix", []string{"plu"}, []string{"plugin"}},
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

// BenchmarkComplete measures the per-keystroke completion path, which the generated scripts
// call on every TAB and must stay sub-millisecond; `make test-bench` reports it.
func BenchmarkComplete(b *testing.B) {
	def := completionDef()
	cases := []struct {
		name  string
		words []string
	}{
		{"commands", []string{""}},
		{"flag-names", []string{"build", "-"}},
		{"flag-value", []string{"build", "--mode", ""}},
	}
	for _, c := range cases {
		b.Run(c.name, func(b *testing.B) {
			for b.Loop() {
				complete(def, c.words, nil, nil)
			}
		})
	}
}

// TestComplete_mapKeyPaths covers a map flag's declared key vocabulary: paths
// complete up to the '=' (the value past it is the user's), and the dynamic
// completer still outranks the static paths.
func TestComplete_mapKeyPaths(t *testing.T) {
	def := Definition{
		Name: "app", Handler: "App",
		Flags: []FlagDef{{
			Name: "set", Identifiers: []string{"--set"}, Type: "map[string]any",
			DottedKeys: true, KeyPaths: []string{"image.tag", "replicas"},
		}},
	}
	cases := []struct {
		name  string
		words []string
		want  []string
	}{
		{"all key paths", []string{"--set", ""}, []string{"image.tag=", "replicas="}},
		{"key path prefix", []string{"--set", "ima"}, []string{"image.tag="}},
		{"past the '=' offers nothing", []string{"--set", "image.tag="}, nil},
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

// TestComplete_fromFileFallsBack pins the from:file completion contract: an '@'
// partial offers nothing (the shell falls back to file completion), even when
// the flag has a static enum; without from:file the enum still answers.
func TestComplete_fromFileFallsBack(t *testing.T) {
	def := Definition{
		Name: "app", Handler: "App",
		Flags: []FlagDef{
			{Name: "token", Identifiers: []string{"--token"}, Type: "string", Enum: []string{"@literal", "abc"}, From: []string{"file"}},
			{Name: "plain", Identifiers: []string{"--plain"}, Type: "string", Enum: []string{"@literal", "abc"}},
		},
	}
	if got := complete(def, []string{"--token", "@"}, nil, nil); len(got) != 0 {
		t.Errorf("complete(@ on from:file) = %v, want nothing (shell file fallback)", got)
	}
	if got := complete(def, []string{"--plain", "@"}, nil, nil); !reflect.DeepEqual(got, []string{"@literal"}) {
		t.Errorf("complete(@ on plain flag) = %v, want the enum match", got)
	}
}

// TestComplete_noAutoHelpFlag pins that completion offers -h/--help only when the CLI declares
// a help flag.
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

func (dynCompletionHandlers) App() Handler      { return dynStub{} }
func (dynCompletionHandlers) AppBuild() Handler { return dynBuildHandler{} }

type dynStub struct {
	NoCascadingPreRun
	NoPreRun
	NoPostRun
	NoCascadingPostRun
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

// TestComplete_dynamicFlagValue pins that a FlagValueCompleter's candidates win over the enum
// and are prefix-filtered, and that no completer, a nil return, or no handlers falls back to
// the enum.
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
			got := complete(def, c.words, reflectLookup(c.handlers), newContext())
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("complete(%v) = %v, want %v", c.words, got, c.want)
			}
		})
	}
}

// TestComplete_dynamicReceivesContext pins that the completer's rtx carries the resolved
// chain, the completion words and the Program's dependencies.
func TestComplete_dynamicReceivesContext(t *testing.T) {
	def := dynCompletionDef()
	rtx := newContext()
	got := complete(def, []string{"build", "--mode", "x"}, reflectLookup(dynCtxHandlers{t: t}), rtx)
	if !reflect.DeepEqual(got, []string{"x-leaf-build"}) {
		t.Errorf("completer did not observe rtx chain/args: %v", got)
	}
}

type dynCtxHandlers struct{ t *testing.T }

func (dynCtxHandlers) App() Handler        { return dynStub{} }
func (h dynCtxHandlers) AppBuild() Handler { return dynCtxBuild{t: h.t} }

type dynCtxBuild struct {
	dynStub
	t *testing.T
}

func (h dynCtxBuild) CompleteFlagValue(rtx *Context, flag, partial string) []string {
	chain := rtx.CommandChain()
	if len(chain) == 0 {
		h.t.Fatal("rtx.CommandChain() empty inside completer")
	}
	leaf := chain[len(chain)-1].Name
	return []string{partial + "-leaf-" + leaf}
}

func TestComplete_runIntercept(t *testing.T) {
	out := &bytes.Buffer{}
	p := NewProgram(completionDef(), &testHandlers{log: new([]string)}).WithArgs([]string{"__complete", "te"})
	p.stdout, p.stderr = out, &bytes.Buffer{}
	if code, _ := p.Run(p.args); code != 0 {
		t.Fatalf("__complete run = %d, want 0", code)
	}
	if got := strings.TrimSpace(out.String()); got != "test" {
		t.Errorf("__complete output = %q, want %q", got, "test")
	}
}

// TestComplete_dynamicViaProgram pins the __complete path end to end: a handler's
// FlagValueCompleter drives the candidates printed to stdout.
func TestComplete_dynamicViaProgram(t *testing.T) {
	out := &bytes.Buffer{}
	p := NewProgram(dynCompletionDef(), dynCompletionHandlers{}).WithArgs([]string{"__complete", "build", "--mode", "s"})
	p.stdout, p.stderr = out, &bytes.Buffer{}
	if code, _ := p.Run(p.args); code != 0 {
		t.Fatalf("__complete run = %d, want 0", code)
	}
	if got := strings.TrimSpace(out.String()); got != "slow" {
		t.Errorf("dynamic __complete output = %q, want %q", got, "slow")
	}
}

// completionFixtureDef is a definition exercising the full completion surface:
// nested commands, enum + plain flags, enum + variadic positionals, and hidden
// commands/flags/arguments.
func completionFixtureDef() Definition {
	return Definition{
		Name: "app", Handler: "App",
		Flags: []FlagDef{
			{Name: "verbose", Identifiers: []string{"-v", "--verbose"}, Type: "bool"},
			{Name: "config", Identifiers: []string{"--config"}, Type: "string"},
		},
		Commands: []CommandDef{
			{
				Name: "deploy", Handler: "AppDeploy",
				Arguments: []ArgDef{
					{Name: "target", Type: "string", Enum: []string{"api", "web", "worker"}},
					{Name: "extra", Type: "[]string", Variadic: true, Enum: []string{"fast", "slow"}},
				},
				Flags: []FlagDef{
					{Name: "env", Identifiers: []string{"--env", "-e"}, Type: "string", Enum: []string{"dev", "staging", "prod"}},
					{Name: "out", Identifiers: []string{"--out"}, Type: "string"},
					{Name: "force", Identifiers: []string{"--force"}, Type: "bool"},
					{Name: "covert", Identifiers: []string{"--covert"}, Type: "bool", Hidden: true},
				},
				Commands: []CommandDef{{Name: "status", Handler: "AppDeployStatus"}},
			},
			{Name: "ghost", Handler: "AppGhost", Hidden: true},
			{
				Name: "stash", Handler: "AppStash",
				Arguments: []ArgDef{{Name: "id", Type: "string", Enum: []string{"a1", "b2"}, Hidden: true}},
			},
		},
	}
}

// TestComplete_dispatchFaithful locks completion to dispatch's reading of the
// context words: flag values are never treated as commands, descent stops at
// the first positional, and "--" ends both flag handling and descent.
func TestComplete_dispatchFaithful(t *testing.T) {
	def := completionFixtureDef()
	cases := []struct {
		name  string
		words []string
		want  []string
	}{
		{"root commands, hidden excluded", []string{""}, []string{"deploy", "stash"}},
		{"prefix filter", []string{"dep"}, []string{"deploy"}},
		{"subcommand plus arg enum", []string{"deploy", ""}, []string{"api", "status", "web", "worker"}},
		{"flag value: enum", []string{"deploy", "--env", ""}, []string{"dev", "prod", "staging"}},
		{"flag value: enum prefix", []string{"deploy", "--env", "d"}, []string{"dev"}},
		{"flag value: short identifier", []string{"deploy", "-e", ""}, []string{"dev", "prod", "staging"}},
		{"flag value without candidates is empty", []string{"deploy", "--out", ""}, nil},
		{"flag value consumed, next word completes again", []string{"deploy", "--out", "x.txt", ""}, []string{"api", "status", "web", "worker"}},
		{"flag value never resolves as a command", []string{"--config", "deploy", ""}, []string{"deploy", "stash"}},
		{"bool flag consumes no value", []string{"deploy", "--force", ""}, []string{"api", "status", "web", "worker"}},
		{"descent stops at the first positional", []string{"deploy", "api", ""}, []string{"fast", "slow"}},
		{"variadic arg absorbs the tail", []string{"deploy", "api", "fast", ""}, []string{"fast", "slow"}},
		{"flag names, hidden excluded, chain-wide", []string{"deploy", "-"}, []string{"--config", "--env", "--force", "--out", "--verbose", "-e", "-v"}},
		{"inline = completes the value", []string{"deploy", "--env=d"}, []string{"--env=dev"}},
		{"inline = on a valueless flag is empty", []string{"deploy", "--force=x"}, nil},
		{"bash = split: empty value", []string{"deploy", "--env", "=", ""}, []string{"dev", "prod", "staging"}},
		{"bash = split: glued value then next word", []string{"deploy", "--env", "=", "dev", ""}, []string{"api", "status", "web", "worker"}},
		{"after -- only positionals complete", []string{"deploy", "--", ""}, []string{"api", "web", "worker"}},
		{"after -- flags never complete", []string{"deploy", "--", "-"}, nil},
		{"hidden argument offers nothing", []string{"stash", ""}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := complete(def, tc.words, nil, nil)
			if len(got) == 0 && len(tc.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("complete(%v) = %v, want %v", tc.words, got, tc.want)
			}
		})
	}
}

// argCompleterHandlers implements ArgValueCompleter on the deploy handler: the
// target argument completes dynamically, anything else defers (nil → enum).
type argCompleterHandlers struct{}

type deployArgCompleter struct{}

func (deployArgCompleter) Run(ctx context.Context, rtx *Context)              {}
func (deployArgCompleter) CascadingPreRun(ctx context.Context, rtx *Context)  {}
func (deployArgCompleter) PreRun(ctx context.Context, rtx *Context)           {}
func (deployArgCompleter) PostRun(ctx context.Context, rtx *Context)          {}
func (deployArgCompleter) CascadingPostRun(ctx context.Context, rtx *Context) {}
func (deployArgCompleter) CompleteArgValue(rtx *Context, arg, partial string) []string {
	if arg == "target" {
		return []string{"dyn-one", "dyn-two"}
	}
	return nil
}

func (argCompleterHandlers) AppDeploy() Handler { return deployArgCompleter{} }

// TestComplete_dynamicArgValue confirms a handler implementing ArgValueCompleter
// supplies positional candidates (authoritative over the enum), and that a nil
// return defers to the static enum.
func TestComplete_dynamicArgValue(t *testing.T) {
	def := completionFixtureDef()
	want := []string{"dyn-one", "dyn-two"}
	// Candidates are prefix-filtered, so query with a prefix the dynamic ones share:
	// "x" would filter every one of them out and prove nothing.
	got := complete(def, []string{"deploy", "dyn"}, reflectLookup(argCompleterHandlers{}), nil)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("dynamic arg candidates = %v, want %v", got, want)
	}
	// The variadic "extra" argument gets nil from the completer → its enum.
	got = complete(def, []string{"deploy", "api", "f"}, reflectLookup(argCompleterHandlers{}), nil)
	if !reflect.DeepEqual(got, []string{"fast"}) {
		t.Errorf("enum fallback after dynamic nil = %v, want [fast]", got)
	}
}

// TestComplete_descriptions pins the wire shape: a candidate with a summary is
// "name\tsummary" (aliases share the command's summary; every identifier
// carries its flag's), one without stays bare, the prefix filter matches the
// name part only, and enum values ride bare.
func TestComplete_descriptions(t *testing.T) {
	def := Definition{
		Name: "app", Handler: "App",
		Flags: []FlagDef{
			{Name: "verbose", Identifiers: []string{"-v", "--verbose"}, Summary: "chattier output", Type: "bool"},
			{Name: "config", Identifiers: []string{"--config"}, Type: "string"}, // no summary: bare
		},
		Commands: []CommandDef{
			{Name: "deploy", Handler: "AppDeploy", Summary: "ship it", Aliases: []string{"dep"},
				Flags: []FlagDef{{Name: "env", Identifiers: []string{"--env"}, Summary: "target environment", Type: "string", Enum: []string{"dev", "prod"}}}},
			{Name: "status", Handler: "AppStatus"}, // no summary: bare
		},
		Plugins: []PluginDef{{Name: "scan", Binary: "app-scan", Summary: "scan things"}},
	}

	got := complete(def, []string{""}, nil, nil)
	want := []string{"dep\tship it", "deploy\tship it", "scan\tscan things", "status"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("command candidates = %v, want %v", got, want)
	}

	// The prefix filter matches the name, not the description.
	if got := complete(def, []string{"dep"}, nil, nil); !reflect.DeepEqual(got, []string{"dep\tship it", "deploy\tship it"}) {
		t.Errorf("prefix-filtered = %v, want the dep/deploy pair", got)
	}

	// Flags: every identifier carries the summary; a summary-less flag is bare.
	got = complete(def, []string{"-"}, nil, nil)
	want = []string{"--config", "--verbose\tchattier output", "-v\tchattier output"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("flag candidates = %v, want %v", got, want)
	}

	// Enum values stay bare — they describe themselves.
	got = complete(def, []string{"deploy", "--env", ""}, nil, nil)
	if !reflect.DeepEqual(got, []string{"dev", "prod"}) {
		t.Errorf("enum candidates = %v, want bare values", got)
	}
}

// TestComplete_stripsStyledSummary pins that an ANSI-styled summary reaches the completion
// wire as plain text.
func TestComplete_stripsStyledSummary(t *testing.T) {
	def := Definition{
		Name: "app", Handler: "App",
		Commands: []CommandDef{
			{Name: "deploy", Handler: "AppDeploy", Summary: "\x1b[1mship it\x1b[0m"},
		},
	}
	got := complete(def, []string{""}, nil, nil)
	want := []string{"deploy\tship it"} // styling gone, text intact
	if !reflect.DeepEqual(got, want) {
		t.Errorf("styled-summary completion = %q, want %q (no ANSI on the wire)", got, want)
	}
}

// TestComplete_pluginOpaque confirms completion goes silent past a plugin or
// discovered-plugin token — the dispatched binary owns that argument surface.
func TestComplete_pluginOpaque(t *testing.T) {
	def := Definition{
		Name: "app", Handler: "App",
		Commands: []CommandDef{{Name: "local", Handler: "AppLocal"}},
		Plugins:  []PluginDef{{Name: "plugin", Binary: "app-plugin"}},
	}
	if got := complete(def, []string{"plugin", ""}, nil, nil); got != nil {
		t.Errorf("complete past a plugin token = %v, want nil", got)
	}
	// The plugin name itself still completes.
	if got := complete(def, []string{"plug"}, nil, nil); !reflect.DeepEqual(got, []string{"plugin"}) {
		t.Errorf("plugin name completion = %v, want [plugin]", got)
	}
}

// TestComplete_nestedPlugin pins that plugins declared on a sub-command complete by name, and
// that completion goes opaque past them.
func TestComplete_nestedPlugin(t *testing.T) {
	def := Definition{
		Name: "acme", Handler: "Acme",
		Commands: []CommandDef{{
			Name: "cluster", Handler: "AcmeCluster",
			Commands: []CommandDef{{Name: "list", Handler: "AcmeClusterList"}},
			Plugins:  []PluginDef{{Name: "scan", Binary: "acme-scan", Aliases: []string{"sc"}}},
		}},
	}
	got := complete(def, []string{"cluster", ""}, nil, nil)
	want := []string{"list", "sc", "scan"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("complete(cluster) = %v, want %v", got, want)
	}
	if got := complete(def, []string{"cluster", "scan", ""}, nil, nil); got != nil {
		t.Errorf("complete past nested plugin = %v, want nil", got)
	}
}

// completionHintDef declares one input per hint kind, plus the two shapes that must produce
// none: a bool (no value to complete) and a flag name being typed.
func completionHintDef() Definition {
	return Definition{
		Name: "app", Handler: "App",
		Flags: []FlagDef{
			{Name: "config", Identifiers: []string{"--config"}, Type: "string",
				Complete: Completion{Kind: "file", Extensions: []string{"yaml", "yml"}}},
			{Name: "out", Identifiers: []string{"--out"}, Type: "string",
				Complete: Completion{Kind: "directory"}},
			{Name: "id", Identifiers: []string{"--id"}, Type: "string",
				Complete: Completion{Kind: "none"}},
			{Name: "plain", Identifiers: []string{"--plain"}, Type: "string"},
			{Name: "force", Identifiers: []string{"--force"}, Type: "bool"},
		},
		Commands: []CommandDef{{
			Name: "open", Handler: "AppOpen",
			Arguments: []ArgDef{{Name: "path", Type: "string", Complete: Completion{Kind: "file"}}},
		}},
	}
}

// TestCompletionHint pins the directive line each declared complete: hint produces.
func TestCompletionHint(t *testing.T) {
	cases := []struct {
		name  string
		words []string
		want  string
	}{
		{"a file flag, separate word", []string{"--config", ""}, ":rotini:file yaml yml"},
		{"a file flag, inline form", []string{"--config="}, ":rotini:file yaml yml"},
		{"a directory flag", []string{"--out", ""}, ":rotini:directory"},
		{"an opaque value suppresses the shell's default", []string{"--id", ""}, ":rotini:none"},
		{"a flag with no hint leaves the shell alone", []string{"--plain", ""}, ""},
		{"a bool takes no value", []string{"--force", ""}, ""},
		{"a flag NAME being typed is not a path", []string{"--con"}, ""},
		{"a positional with a hint", []string{"open", ""}, ":rotini:file"},
		{"a bare word that could still be a sub-command", []string{""}, ""},
		{"nothing after the terminator", []string{"--", ""}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := completionHint(completionHintDef(), tc.words); got != tc.want {
				t.Errorf("completionHint(%q) = %q, want %q", tc.words, got, tc.want)
			}
		})
	}
}

// TestCompletionHint_isSeparableFromCandidates pins that the directive line, which shares the
// candidates' stream, is distinguishable from every candidate.
func TestCompletionHint_isSeparableFromCandidates(t *testing.T) {
	def := completionHintDef()
	for _, words := range [][]string{{""}, {"--"}, {"--config", ""}, {"open", ""}} {
		for _, c := range complete(def, words, nil, nil) {
			if strings.HasPrefix(c, completionDirectivePrefix) {
				t.Errorf("candidate %q collides with the directive prefix", c)
			}
		}
	}
	if d := completionHint(def, []string{"--config", ""}); !strings.HasPrefix(d, completionDirectivePrefix) {
		t.Errorf("directive %q does not carry the prefix a script matches on", d)
	}
}

// TestCompletionHint_noneIsNotTheSameAsEmpty pins that no hint leaves the shell's default
// while "none" emits a directive suppressing it.
func TestCompletionHint_noneIsNotTheSameAsEmpty(t *testing.T) {
	def := completionHintDef()
	none := completionHint(def, []string{"--id", ""})
	unset := completionHint(def, []string{"--plain", ""})

	if none == unset {
		t.Fatalf("kind none and no hint both produced %q — the shell cannot tell them apart", none)
	}
	if none != ":rotini:none" {
		t.Errorf("none = %q, want the explicit suppression directive", none)
	}
	if unset != "" {
		t.Errorf("no hint = %q, want empty so the shell applies its own default", unset)
	}
}

// completeIn runs Program.Complete in format and returns its whole stdout.
func completeIn(t *testing.T, format CompletionFormat, def Definition, handlers any, words ...string) string {
	t.Helper()
	out := &bytes.Buffer{}
	p := NewProgram(def, handlers)
	p.stdout, p.stderr = out, &bytes.Buffer{}
	if code, err := p.Complete(words, format); code != 0 || err != nil {
		t.Fatalf("Complete(%q) = %d, %v", words, code, err)
	}
	return out.String()
}

// TestPluginCompletion_directives pins the hint → directive mapping; the numbers are the
// plugin hosts' wire values.
func TestPluginCompletion_directives(t *testing.T) {
	cases := []struct {
		name  string
		words []string
		want  string
	}{
		{"file with extensions: FilterFileExt, extensions as candidates", []string{"--config", ""}, "yaml\nyml\n:8\n"},
		{"directory: FilterDirs", []string{"--out", ""}, ":16\n"},
		{"none: NoFileComp", []string{"--id", ""}, ":4\n"},
		{"no hint: Default", []string{"--plain", ""}, ":0\n"},
		{"plain file: Default, whose fallback is files", []string{"open", ""}, ":0\n"},
		{"candidates ride with Default", []string{"op"}, "open\n:0\n"},
		{"no words completes a new first word", nil, "open\n:0\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := completeIn(t, PluginCompletion, completionHintDef(), nil, tc.words...); got != tc.want {
				t.Errorf("Complete(%q, PluginCompletion) = %q, want %q", tc.words, got, tc.want)
			}
		})
	}
}

// TestPluginCompletion_candidatesWinOverAFilteringHint pins that real candidates suppress the
// file-extension (8) and directory (16) directives, whose candidates the hosts read as
// arguments.
func TestPluginCompletion_candidatesWinOverAFilteringHint(t *testing.T) {
	def := Definition{
		Name: "app", Handler: "App",
		Flags: []FlagDef{
			{Name: "config", Identifiers: []string{"--config"}, Type: "string", Enum: []string{"base.yaml"},
				Complete: Completion{Kind: "file", Extensions: []string{"yaml"}}},
			{Name: "out", Identifiers: []string{"--out"}, Type: "string", Enum: []string{"dist"},
				Complete: Completion{Kind: "directory"}},
			{Name: "id", Identifiers: []string{"--id"}, Type: "string", Enum: []string{"a1"},
				Complete: Completion{Kind: "none"}},
		},
	}
	for words, want := range map[string]string{
		"--config": "base.yaml\n:0\n",
		"--out":    "dist\n:0\n",
		"--id":     "a1\n:4\n", // none is compatible with candidates: it only suppresses files
	} {
		if got := completeIn(t, PluginCompletion, def, nil, words, ""); got != want {
			t.Errorf("Complete(%s, PluginCompletion) = %q, want %q", words, got, want)
		}
	}
}

// TestComplete_formatIsTheOnlyDifference pins that every format receives the same answer, and
// that the nil format matches __complete's output byte for byte.
func TestComplete_formatIsTheOnlyDifference(t *testing.T) {
	if got := completeIn(t, PluginCompletion, dynCompletionDef(), dynCompletionHandlers{}, "build", "--mode", "s"); got != "slow\n:0\n" {
		t.Errorf("dynamic completer = %q", got)
	}
	def := Definition{Name: "app", Handler: "App", Commands: []CommandDef{{Name: "deploy", Handler: "AppDeploy", Summary: "ship it"}}}
	if got := completeIn(t, PluginCompletion, def, nil, "de"); got != "deploy\tship it\n:0\n" {
		t.Errorf("PluginCompletion, with a description = %q", got)
	}
	if got := completeIn(t, nil, def, nil, "de"); got != "deploy\tship it\n" {
		t.Errorf("rotini's own, with a description = %q", got)
	}
	if got := completeIn(t, nil, completionHintDef(), nil, "--config", ""); got != ":rotini:file yaml yml\n" {
		t.Errorf("rotini's own hint line = %q", got)
	}
}

// TestComplete_customFormat pins that a custom CompletionFormat (here "value:description",
// colons escaped) sees the same candidates and hint the built-ins do.
func TestComplete_customFormat(t *testing.T) {
	var seen CompletionResult
	colonFormat := func(w io.Writer, r CompletionResult) error {
		seen = r
		for _, c := range r.Candidates {
			line := strings.ReplaceAll(c.Value, ":", `\:`)
			if c.Description != "" {
				line += ":" + c.Description
			}
			if _, err := fmt.Fprintln(w, line); err != nil {
				return err
			}
		}
		return nil
	}
	def := Definition{Name: "app", Handler: "App", Commands: []CommandDef{
		{Name: "deploy", Handler: "AppDeploy", Summary: "ship it"},
		{Name: "db:migrate", Handler: "AppDbMigrate"},
	}}
	if got := completeIn(t, colonFormat, def, nil, ""); got != "db\\:migrate\ndeploy:ship it\n" {
		t.Errorf("custom format = %q", got)
	}

	completeIn(t, colonFormat, completionHintDef(), nil, "--config", "")
	if seen.Hint.Kind != "file" || !slices.Equal(seen.Hint.Extensions, []string{"yaml", "yml"}) {
		t.Errorf("the format was not handed the hint: %+v", seen.Hint)
	}
}

// TestComplete_formatError pins that a format's write error fails the request.
func TestComplete_formatError(t *testing.T) {
	boom := errors.New("closed pipe")
	p := NewProgram(completionHintDef(), nil)
	p.stdout, p.stderr = &bytes.Buffer{}, &bytes.Buffer{}
	code, err := p.Complete([]string{""}, func(io.Writer, CompletionResult) error { return boom })
	if code != 1 || !errors.Is(err, boom) {
		t.Errorf("Complete = %d, %v; want 1 and the format's error", code, err)
	}
}

// TestWithCompletion pins that the setter switches the format __complete writes and nil
// restores rotini's own. With no hint, rotini's format ends on a candidate, which a plugin host
// would read as the directive.
func TestWithCompletion(t *testing.T) {
	run := func(p *Program, argv ...string) string {
		t.Helper()
		out := &bytes.Buffer{}
		p.stdout, p.stderr = out, &bytes.Buffer{}
		if code, err := p.Run(argv); code != 0 || err != nil {
			t.Fatalf("Run(%q) = %d, %v", argv, code, err)
		}
		return out.String()
	}
	def := completionHintDef()

	if got := run(NewProgram(def, nil), "__complete", "op"); got != "open\n" {
		t.Errorf("default __complete = %q, want rotini's format (no directive without a hint)", got)
	}
	if got := run(NewProgram(def, nil).WithCompletion(PluginCompletion), "__complete", "op"); got != "open\n:0\n" {
		t.Errorf("WithCompletion(PluginCompletion) __complete = %q, want the plugin hosts' format", got)
	}
	if got := run(NewProgram(def, nil).WithCompletion(PluginCompletion), "__complete", "--id", ""); got != ":4\n" {
		t.Errorf("WithCompletion(PluginCompletion) hint = %q, want NoFileComp", got)
	}
	if got := run(NewProgram(def, nil).WithCompletion(PluginCompletion).WithCompletion(nil), "__complete", "op"); got != "open\n" {
		t.Errorf("WithCompletion(nil) = %q, want rotini's own restored", got)
	}
}

// lenientRootInputs is a root's generated inputs shape: one global flag with an env fallback,
// and a required one that a half-typed line has not supplied yet.
type lenientRootInputs struct {
	App struct {
		Flags struct {
			Store string `rotini:"store" recon:"store" env:"LENIENT_STORE"`
			Token string `rotini:"token"`
		}
		Arguments struct{}
	}
}

type lenientHandlers struct{}

type lenientShow struct{ deployArgCompleter }

// CompleteArgValue follows the FlagValueCompleter doc recipe: the root's flags, read as the
// root command from the line and the environment, with nothing validated.
func (lenientShow) CompleteArgValue(rtx *Context, arg, partial string) []string {
	var in lenientRootInputs
	AsCommand(0, func(_ context.Context, rtx *Context) {
		env, _ := rtx.EnvInputs[lenientRootInputs]()
		argv, _ := rtx.ArgvInputs[lenientRootInputs]()
		in = MergeInputs(env, argv)
	})(context.Background(), rtx)
	return []string{"from-" + in.App.Flags.Store}
}

func (lenientHandlers) AppShow() Handler { return lenientShow{} }

// TestComplete_lenientParseOfAnAncestorsFlags pins the documented recipe: a completer reads a
// root flag from the line, else its environment fallback, though a required flag is missing.
func TestComplete_lenientParseOfAnAncestorsFlags(t *testing.T) {
	def := Definition{
		Name: "app", Handler: "App",
		Flags: []FlagDef{
			{Name: "store", Identifiers: []string{"--store"}, Type: "string"},
			{Name: "token", Identifiers: []string{"--token"}, Type: "string", Required: true},
		},
		Commands: []CommandDef{{Name: "show", Handler: "AppShow", Arguments: []ArgDef{{Name: "name", Type: "string"}}}},
	}
	t.Setenv("LENIENT_STORE", "env")
	if got := complete(def, []string{"show", ""}, reflectLookup(lenientHandlers{}), newContext()); !reflect.DeepEqual(got, []string{"from-env"}) {
		t.Errorf("env fallback: candidates = %v, want [from-env]", got)
	}
	if got := complete(def, []string{"--store", "line", "show", ""}, reflectLookup(lenientHandlers{}), newContext()); !reflect.DeepEqual(got, []string{"from-line"}) {
		t.Errorf("flag on the line: candidates = %v, want [from-line] — argv outranks env", got)
	}
}

// TestComplete_followsTheParsersFlagValueRule pins that completion uses the parser's rule for
// which word is a flag's value (`--color sub <TAB>`, `-vn 5 sub <TAB>`), so a bare
// optional-value flag does not consume the next word.
func TestComplete_followsTheParsersFlagValueRule(t *testing.T) {
	def := Definition{Name: "app", Handler: "App",
		Flags: []FlagDef{
			{Name: "color", Identifiers: []string{"--color"}, Type: "string", ImplicitValue: "always", Enum: []string{"always", "never"}},
			{Name: "verbose", Identifiers: []string{"-v"}, Type: "bool"},
			{Name: "num", Identifiers: []string{"-n"}, Type: "int"},
		},
		Commands: []CommandDef{{Name: "sub", Handler: "AppSub", Commands: []CommandDef{{Name: "leaf", Handler: "AppSubLeaf"}}}}}
	for _, words := range [][]string{{"--color", "sub", "l"}, {"-vn", "5", "sub", "l"}} {
		if got := complete(def, words, nil, nil); !reflect.DeepEqual(got, []string{"leaf"}) {
			t.Errorf("complete %q = %v, want [leaf]", words, got)
		}
	}
	if got := complete(def, []string{"--color", "s"}, nil, nil); !reflect.DeepEqual(got, []string{"sub"}) {
		t.Errorf("complete --color s = %v, want [sub] — the optional value must be attached", got)
	}
	if got := complete(def, []string{"--color=n"}, nil, nil); !reflect.DeepEqual(got, []string{"--color=never"}) {
		t.Errorf("complete --color=n = %v, want [--color=never]", got)
	}
}
