package rotinitest_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/go-rotini/rotini"
	"github.com/go-rotini/rotini/rotinitest"
)

type toolCI struct {
	Flags struct {
		Verbose bool `rotini:"verbose"`
	}
	Arguments struct{}
}

type toolInputs struct{ Tool toolCI }

type addCI struct {
	Flags struct {
		Count int    `rotini:"count"`
		Token string `rotini:"token"`
	}
	Arguments struct {
		Title string `rotini:"title"`
	}
	Env struct {
		Key string `rotini:"key" recon:"key" env:"TOOL_KEY"`
	}
	Config struct {
		Region string `rotini:"region" recon:"region"`
	}
}

type addInputs struct {
	Tool    toolCI
	ToolAdd addCI
}

type listCI struct {
	Flags struct {
		Code int  `rotini:"code"`
		Bad  bool `rotini:"bad"`
	}
	Arguments struct{}
}

type listInputs struct {
	Tool     toolCI
	ToolList listCI
}

type listOutput struct {
	Items []string `json:"items"`
}

type doc struct {
	Name string `json:"name"`
	N    int    `json:"n"`
}

type loadJSON struct {
	Tool     toolCI
	ToolLoad struct {
		Flags, Arguments struct{}
		Stdin            *doc `stdin:"json"`
	}
}

type loadYAML struct {
	Tool     toolCI
	ToolLoad struct {
		Flags, Arguments struct{}
		Stdin            *doc `stdin:"yaml"`
	}
}

type loadTOML struct {
	Tool     toolCI
	ToolLoad struct {
		Flags, Arguments struct{}
		Stdin            *doc `stdin:"toml"`
	}
}

type loadText struct {
	Tool     toolCI
	ToolLoad struct {
		Flags, Arguments struct{}
		Stdin            *string `stdin:"text"`
	}
}

type loadLines struct {
	Tool     toolCI
	ToolLoad struct {
		Flags, Arguments struct{}
		Stdin            *[]string `stdin:"lines"`
	}
}

func toolDef() rotini.Definition {
	return rotini.Definition{
		Name: "tool", Handler: "Tool",
		Flags:  []rotini.FlagDef{{Name: "verbose", Identifiers: []string{"--verbose"}, Type: "bool"}},
		Inputs: reflect.TypeFor[toolInputs](),
		Commands: []rotini.CommandDef{
			{
				Name: "add", Handler: "ToolAdd", Inputs: reflect.TypeFor[addInputs](),
				Flags: []rotini.FlagDef{
					{Name: "count", Identifiers: []string{"--count"}, Type: "int", Default: "1"},
					{Name: "token", Identifiers: []string{"--token"}, Type: "string", Secret: true},
				},
				Arguments: []rotini.ArgDef{{Name: "title", Type: "string"}},
			},
			{
				Name: "list", Handler: "ToolList", Inputs: reflect.TypeFor[listInputs](),
				Flags: []rotini.FlagDef{
					{Name: "code", Identifiers: []string{"--code"}, Type: "int"},
					{Name: "bad", Identifiers: []string{"--bad"}, Type: "bool"},
				},
				Output: &rotini.OutputDef{
					Type:   reflect.TypeFor[listOutput](),
					Schema: `{"type":"object","required":["items"],"properties":{"items":{"type":"array","items":{"type":"string"}}}}`,
				},
				ExitStatus: []rotini.ExitStatusDef{{Code: 0}, {Code: 3, Name: "empty", Summary: "nothing to list"}},
			},
			{Name: "load", Handler: "ToolLoad"},
		},
	}
}

type handler struct {
	rotini.NoHooks
	run func(rtx *rotini.Context)
}

func (h *handler) Run(_ context.Context, rtx *rotini.Context) { h.run(rtx) }

// read collects T, halting with code 2 on an error.
func read[T any](rtx *rotini.Context) (T, bool) {
	in, err := rtx.Inputs[T]()
	if err != nil {
		rtx.RecordError(err)
		rtx.HaltWithCode(2)
		return in, false
	}
	return in, true
}

