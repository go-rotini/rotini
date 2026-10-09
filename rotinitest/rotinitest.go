// Package rotinitest runs a rotini program from a test with typed inputs, and checks what it
// wrote and how it exited. Production code never imports it, so `testing` stays out of the CLI's
// binary.
//
//	func TestAdd(t *testing.T) {
//		t.Parallel()
//		var in cmd.TaskrAddInputs
//		in.TaskrAdd.Arguments.Title = "write docs"
//		res := rotinitest.Run(t, cmd.NewProgram(cmd.Handlers()), in)
//		rotinitest.ExitDocumented(t, res)
//		if out := rotinitest.Output[cmd.TaskrAddOutput](t, res); out.Title != "write docs" {
//			t.Errorf("added %+v", out)
//		}
//	}
//
// [Run] writes the inputs value as a command line with [rotini.ArgvOf], so the program parses
// it exactly as it would a user's. Each run gets its own environment and working directory
// ([rotini.Program.WithEnviron], [rotini.Program.WithDir]): only the variables the inputs set,
// plus HOME and XDG_CONFIG_HOME pointing into a temporary directory. A developer's own variables
// and configuration files never leak in, and tests can call t.Parallel.
//
// What it doesn't do:
//   - Pass a fresh program to every call, built as main builds it (with the dependencies main
//     adds). Run sets its streams and environment, so a program shared between tests, such as
//     the generated package's Program variable, leaks state between them.
//   - Config inputs come from configuration files the test writes into the run's directory
//     ([Dir]) or its XDG_CONFIG_HOME; putting one in the inputs value fails the test.
//   - Command lines that typed inputs can't express (a typo, an unknown flag) are tested with
//     [rotini.Program.Run] directly.
//   - Handlers still run in the test process's working directory; see [rotini.Program.WithDir].
package rotinitest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/go-rotini/recon"
	"github.com/go-rotini/rotini"
)

// Result is what a [Run] produced.
type Result struct {
	Code           int    // the exit code
	Err            error  // the error the run reported, nil on success
	Stdout, Stderr []byte // what the program wrote
	Argv           []string
	Env            []string // the run's whole environment
	Dir            string   // the run's working directory

	program *rotini.Program
	command string                 // the invoked command's path, for messages
	exits   []rotini.ExitStatusDef // the invoked command's documented exit codes
}

// Option configures [Run].
type Option func(*config)

type config struct {
	set     rotini.Presence
	argvOpt []rotini.ArgvOption
	stdin   io.Reader
	ctx     context.Context
	env     []string
	dir     string
	path    []string
	hasPath bool
}

// Presence names the inputs [Run] writes, in place of [rotini.PresenceOf] of the inputs value,
// which leaves zero values out. Name a field to write its zero value (--count=0).
func Presence(set rotini.Presence) Option {
	return func(c *config) { c.set = set }
}

// Secrets lets [Run] write secret flags and arguments into argv; see [rotini.ArgvSecrets].
func Secrets() Option {
	return func(c *config) { c.argvOpt = append(c.argvOpt, rotini.ArgvSecrets()) }
}

// Stdin is the run's standard input, in place of the payload in the inputs value's Stdin field.
func Stdin(r io.Reader) Option {
	return func(c *config) { c.stdin = r }
}

// Context is the run's context, in place of the test's (t.Context).
func Context(ctx context.Context) Option {
	return func(c *config) { c.ctx = ctx }
}

// Env adds variables to the run's environment, as KEY=value; PATH for a test that runs plugins,
// for one. The inputs value's own env inputs win.
func Env(kv ...string) Option {
	return func(c *config) { c.env = append(c.env, kv...) }
}

// Dir is the run's working directory, in place of a new temporary one; a test that writes a
// configuration file there first passes it.
func Dir(dir string) Option {
	return func(c *config) { c.dir = dir }
}

// Path names the command the inputs value belongs to; see [rotini.ArgvPath]. A generated
// program records each command's inputs type, so only a hand-built Definition needs it.
func Path(names ...string) Option {
	return func(c *config) { c.path, c.hasPath = names, true }
}

// Run runs p with the command line and environment that supply in, the generated inputs type of
// the command to run, with output checks on ([rotini.Program.WithOutputChecks]). Only in's set
// fields are written ([Presence] to choose). A value no command line can express fails the
// test. p must be a fresh program: Run sets its streams, environment and directory.
func Run[T any](tb testing.TB, p *rotini.Program, in T, opts ...Option) Result {
	tb.Helper()
	var cfg config
	for _, o := range opts {
		if o != nil {
			o(&cfg)
		}
	}
	def := p.Definition()
	path, ok := cfg.path, cfg.hasPath
	if !ok {
		if path, ok = commandPath(def, reflect.TypeFor[T]()); !ok {
			tb.Fatalf("rotinitest: no command of %q has the inputs type %s; regenerate, or pass rotinitest.Path", def.Name, reflect.TypeFor[T]())
		}
	}

	set := cfg.set
	if set == nil {
		set = rotini.PresenceOf(in)
	}
	argvSet := rotini.Presence{}
	for p, src := range set {
		if !strings.HasSuffix(string(p), ".Stdin") {
			argvSet[p] = src
		}
	}
	argvOpts := append(slices.Clone(cfg.argvOpt), rotini.ArgvPath(path...))
	argv, inputEnv, err := rotini.ArgvOf(def, in, argvSet, argvOpts...)
	if err != nil {
		tb.Fatalf("rotinitest: %v", err)
	}

	base := tb.TempDir()
	home := filepath.Join(base, "home")
	dir := cfg.dir
	if dir == "" {
		dir = filepath.Join(base, "work")
	}
	for _, d := range []string{home, dir} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			tb.Fatalf("rotinitest: %v", err)
		}
	}
	env := make([]string, 0, 3+len(cfg.env)+len(inputEnv))
	env = append(env, "HOME="+home, "USERPROFILE="+home, "XDG_CONFIG_HOME="+filepath.Join(home, ".config"))
	env = append(env, cfg.env...)
	env = append(env, inputEnv...)

	stdin := cfg.stdin
	if stdin == nil {
		payload, err := stdinPayload(in)
		if err != nil {
			tb.Fatalf("rotinitest: %v", err)
		}
		stdin = bytes.NewReader(payload)
	}
	ctx := cfg.ctx
	if ctx == nil {
		ctx = tb.Context()
	}

	var stdout, stderr bytes.Buffer
	p.WithStdin(stdin).WithStdout(&stdout).WithStderr(&stderr).
		WithEnviron(env).WithDir(dir).WithOutputChecks(true)
	code, runErr := p.RunContext(ctx, argv)
	return Result{
		Code: code, Err: runErr,
		Stdout: stdout.Bytes(), Stderr: stderr.Bytes(),
		Argv: argv, Env: env, Dir: dir,
		program: p,
		command: strings.Join(append([]string{def.Name}, path...), " "),
		exits:   exitStatus(def, path),
	}
}

