package rotini

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/go-rotini/recon"
)

// scDef is a root with a short-circuit --help and every kind of declared requirement, plus a
// sub-command whose own requirement a root short-circuit flag also waives.
func scDef(t *testing.T) Definition {
	t.Helper()
	low, high := 1.0, 10.0
	return Definition{
		Name: "app", Handler: "App",
		Flags: []FlagDef{
			{Name: "help", Identifiers: []string{"-h", "--help"}, Type: "bool", ShortCircuit: true},
			{Name: "token", Identifiers: []string{"--token"}, Type: "string", Required: true},
			{Name: "level", Identifiers: []string{"--level"}, Type: "string", Enum: []string{"low", "high"}},
			{Name: "port", Identifiers: []string{"--port"}, Type: "int", Minimum: &low, Maximum: &high},
			{Name: "file", Identifiers: []string{"--file"}, Type: "existingfile"},
			{Name: "labels", Identifiers: []string{"--labels"}, Type: "map[string]string"},
			{Name: "a", Identifiers: []string{"--a"}, Type: "bool"},
			{Name: "b", Identifiers: []string{"--b"}, Type: "bool"},
		},
		FlagGroups:       []FlagGroup{{Kind: FlagGroupMutuallyExclusive, Flags: []string{"a", "b"}}},
		FlagDependencies: []FlagDependency{{When: "level", Requires: []string{"port"}}},
		Commands: []CommandDef{{
			Name: "run", Handler: "AppRun",
			Arguments: []ArgDef{{Name: "target", Type: "string", Required: true}},
		}},
	}
}

// TestShortCircuit_parseWaivesRequirements walks what a short-circuit flag waives and what it
// keeps through Parser.Parse: requirements are waived when a short-circuit flag is set on argv; what cannot
// be read is still an error.
func TestShortCircuit_parseWaivesRequirements(t *testing.T) {
	tests := []struct {
		name string
		argv []string
		kind ParseKind // ParseKindUnspecified = no error expected
	}{
		{"required without the flag", []string{}, ParseKindMissingRequired},
		{"required waived", []string{"--help"}, ParseKindUnspecified},
		{"short identifier", []string{"-h"}, ParseKindUnspecified},
		{"enum waived", []string{"--help", "--level", "medium"}, ParseKindUnspecified},
		{"bound waived", []string{"--help", "--port", "99"}, ParseKindUnspecified},
		{"existing-file check waived", []string{"--help", "--file", "/no/such/file"}, ParseKindUnspecified},
		{"flag group waived", []string{"--help", "--a", "--b"}, ParseKindUnspecified},
		{"flag dependency waived", []string{"--help", "--level", "low"}, ParseKindUnspecified},
		{"sub-command requirement waived", []string{"run", "--help"}, ParseKindUnspecified},
		{"explicit false waives nothing", []string{"--help=false"}, ParseKindMissingRequired},
		{"unknown flag kept", []string{"--help", "--bogus"}, ParseKindUnknownFlag},
		{"unknown command kept", []string{"nope", "--help"}, ParseKindUnknownCommand},
		{"uncoercible value kept", []string{"--help", "--port", "abc"}, ParseKindInvalidValue},
		{"malformed map kept", []string{"--help", "--labels", "novalue"}, ParseKindInvalidValue},
		{"extra positional kept", []string{"run", "x", "y", "--help"}, ParseKindTooManyArguments},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// A typed field, as generated code has, so a value of the wrong type is coerced
			// (and fails) at bind time. A sub-command run binds a type for that command.
			var root struct {
				App struct {
					Flags struct {
						Port int `rotini:"port"`
					}
				}
			}
			var out any = &root
			if len(tt.argv) > 0 && tt.argv[0] == "run" {
				out = &struct{}{}
			}
			err := NewParser().Parse(NewContextFor(scDef(t), tt.argv), out)
			if tt.kind == ParseKindUnspecified {
				if err != nil {
					t.Fatalf("Parse(%q) = %v, want no error", tt.argv, err)
				}
				return
			}
			var pe *ParseError
			if !errors.As(err, &pe) || pe.Kind != tt.kind {
				t.Fatalf("Parse(%q) = %v, want ParseKind %v", tt.argv, err, tt.kind)
			}
		})
	}
}