// newTool builds a fresh program, as a test should for every run. load echoes a stdin payload
// of type T.
func newTool[T any]() *rotini.Program {
	handlers := map[string]func(rtx *rotini.Context){
		"Tool": func(*rotini.Context) {},
		"ToolAdd": func(rtx *rotini.Context) {
			in, ok := read[addInputs](rtx)
			if !ok {
				return
			}
			home, _ := rtx.LookupEnv("HOME")
			fmt.Fprintf(rtx.Stdout, "count=%d title=%s token=%s key=%s home=%t",
				in.ToolAdd.Flags.Count, in.ToolAdd.Arguments.Title, in.ToolAdd.Flags.Token,
				in.ToolAdd.Env.Key, home != os.Getenv("HOME"))
		},
		"ToolList": func(rtx *rotini.Context) {
			in, ok := read[listInputs](rtx)
			if !ok {
				return
			}
			if in.ToolList.Flags.Code != 0 {
				rtx.HaltWithCode(in.ToolList.Flags.Code)
				return
			}
			out := listOutput{Items: []string{"a", "b"}}
			if in.ToolList.Flags.Bad {
				out.Items = nil
			}
			if err := rtx.WriteOutput(out, "json", nil); err != nil {
				rtx.RecordError(err)
				rtx.HaltWithCode(1)
			}
		},
		"ToolLoad": func(rtx *rotini.Context) {
			in, ok := read[T](rtx)
			if !ok {
				return
			}
			v := reflect.ValueOf(in).Field(1).FieldByName("Stdin").Elem().Interface()
			b, _ := json.Marshal(v)
			fmt.Fprint(rtx.Stdout, string(b))
		},
	}
	return rotini.NewProgramFunc(toolDef(), func(name string) (rotini.Handler, bool) {
		run, ok := handlers[name]
		return &handler{run: run}, ok
	}).WithInputSettings(rotini.InputSettings{EnvPrefix: "TOOL"})
}

// fakeTB records a test failure instead of failing; Fatalf ends the calling goroutine, as the
// real one does.
type fakeTB struct {
	testing.TB

	failed bool
	msg    string
}

func (f *fakeTB) Helper() {}

func (f *fakeTB) Errorf(format string, args ...any) {
	f.failed, f.msg = true, fmt.Sprintf(format, args...)
}

func (f *fakeTB) Fatalf(format string, args ...any) {
	f.Errorf(format, args...)
	runtime.Goexit()
}

// failure runs fn against a fakeTB and returns the failure message, "" when it passed.
func failure(t *testing.T, fn func(tb testing.TB)) string {
	t.Helper()
	f := &fakeTB{TB: t}
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn(f)
	}()
	<-done
	if !f.failed {
		return ""
	}
	return f.msg
}

