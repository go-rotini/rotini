package rotini

import (
	"errors"
	"io"
	"reflect"
	"slices"
	"testing"
)

// testLookup is testHandlers' generated-style lookup: a switch over the handler names.
func testLookup(t *testHandlers) HandlerLookup {
	return func(name string) (Handler, bool) {
		switch name {
		case "App":
			return t.App(), true
		case "AppRun":
			return t.AppRun(), true
		}
		return nil, false
	}
}

func runFunc(t *testing.T, lookup HandlerLookup, argv ...string) (int, error) {
	t.Helper()
	p := NewProgramFunc(testDef(), lookup).WithStdout(io.Discard).WithStderr(io.Discard)
	return p.Run(argv)
}

func TestNewProgramFunc_dispatches(t *testing.T) {
	log := new([]string)
	code, err := runFunc(t, testLookup(&testHandlers{log: log}), "run", "x")
	if code != 0 || err != nil {
		t.Fatalf("Run = %d, %v; want 0, nil", code, err)
	}
	if !slices.Contains(*log, "run.Run") {
		t.Errorf("hooks run = %v, want run.Run among them", *log)
	}
}

// TestNewProgramFunc_parity runs the same invocations through both constructors: the lookup
// and the reflective handler set must produce the same hooks, codes and errors.
func TestNewProgramFunc_parity(t *testing.T) {
	for _, argv := range [][]string{
		{"run", "x"},
		{"r", "x", "y", "-c", "2"},
		{"-v", "run"},
		{},
		{"nope"},
	} {
		viaReflect, viaLookup := new([]string), new([]string)
		p := NewProgram(testDef(), &testHandlers{log: viaReflect}).WithStdout(io.Discard).WithStderr(io.Discard)
		rc, rerr := p.Run(argv)
		lc, lerr := runFunc(t, testLookup(&testHandlers{log: viaLookup}), argv...)
		if rc != lc || (rerr == nil) != (lerr == nil) || (rerr != nil && rerr.Error() != lerr.Error()) {
			t.Errorf("%v: NewProgram = %d, %v; NewProgramFunc = %d, %v", argv, rc, rerr, lc, lerr)
		}
		if !slices.Equal(*viaReflect, *viaLookup) {
			t.Errorf("%v: hooks differ:\n reflect %v\n lookup  %v", argv, *viaReflect, *viaLookup)
		}
	}
}

func TestNewProgramFunc_wiringErrors(t *testing.T) {
	cases := []struct {
		name   string
		lookup HandlerLookup
		want   string
	}{
		{
			"unknown name",
			func(name string) (Handler, bool) {
				if name == "App" {
					return &recHandler{name: "app", log: new([]string)}, true
				}
				return nil, false
			},
			`no handler for command "run" (missing method "AppRun")`,
		},
		{
			"nil handler",
			func(string) (Handler, bool) { return nil, true },
			`handler "App" does not implement Handler`,
		},
		{"nil lookup", nil, "no handlers: NewProgramFunc was given a nil lookup"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, err := func() (n int, e error) {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("Run panicked instead of reporting a fault: %v", r)
					}
				}()
				return runFunc(t, tc.lookup, "run", "x")
			}()
			if code == 0 {
				t.Error("exit code = 0, want non-zero")
			}
			werr, ok := errors.AsType[*WiringError](err)
			if !ok {
				t.Fatalf("error is %T (%v), want a *WiringError", err, err)
			}
			if werr.Msg != tc.want {
				t.Errorf("message = %q, want %q", werr.Msg, tc.want)
			}
		})
	}
}

// TestNewProgram_nilHandlersMessage pins the reflective constructor's own nil message.
func TestNewProgram_nilHandlersMessage(t *testing.T) {
	_, err := NewProgram(testDef(), nil).WithStdout(io.Discard).WithStderr(io.Discard).Run([]string{"run"})
	werr, ok := errors.AsType[*WiringError](err)
	if !ok || werr.Msg != "no handlers: NewProgram was given a nil handlers value" {
		t.Errorf("error = %v, want the NewProgram nil-handlers wiring error", err)
	}
}

// TestNewProgramFunc_completers: dynamic completers resolve through the lookup, and a handler
// without a completer, an unknown name or a nil lookup falls back to the static enum.
func TestNewProgramFunc_completers(t *testing.T) {
	def := completionFixtureDef()
	withCompleter := HandlerLookup(func(name string) (Handler, bool) {
		if name == "AppDeploy" {
			return deployArgCompleter{}, true
		}
		return nil, false
	})
	plain := HandlerLookup(func(string) (Handler, bool) { return &recHandler{log: new([]string)}, true })

	if got := complete(def, []string{"deploy", "dyn"}, withCompleter, nil); !reflect.DeepEqual(got, []string{"dyn-one", "dyn-two"}) {
		t.Errorf("dynamic candidates = %v, want [dyn-one dyn-two]", got)
	}
	for name, lookup := range map[string]HandlerLookup{"no completer": plain, "nil lookup": nil} {
		if got := complete(def, []string{"deploy", "w"}, lookup, nil); !reflect.DeepEqual(got, []string{"web", "worker"}) {
			t.Errorf("%s: candidates = %v, want the enum [web worker]", name, got)
		}
	}
	unknown := HandlerLookup(func(string) (Handler, bool) { return nil, false })
	if got := complete(def, []string{"deploy", "w"}, unknown, nil); !reflect.DeepEqual(got, []string{"web", "worker"}) {
		t.Errorf("unknown name: candidates = %v, want the enum [web worker]", got)
	}
}