// TestShortCircuit_flagValueReadable confirms the short-circuit flag itself reads true, which is
// how a handler knows to act, and that the other values given are still bound.
func TestShortCircuit_flagValueReadable(t *testing.T) {
	var in struct {
		App struct {
			Flags struct {
				Help  bool   `rotini:"help"`
				Level string `rotini:"level"`
			}
		}
	}
	if err := NewParser().Parse(NewContextFor(scDef(t), []string{"--help", "--level", "medium"}), &in); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !in.App.Flags.Help || in.App.Flags.Level != "medium" {
		t.Errorf("bound %+v, want Help=true Level=medium", in.App.Flags)
	}
}

type scChannelInputs struct {
	App struct {
		Flags struct {
			Help bool `rotini:"help"`
		}
		Arguments struct{}
		Env       struct {
			Region string `rotini:"region" recon:"region,required"`
			Port   int    `rotini:"port" recon:"port"`
		}
		Config struct {
			Token string `rotini:"token" recon:"api.token,required"`
		}
		Stdin *tbStdinReqPayload `stdin:"yaml,required"`
	}
}

func scChannelDef() Definition {
	return Definition{
		Name: "app", Handler: "App",
		Flags: []FlagDef{{Name: "help", Identifiers: []string{"--help"}, Type: "bool", ShortCircuit: true}},
	}
}

// TestShortCircuit_readerWaivesChannelRequirements covers the env, config and stdin channels: a
// required env var, a required config key, a config file's schema and a required stdin payload
// are all waived, and still enforced without the flag.
func TestShortCircuit_readerWaivesChannelRequirements(t *testing.T) {
	cfg := writeConfig(t, "api:\n  other: x\n")
	reader := func() *InputReader {
		return NewInputReader(InputSettings{ConfigFiles: []ConfigFile{{
			Name: "app", Path: cfg, Format: "yaml",
			Schema: `{"type":"object","required":["must"]}`,
		}}})
	}

	rtx := NewContextFor(scChannelDef(), []string{"--help"})
	rtx.Stdin = strings.NewReader("")
	var in scChannelInputs
	if err := reader().Read(rtx, &in); err != nil {
		t.Fatalf("Read(--help) = %v, want every channel requirement waived", err)
	}
	if !in.App.Flags.Help {
		t.Error("Help not bound")
	}

	plain := NewContextFor(scChannelDef(), nil)
	plain.Stdin = strings.NewReader("")
	var in2 scChannelInputs
	if err := reader().Read(plain, &in2); err == nil {
		t.Fatal("Read(no flag) succeeded, want a requirement error")
	}
}