// Output decodes the run's stdout as JSON into T, the generated output type of the command
// that ran, and checks it against the command's declared schema; see [rotini.DecodeOutput]. A
// failure fails the test.
func Output[T any](tb testing.TB, res Result) T {
	tb.Helper()
	return OutputAs[T](tb, res, "json")
}

// OutputAs is [Output] for stdout written in format: json, yaml or toml.
func OutputAs[T any](tb testing.TB, res Result, format string) T {
	tb.Helper()
	out, err := rotini.DecodeOutput[T](res.program, res.Stdout, format)
	if err != nil {
		tb.Fatalf("rotinitest: %s: %v\nstdout:\n%s", res.command, err, res.Stdout)
	}
	return out
}

// ExitDocumented fails the test unless the run's exit code is one its command documents in its
// spec's exit_status. 0 always passes. A command that documents no exit codes fails on any
// other: there is nothing to check the code against. Only the invoked command's own list counts.
func ExitDocumented(tb testing.TB, res Result) {
	tb.Helper()
	if res.Code == 0 {
		return
	}
	if len(res.exits) == 0 {
		tb.Errorf("rotinitest: %s exited %d, and declares no exit_status to check it against", res.command, res.Code)
		return
	}
	codes := make([]string, len(res.exits))
	for i, e := range res.exits {
		if e.Code == res.Code {
			return
		}
		codes[i] = strconv.Itoa(e.Code)
		if e.Name != "" {
			codes[i] += " (" + e.Name + ")"
		}
	}
	tb.Errorf("rotinitest: %s exited %d, which its exit_status doesn't list (%s); error: %v",
		res.command, res.Code, strings.Join(codes, ", "), res.Err)
}

// commandPath finds the command whose recorded inputs type is t, as its path below the root.
func commandPath(def rotini.Definition, t reflect.Type) ([]string, bool) {
	if def.Inputs == t {
		return nil, true
	}
	var find func(cmds []rotini.CommandDef, prefix []string) ([]string, bool)
	find = func(cmds []rotini.CommandDef, prefix []string) ([]string, bool) {
		for _, c := range cmds {
			p := append(slices.Clone(prefix), c.Name)
			if c.Inputs == t {
				return p, true
			}
			if got, ok := find(c.Commands, p); ok {
				return got, true
			}
		}
		return nil, false
	}
	return find(def.Commands, nil)
}

// exitStatus is the exit codes the command at path documents.
func exitStatus(def rotini.Definition, path []string) []rotini.ExitStatusDef {
	exits, cmds := def.ExitStatus, def.Commands
	for _, name := range path {
		i := slices.IndexFunc(cmds, func(c rotini.CommandDef) bool {
			return c.Name == name || slices.Contains(c.Aliases, name)
		})
		if i < 0 {
			return nil
		}
		exits, cmds = cmds[i].ExitStatus, cmds[i].Commands
	}
	return exits
}

// stdinPayload encodes the inputs value's stdin payload, the invoked command's Stdin field, in
// the format its tag names; none is empty input.
func stdinPayload(in any) ([]byte, error) {
	v := reflect.ValueOf(in)
	if v.Kind() != reflect.Struct || v.NumField() == 0 {
		return nil, nil
	}
	leaf := v.Field(v.NumField() - 1)
	if leaf.Kind() != reflect.Struct {
		return nil, nil
	}
	sf, ok := leaf.Type().FieldByName("Stdin")
	if !ok {
		return nil, nil
	}
	f := leaf.FieldByIndex(sf.Index)
	if f.Kind() == reflect.Pointer {
		if f.IsNil() {
			return nil, nil
		}
		f = f.Elem()
	}
	format, _, _ := strings.Cut(sf.Tag.Get("stdin"), ",")
	switch format {
	case "text":
		return []byte(f.String()), nil
	case "lines":
		lines, _ := reflect.TypeAssert[[]string](f)
		return []byte(strings.Join(lines, "\n")), nil
	}
	data, err := json.Marshal(f.Interface())
	if err != nil {
		return nil, fmt.Errorf("encode the stdin payload: %w", err)
	}
	if format == "json" || format == "yaml" { // JSON is YAML
		return data, nil
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("the stdin payload must be an object to write as %s: %w", format, err)
	}
	codec, ok := recon.DefaultCodecs().ByName(format)
	if !ok {
		return nil, fmt.Errorf("no encoder for the stdin format %q", format)
	}
	b, err := codec.Encode(doc)
	if err != nil {
		return nil, fmt.Errorf("encode the stdin payload as %s: %w", format, err)
	}
	return b, nil
}