func TestRun(t *testing.T) {
	t.Parallel()
	t.Run("argv, streams and code", func(t *testing.T) {
		t.Parallel()
		var in addInputs
		in.Tool.Flags.Verbose = true
		in.ToolAdd.Flags.Count = 3
		in.ToolAdd.Arguments.Title = "--not-a-flag"
		res := rotinitest.Run(t, newTool[loadJSON](), in)
		if res.Code != 0 || res.Err != nil {
			t.Fatalf("exit %d: %v\n%s", res.Code, res.Err, res.Stderr)
		}
		if want := []string{"--verbose", "add", "--count=3", "--", "--not-a-flag"}; !slices.Equal(res.Argv, want) {
			t.Errorf("argv = %q, want %q", res.Argv, want)
		}
		if got := string(res.Stdout); !strings.HasPrefix(got, "count=3 title=--not-a-flag token= key= home=true") {
			t.Errorf("stdout = %q", got)
		}
	})
	t.Run("env inputs go into the run's own environment", func(t *testing.T) {
		t.Parallel()
		var in addInputs
		in.ToolAdd.Env.Key = "k1"
		res := rotinitest.Run(t, newTool[loadJSON](), in, rotinitest.Env("EXTRA=1"))
		if !strings.Contains(string(res.Stdout), "key=k1") {
			t.Errorf("stdout = %q", res.Stdout)
		}
		if !slices.Contains(res.Env, "TOOL_KEY=k1") || !slices.Contains(res.Env, "EXTRA=1") {
			t.Errorf("env = %q", res.Env)
		}
		if !slices.ContainsFunc(res.Env, func(kv string) bool { return strings.HasPrefix(kv, "XDG_CONFIG_HOME=") }) {
			t.Errorf("env = %q, want XDG_CONFIG_HOME set", res.Env)
		}
	})
	t.Run("an explicit zero", func(t *testing.T) {
		t.Parallel()
		var in addInputs
		res := rotinitest.Run(t, newTool[loadJSON](), in,
			rotinitest.Presence(rotini.Presence{"ToolAdd.Flags.Count": {}}))
		if !strings.HasPrefix(string(res.Stdout), "count=0 ") {
			t.Errorf("stdout = %q, want count=0 over the default 1", res.Stdout)
		}
	})
	t.Run("secrets", func(t *testing.T) {
		t.Parallel()
		var in addInputs
		in.ToolAdd.Flags.Token = "s3cret"
		msg := failure(t, func(tb testing.TB) { rotinitest.Run(tb, newTool[loadJSON](), in) })
		if !strings.Contains(msg, "secret") {
			t.Errorf("failure = %q, want a refused secret", msg)
		}
		res := rotinitest.Run(t, newTool[loadJSON](), in, rotinitest.Secrets())
		if !strings.Contains(string(res.Stdout), "token=s3cret") {
			t.Errorf("stdout = %q", res.Stdout)
		}
	})
	t.Run("a config input fails the test", func(t *testing.T) {
		t.Parallel()
		var in addInputs
		in.ToolAdd.Config.Region = "eu"
		if msg := failure(t, func(tb testing.TB) { rotinitest.Run(tb, newTool[loadJSON](), in) }); !strings.Contains(msg, "configuration file") {
			t.Errorf("failure = %q", msg)
		}
	})
	t.Run("an unknown inputs type fails the test", func(t *testing.T) {
		t.Parallel()
		type stray struct{ Tool toolCI }
		if msg := failure(t, func(tb testing.TB) { rotinitest.Run(tb, newTool[loadJSON](), stray{}) }); !strings.Contains(msg, "rotinitest.Path") {
			t.Errorf("failure = %q", msg)
		}
	})
}

// TestRunIsolated shows a run never sees the test process's environment.
func TestRunIsolated(t *testing.T) {
	t.Setenv("TOOL_KEY", "leaked")
	res := rotinitest.Run(t, newTool[loadJSON](), addInputs{})
	if !strings.Contains(string(res.Stdout), "key= ") {
		t.Errorf("stdout = %q, want no key from the process environment", res.Stdout)
	}
}