// TestShortCircuit_readerSkipsUnparseableConfig confirms a waived run still reports an env value
// it cannot read, but skips a configuration file that cannot be parsed or holds a value of the
// wrong type, so --help works in a broken directory.
func TestShortCircuit_readerSkipsUnparseableConfig(t *testing.T) {
	t.Run("env value of the wrong type", func(t *testing.T) {
		t.Setenv("PORT", "abc")
		rtx := NewContextFor(scChannelDef(), []string{"--help"})
		rtx.Stdin = strings.NewReader("")
		var in scChannelInputs
		if err := NewInputReader(InputSettings{}).Read(rtx, &in); err == nil {
			t.Fatal("Read = nil, want the coercion error kept")
		}
	})
	t.Run("malformed config file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "app.yaml")
		if err := os.WriteFile(path, []byte("api: [unclosed\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		rtx := NewContextFor(scChannelDef(), []string{"--help"})
		rtx.Stdin = strings.NewReader("")
		var in scChannelInputs
		err := NewInputReader(InputSettings{ConfigFiles: []ConfigFile{{Name: "app", Path: path, Format: "yaml"}}}).Read(rtx, &in)
		if err != nil {
			t.Fatalf("Read = %v, want the unparseable config file skipped", err)
		}
	})
	t.Run("config value of the wrong type", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "app.yaml")
		if err := os.WriteFile(path, []byte("count: abc\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		rtx := NewContextFor(scChannelDef(), []string{"--help"})
		rtx.Stdin = strings.NewReader("")
		var in struct {
			App struct {
				Flags struct {
					Help bool `rotini:"help"`
				}
				Config struct {
					Count int `rotini:"count" recon:"count"`
				}
			}
		}
		err := NewInputReader(InputSettings{ConfigFiles: []ConfigFile{{Name: "app", Path: path, Format: "yaml"}}}).Read(rtx, &in)
		if err != nil {
			t.Fatalf("Read = %v, want the config value skipped", err)
		}
	})
	t.Run("missing config_source path", func(t *testing.T) {
		t.Setenv("APP_CONFIG", filepath.Join(t.TempDir(), "absent.yaml"))
		rtx := NewContextFor(scChannelDef(), []string{"--help"})
		rtx.Stdin = strings.NewReader("")
		var in scChannelInputs
		err := NewInputReader(InputSettings{ConfigFiles: []ConfigFile{{
			Name: "app", Path: "app.yaml", PathFrom: &PathFromDef{Env: "APP_CONFIG"},
		}}}).Read(rtx, &in)
		if err != nil {
			t.Fatalf("Read = %v, want the missing config file skipped", err)
		}
	})
}

// brokenConfigDef is a root with a short-circuit --help and an int flag that falls back to a
// configuration key, the shape a root hook reading every source sees under --help.
func brokenConfigDef() Definition {
	return Definition{
		Name: "app", Handler: "App",
		Flags: []FlagDef{
			{Name: "help", Identifiers: []string{"--help"}, Type: "bool", ShortCircuit: true},
			{Name: "limit", Identifiers: []string{"--limit"}, Type: "int", Default: "5"},
		},
		Commands: []CommandDef{{Name: "sub", Handler: "AppSub"}},
	}
}

type brokenConfigInputs struct {
	App struct {
		Flags struct {
			Help  bool `rotini:"help"`
			Limit int  `rotini:"limit" recon:"list.limit"`
		}
		Arguments struct{}
	}
}

// TestShortCircuit_brokenConfigFallbackFlag confirms a fallback flag's broken configuration
// file, unparseable or holding a value of the wrong type, doesn't block --help, and the flag
// keeps its default.
func TestShortCircuit_brokenConfigFallbackFlag(t *testing.T) {
	for _, body := range []string{"defaults: [\n", "list:\n  limit: abc\n"} {
		path := filepath.Join(t.TempDir(), ".app.yaml")
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		reader := NewInputReader(InputSettings{ConfigFiles: []ConfigFile{{Name: "project", Path: path}}})
		for _, argv := range [][]string{{"--help"}, {"sub", "--help"}} {
			rtx := NewContextFor(brokenConfigDef(), argv)
			rtx.frame = 0
			var in brokenConfigInputs
			if err := reader.Read(rtx, &in); err != nil {
				t.Fatalf("%q with %q: Read = %v, want nil", body, argv, err)
			}
			if in.App.Flags.Limit != 5 {
				t.Errorf("%q with %q: Limit = %d, want the default 5", body, argv, in.App.Flags.Limit)
			}
		}
	}
}

// TestShortCircuit_brokenConfigWithoutWaiver confirms a normal run still reports the file.
func TestShortCircuit_brokenConfigWithoutWaiver(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".app.yaml")
	if err := os.WriteFile(path, []byte("defaults: [\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	reader := NewInputReader(InputSettings{ConfigFiles: []ConfigFile{{Name: "project", Path: path}}})
	rtx := NewContextFor(brokenConfigDef(), []string{"sub"})
	rtx.frame = 0
	var in brokenConfigInputs
	err := reader.Read(rtx, &in)
	var ie *InputError
	if !errors.As(err, &ie) || ie.Channel != channelConfig {
		t.Fatalf("Read = %v, want a config *InputError", err)
	}
	if _, ok := errors.AsType[*recon.ParseError](err); !ok {
		t.Errorf("Read = %v, want the recon parse error reachable", err)
	}
	if !strings.Contains(err.Error(), "could not parse configuration file ") || !strings.Contains(err.Error(), ".app.yaml:1:11: ") {
		t.Errorf("Read = %q, want the file named", err)
	}
}

// TestShortCircuit_brokenConfigKeepsOtherFiles confirms a waived run with one broken file still
// binds the other file's values.
func TestShortCircuit_brokenConfigKeepsOtherFiles(t *testing.T) {
	dir := t.TempDir()
	broken := filepath.Join(dir, "broken.yaml")
	good := filepath.Join(dir, "good.yaml")
	if err := os.WriteFile(broken, []byte("list: [\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(good, []byte("list:\n  limit: 9\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	reader := NewInputReader(InputSettings{ConfigFiles: []ConfigFile{
		{Name: "broken", Path: broken},
		{Name: "good", Path: good},
	}})
	rtx := NewContextFor(brokenConfigDef(), []string{"--help"})
	var in brokenConfigInputs
	if err := reader.Read(rtx, &in); err != nil {
		t.Fatalf("Read = %v, want nil", err)
	}
	if in.App.Flags.Limit != 9 {
		t.Errorf("Limit = %d, want 9 from the readable file", in.App.Flags.Limit)
	}
}

// TestShortCircuit_resolvedValuesStillBound confirms the waiver reads every channel: values that
// resolve are bound even though missing required ones are not reported.
func TestShortCircuit_resolvedValuesStillBound(t *testing.T) {
	t.Setenv("PORT", "8080")
	rtx := NewContextFor(scChannelDef(), []string{"--help"})
	rtx.Stdin = strings.NewReader("")
	var in scChannelInputs
	if err := NewInputReader(InputSettings{}).Read(rtx, &in); err != nil {
		t.Fatalf("Read: %v", err)
	}
	if in.App.Env.Port != 8080 {
		t.Errorf("Env.Port = %d, want 8080 bound alongside the waived required REGION", in.App.Env.Port)
	}
}

// TestShortCircuit_perChannelReaders confirms the per-channel layers apply the same waiver.
func TestShortCircuit_perChannelReaders(t *testing.T) {
	rtx := NewContextFor(scChannelDef(), []string{"--help"})
	rtx.Stdin = strings.NewReader("")
	if _, err := rtx.EnvInputs[scChannelInputs](); err != nil {
		t.Errorf("EnvInputs(--help) = %v, want the required env var waived", err)
	}
	if _, err := rtx.StdinInputs[scChannelInputs](); err != nil {
		t.Errorf("StdinInputs(--help) = %v, want the required payload waived", err)
	}
	plain := NewContextFor(scChannelDef(), nil)
	if _, err := plain.EnvInputs[scChannelInputs](); err == nil {
		t.Error("EnvInputs(no flag) = nil, want the required env var reported")
	}
}

// TestShortCircuit_completionStops confirms nothing is offered once a short-circuit flag is on
// the line, file fallback included, and that --help=false does not stop completion.
func TestShortCircuit_completionStops(t *testing.T) {
	def := scDef(t)
	rtx := NewContextFor(def, nil)
	if got := complete(def, []string{"--help", ""}, nil, rtx); len(got) != 0 {
		t.Errorf("complete after --help = %q, want nothing", got)
	}
	if hint := completionHintFor(def, []string{"--help", ""}); hint.Kind != "none" {
		t.Errorf("hint after --help = %+v, want kind none", hint)
	}
	if got := complete(def, []string{"--help=false", ""}, nil, rtx); len(got) == 0 {
		t.Error("complete after --help=false offered nothing, want the usual candidates")
	}
}

// TestShortCircuit_completionReadsBundles pins that a short-circuit flag inside a bundle of
// short flags (-xh) stops completion, as the parser reads it as set.
func TestShortCircuit_completionReadsBundles(t *testing.T) {
	def := Definition{
		Name: "app", Handler: "App",
		Flags: []FlagDef{
			{Name: "help", Identifiers: []string{"-h", "--help"}, Type: "bool", ShortCircuit: true},
			{Name: "x", Identifiers: []string{"-x"}, Type: "bool"},
			{Name: "out", Identifiers: []string{"-o"}, Type: "string"},
		},
		Commands: []CommandDef{{Name: "run", Handler: "AppRun"}},
	}
	for _, tt := range []struct {
		words []string
		stop  bool
	}{
		{[]string{"-xh", ""}, true},
		{[]string{"-hx", ""}, true},
		{[]string{"-x", ""}, false},
		{[]string{"-oh", ""}, false}, // h is -o's value here
	} {
		got := complete(def, tt.words, nil, nil)
		stopped := len(got) == 0 && completionHintFor(def, tt.words).Kind == "none"
		if stopped != tt.stop {
			t.Errorf("%q: candidates %q, stopped %v; want stopped %v", tt.words, got, stopped, tt.stop)
		}
	}
}

// scStrayDef is a root with short-circuit --help and --version, an author's own short-circuit
// --explain, a group command, and a leaf that takes one argument.
func scStrayDef() Definition {
	return Definition{
		Name: "app", Handler: "App",
		Flags: []FlagDef{
			{Name: "help", Identifiers: []string{"-h", "--help"}, Type: "bool", ShortCircuit: true},
			{Name: "version", Identifiers: []string{"--version"}, Type: "bool", ShortCircuit: true},
			{Name: "explain", Identifiers: []string{"--explain"}, Type: "bool", ShortCircuit: true},
		},
		Commands: []CommandDef{
			{Name: "add", Handler: "AppAdd", Arguments: []ArgDef{{Name: "title", Type: "string", Required: true}}},
			{Name: "config", Handler: "AppConfig", Commands: []CommandDef{
				{Name: "set", Handler: "AppConfigSet", Arguments: []ArgDef{{Name: "key", Type: "string", Required: true}}},
			}},
		},
	}
}

// TestContext_UnknownCommand pins the word a short-circuit flag let pass: the same
// path-prefixed unknown-command error the run reports without the flag, for --help, --version
// and an author's own flag, on the root and on a group; nil when everything resolved, when a
// leaf merely has too many arguments, and without a short-circuit flag.
func TestContext_UnknownCommand(t *testing.T) {
	tests := []struct {
		argv  []string
		token string // "" = nil expected
		msg   string
		cands []string
	}{
		{[]string{"nope", "--help"}, "nope", `app: unknown command "nope"`, []string{"add", "config"}},
		{[]string{"--help", "nope"}, "nope", `app: unknown command "nope"`, []string{"add", "config"}},
		{[]string{"nope", "-h"}, "nope", `app: unknown command "nope"`, []string{"add", "config"}},
		{[]string{"nope", "--version"}, "nope", `app: unknown command "nope"`, []string{"add", "config"}},
		{[]string{"nope", "--explain"}, "nope", `app: unknown command "nope"`, []string{"add", "config"}},
		{[]string{"config", "sett", "--help"}, "sett", `app config: unknown command "sett"`, []string{"set"}},
		{argv: []string{"--help"}},
		{argv: []string{"config", "--help"}},
		{argv: []string{"add", "a", "b", "--help"}},
		{argv: []string{"nope"}},
		{argv: []string{"nope", "--help=false"}},
		{argv: []string{"config", "sett"}},
	}
	for _, tt := range tests {
		err := NewContextFor(scStrayDef(), tt.argv).UnknownCommand()
		if tt.token == "" {
			if err != nil {
				t.Errorf("%q: UnknownCommand() = %v, want nil", tt.argv, err)
			}
			continue
		}
		pe, ok := errors.AsType[*ParseError](err)
		if !ok || pe.Kind != ParseKindUnknownCommand || pe.Token != tt.token || pe.Error() != tt.msg || !slices.Equal(pe.Candidates, tt.cands) {
			t.Errorf("%q: UnknownCommand() = %#v, want an unknown-command *ParseError %q for %q with candidates %q", tt.argv, err, tt.msg, tt.token, tt.cands)
		}
	}
}

type scStrayInputs struct {
	App struct {
		Flags struct {
			Help    bool `rotini:"help"`
			Version bool `rotini:"version"`
		}
		Arguments struct{}
	}
}

type scStrayHandlers struct{ optOut bool }

func (h scStrayHandlers) App() Handler        { return FromErrorHandler(&scStrayRoot{optOut: h.optOut}) }
func (scStrayHandlers) AppAdd() Handler       { return ocNoop{} }
func (scStrayHandlers) AppConfig() Handler    { return ocNoop{} }
func (scStrayHandlers) AppConfigSet() Handler { return ocNoop{} }

// scStrayRoot answers --help and --version the way the seeded root hook does; optOut is the
// same hook with the UnknownCommand check deleted.
type scStrayRoot struct {
	NoPreRunE
	NoPostRunE
	NoCascadingPostRunE
	optOut bool
}

func (h *scStrayRoot) CascadingPreRun(_ context.Context, rtx *Context) error {
	argv, err := rtx.ArgvInputs[scStrayInputs]()
	if err != nil {
		return err
	}
	if !h.optOut {
		if err := rtx.UnknownCommand(); err != nil {
			return err
		}
	}
	switch {
	case argv.Values.App.Flags.Help:
		page, err := rtx.InvokedHelp()
		if err != nil {
			return err
		}
		_, _ = io.WriteString(rtx.Stdout, page)
		rtx.HaltWithCode(0)
	case argv.Values.App.Flags.Version:
		_, _ = io.WriteString(rtx.Stdout, "app 1.0.0")
		rtx.HaltWithCode(0)
	}
	return nil
}

func (*scStrayRoot) Run(context.Context, *Context) error { return nil }

// TestShortCircuit_hookReportsUnknownCommand pins the seeded hook's shape: with the
// UnknownCommand check, `app nope --help` fails with exit 1 and the unknown-command error;
// without it, the hook answers for the root, as ArgvInputs and InvokedHelp report no error.
func TestShortCircuit_hookReportsUnknownCommand(t *testing.T) {
	run := func(optOut bool, argv ...string) (int, error, string) {
		var out bytes.Buffer
		p := NewProgram(scStrayDef(), scStrayHandlers{optOut: optOut}).
			WithHelp(func(path ...string) (string, error) { return "page:" + strings.Join(path, " "), nil }).
			WithStdout(&out).WithStderr(io.Discard).WithoutSignalHandling()
		code, err := p.Run(argv)
		return code, err, out.String()
	}
	for _, argv := range [][]string{{"nope", "--help"}, {"nope", "--version"}, {"config", "sett", "--help"}} {
		code, err, out := run(false, argv...)
		if code != 1 || !isUnknownCommand(err) || out != "" {
			t.Errorf("%q: exit %d, %v, printed %q; want 1 and the unknown-command error", argv, code, err, out)
		}
	}
	for argv, page := range map[string]string{"nope --help": "page:", "config sett --help": "page:config", "nope --version": "app 1.0.0"} {
		code, err, out := run(true, strings.Fields(argv)...)
		if code != 0 || err != nil || out != page {
			t.Errorf("opted out, %q: exit %d, %v, printed %q; want 0 and %q", argv, code, err, out, page)
		}
	}
	for argv, page := range map[string]string{"--help": "page:", "config --help": "page:config", "add a b --help": "page:add"} {
		code, err, out := run(false, strings.Fields(argv)...)
		if code != 0 || err != nil || out != page {
			t.Errorf("%q: exit %d, %v, printed %q; want 0 and %q", argv, code, err, out, page)
		}
	}
}

func isUnknownCommand(err error) bool {
	pe, ok := errors.AsType[*ParseError](err)
	return ok && pe.Kind == ParseKindUnknownCommand
}