func TestRunStdin(t *testing.T) {
	t.Parallel()
	d := &doc{Name: "x", N: 2}
	cases := []struct {
		name string
		run  func(t *testing.T) rotinitest.Result
		want string
	}{
		{"json", func(t *testing.T) rotinitest.Result {
			var in loadJSON
			in.ToolLoad.Stdin = d
			return rotinitest.Run(t, newTool[loadJSON](), in, rotinitest.Path("load"))
		}, `{"name":"x","n":2}`},
		{"yaml", func(t *testing.T) rotinitest.Result {
			var in loadYAML
			in.ToolLoad.Stdin = d
			return rotinitest.Run(t, newTool[loadYAML](), in, rotinitest.Path("load"))
		}, `{"name":"x","n":2}`},
		{"toml", func(t *testing.T) rotinitest.Result {
			var in loadTOML
			in.ToolLoad.Stdin = d
			return rotinitest.Run(t, newTool[loadTOML](), in, rotinitest.Path("load"))
		}, `{"name":"x","n":2}`},
		{"text", func(t *testing.T) rotinitest.Result {
			var in loadText
			in.ToolLoad.Stdin = new("hello\nworld")
			return rotinitest.Run(t, newTool[loadText](), in, rotinitest.Path("load"))
		}, `"hello\nworld"`},
		{"lines", func(t *testing.T) rotinitest.Result {
			var in loadLines
			in.ToolLoad.Stdin = &[]string{"a", "b"}
			return rotinitest.Run(t, newTool[loadLines](), in, rotinitest.Path("load"))
		}, `["a","b"]`},
		{"the Stdin option wins", func(t *testing.T) rotinitest.Result {
			var in loadText
			in.ToolLoad.Stdin = new("ignored")
			return rotinitest.Run(t, newTool[loadText](), in, rotinitest.Path("load"), rotinitest.Stdin(strings.NewReader("given")))
		}, `"given"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			res := tc.run(t)
			if res.Code != 0 || string(res.Stdout) != tc.want {
				t.Fatalf("exit %d, stdout %s, want %s\n%s", res.Code, res.Stdout, tc.want, res.Stderr)
			}
		})
	}
}

func TestOutput(t *testing.T) {
	t.Parallel()
	res := rotinitest.Run(t, newTool[loadJSON](), listInputs{})
	if out := rotinitest.Output[listOutput](t, res); !slices.Equal(out.Items, []string{"a", "b"}) {
		t.Errorf("output = %+v", out)
	}

	res.Stdout = []byte(`{"items":"a"}`)
	if msg := failure(t, func(tb testing.TB) { rotinitest.Output[listOutput](tb, res) }); msg == "" {
		t.Error("an output off its schema decoded without failing")
	}
	if msg := failure(t, func(tb testing.TB) { rotinitest.OutputAs[listOutput](tb, res, "csv") }); msg == "" {
		t.Error("an unknown format decoded without failing")
	}

	// Output checks are on: an output off its schema is the program's bug, and isn't written.
	var bad listInputs
	bad.ToolList.Flags.Bad = true
	if res := rotinitest.Run(t, newTool[loadJSON](), bad); res.Code == 0 || len(res.Stdout) != 0 {
		t.Errorf("exit %d, stdout %q: want a failed check", res.Code, res.Stdout)
	}
}

func TestExitDocumented(t *testing.T) {
	t.Parallel()
	list := func(code int) rotinitest.Result {
		var in listInputs
		in.ToolList.Flags.Code = code
		return rotinitest.Run(t, newTool[loadJSON](), in)
	}
	if msg := failure(t, func(tb testing.TB) { rotinitest.ExitDocumented(tb, list(0)) }); msg != "" {
		t.Errorf("0: %s", msg)
	}
	if msg := failure(t, func(tb testing.TB) { rotinitest.ExitDocumented(tb, list(3)) }); msg != "" {
		t.Errorf("3: %s", msg)
	}
	if msg := failure(t, func(tb testing.TB) { rotinitest.ExitDocumented(tb, list(4)) }); !strings.Contains(msg, "tool list exited 4, which its exit_status doesn't list (0, 3 (empty))") {
		t.Errorf("4: %q", msg)
	}
	// add documents nothing, so only 0 passes.
	var in addInputs
	in.ToolAdd.Arguments.Title = "x"
	ok := rotinitest.Run(t, newTool[loadJSON](), in)
	if msg := failure(t, func(tb testing.TB) { rotinitest.ExitDocumented(tb, ok) }); msg != "" {
		t.Errorf("add 0: %s", msg)
	}
	ok.Code = 2 // as a failed run would
	if msg := failure(t, func(tb testing.TB) { rotinitest.ExitDocumented(tb, ok) }); !strings.Contains(msg, "declares no exit_status") {
		t.Errorf("add 2: %q", msg)
	}
}

// TestRunDir shows a config file written to the run's directory before the run.
func TestRunDir(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "marker"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	res := rotinitest.Run(t, newTool[loadJSON](), toolInputs{}, rotinitest.Dir(dir))
	if res.Dir != dir {
		t.Errorf("dir = %q, want %q", res.Dir, dir)
	}
}
