package rotini

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestSplitArgs(t *testing.T) {
	cases := []struct {
		line string
		want []string
	}{
		{"", nil},
		{"   ", nil},
		{"build", []string{"build"}},
		{"build  --out  dist", []string{"build", "--out", "dist"}},
		{`build --msg "hello world"`, []string{"build", "--msg", "hello world"}},
		{`build --msg 'single quoted'`, []string{"build", "--msg", "single quoted"}},
		{`build --path a\ b`, []string{"build", "--path", "a b"}},
		{`say "it's fine"`, []string{"say", "it's fine"}},
		{`empty "" arg`, []string{"empty", "", "arg"}},  // an explicit empty token survives
		{`x '\n literal'`, []string{"x", `\n literal`}}, // single quotes do not escape
	}
	for _, tc := range cases {
		if got := splitArgs(tc.line); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("splitArgs(%q) = %#v, want %#v", tc.line, got, tc.want)
		}
	}
}

// replProgram builds a Program over the shared test definition whose run handler
// records what it saw, so a REPL test can assert on dispatch.
func replProgram(t *testing.T, onRun func(rtx *Context)) (*Program, *bytes.Buffer) {
	t.Helper()
	out := &bytes.Buffer{}
	h := &testHandlers{log: new([]string), onRun: onRun}
	p := NewProgram(testDef(), h).WithExit(func(int) { t.Error("REPL must never exit the process") })
	p.stdout, p.stderr = out, out
	return p, out
}

// Each typed line dispatches against the same Definition the binary uses.
func TestREPL_dispatchesEachLine(t *testing.T) {
	var seen []string
	p, _ := replProgram(t, func(rtx *Context) { seen = append(seen, strings.Join(rtx.Argv, " ")) })
	out := &bytes.Buffer{}

	err := NewREPL(p).
		WithInput(strings.NewReader("run alpha\nrun beta\n")).
		WithOutput(out).
		Run(context.Background())
	if err != nil {
		t.Fatalf("Run = %v, want nil", err)
	}
	if len(seen) != 2 || !strings.Contains(seen[0], "alpha") || !strings.Contains(seen[1], "beta") {
		t.Errorf("dispatched %v, want two runs with alpha then beta", seen)
	}
	if strings.Count(out.String(), "> ") != 3 { // two lines plus the prompt before EOF
		t.Errorf("prompt not written per line: %q", out.String())
	}
}

// The re-entrancy contract, from the REPL's side: one line's failure must not
// poison the next.
func TestREPL_runsAreIsolated(t *testing.T) {
	var n int
	p, progOut := replProgram(t, func(rtx *Context) {
		n++
		if n == 1 {
			rtx.RecordError(errors.New("first line failed"))
			rtx.HaltWithCode(2)
		}
	})

	err := NewREPL(p).WithPrompt("").WithErrorEcho(false).
		WithInput(strings.NewReader("run a\nrun b\n")).
		WithOutput(&bytes.Buffer{}).
		Run(context.Background())
	if err != nil {
		t.Fatalf("Run = %v, want nil — a failing command must not end the loop", err)
	}
	if n != 2 {
		t.Errorf("ran %d lines, want 2 — the loop stopped after the failure", n)
	}
	if got := progOut.String(); strings.Count(got, "first line failed") != 1 {
		t.Errorf("the first line's error was reported %d times:\n%s", strings.Count(got, "first line failed"), got)
	}
}

func TestREPL_exitCommands(t *testing.T) {
	for _, word := range []string{"exit", "quit", "EXIT"} {
		var n int
		p, _ := replProgram(t, func(*Context) { n++ })
		if err := NewREPL(p).WithPrompt("").
			WithInput(strings.NewReader(word + "\nrun never\n")).
			WithOutput(&bytes.Buffer{}).
			Run(context.Background()); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Errorf("%q did not end the loop before the next line", word)
		}
	}
}

func TestREPL_customExitCommandsAndBlankLines(t *testing.T) {
	var n int
	p, _ := replProgram(t, func(*Context) { n++ })
	err := NewREPL(p).WithPrompt("").WithExitCommands("bye").
		WithInput(strings.NewReader("\n   \nrun x\nbye\nrun never\n")).
		WithOutput(&bytes.Buffer{}).
		Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("ran %d commands, want 1 (blank lines skipped, custom exit honored)", n)
	}
}

// End of input closes the session cleanly — a piped script is a valid REPL run.
func TestREPL_endOfInputIsCleanExit(t *testing.T) {
	p, _ := replProgram(t, func(*Context) {})
	if err := NewREPL(p).WithPrompt("").WithInput(strings.NewReader("")).
		WithOutput(&bytes.Buffer{}).Run(context.Background()); err != nil {
		t.Errorf("empty input = %v, want a clean nil exit", err)
	}
}

func TestREPL_contextCancellationEndsCleanly(t *testing.T) {
	p, _ := replProgram(t, func(*Context) {})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan error, 1)
	go func() {
		done <- NewREPL(p).WithPrompt("").WithInput(blockingReader{}).
			WithOutput(&bytes.Buffer{}).Run(ctx)
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("canceled REPL = %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("REPL did not return on a canceled context")
	}
}

// A REPL inherits the program's streams, so no wiring is needed for the common case.
func TestREPL_defaultsToProgramStreams(t *testing.T) {
	p, _ := replProgram(t, func(*Context) {})
	p.stdin = strings.NewReader("run x\n")
	r := NewREPL(p)
	if r.in == nil || r.out == nil {
		t.Fatal("REPL did not inherit the program's streams")
	}
}

func TestREPL_requiresAProgramAndInput(t *testing.T) {
	if err := NewREPL(nil).Run(context.Background()); !errors.Is(err, ErrInternal) {
		t.Errorf("REPL with no program = %v, want an internal error", err)
	}
	p, _ := replProgram(t, func(*Context) {})
	if err := NewREPL(p).WithInput(nil).Run(context.Background()); !errors.Is(err, ErrUsage) {
		t.Errorf("REPL with no input = %v, want a usage error", err)
	}
}

// TestREPL_errorEchoDefaultsOff pins the pairing of two defaults that used not to compose.
//
// rotini's default funnel prints every recorded error, and the REPL's echo defaulted to on, so
// an out-of-the-box program in an out-of-the-box REPL printed each failure twice — once on
// stderr from the funnel, once on stdout from the echo, which in a terminal is one destination.
// The knob's own doc pointed at the right idea with the wrong test ("the same stream"): stream
// identity is not what matters, whether the funnel already reports is.
func TestREPL_errorEchoDefaultsOff(t *testing.T) {
	t.Parallel()
	failing := errors.New("that did not work")

	newProgram := func(out, errOut *bytes.Buffer, input string) *Program {
		def := Definition{Name: "app", Handler: "App", Commands: []CommandDef{{Name: "boom", Handler: "AppBoom"}}}
		p := NewProgram(def, &echoHandlers{fail: failing})
		p.stdout, p.stderr = out, errOut
		p.stdin = strings.NewReader(input)
		return p
	}

	t.Run("off by default: the funnel reports, the REPL does not repeat it", func(t *testing.T) {
		t.Parallel()
		var out, errOut bytes.Buffer
		if err := NewREPL(newProgram(&out, &errOut, "boom\nexit\n")).WithPrompt("").Run(context.Background()); err != nil {
			t.Fatal(err)
		}
		if n := strings.Count(errOut.String(), failing.Error()); n != 1 {
			t.Errorf("the funnel reported %d times, want 1: %q", n, errOut.String())
		}
		if strings.Contains(out.String(), failing.Error()) {
			t.Errorf("the REPL echoed an error the funnel had already reported: %q", out.String())
		}
	})

	t.Run("on when asked: for a program whose funnel is silent", func(t *testing.T) {
		t.Parallel()
		var out, errOut bytes.Buffer
		p := newProgram(&out, &errOut, "boom\nexit\n").
			WithFunnel(func(context.Context, *Context, Outcome) {}) // says nothing
		if err := NewREPL(p).WithPrompt("").WithErrorEcho(true).Run(context.Background()); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), failing.Error()) {
			t.Errorf("with a silent funnel and the echo on, nothing was reported: %q", out.String())
		}
	})

	t.Run("a failing command never ends the session", func(t *testing.T) {
		t.Parallel()
		var out, errOut bytes.Buffer
		if err := NewREPL(newProgram(&out, &errOut, "boom\nboom\nexit\n")).WithPrompt("").Run(context.Background()); err != nil {
			t.Fatalf("a failing command ended the loop: %v", err)
		}
		if n := strings.Count(errOut.String(), failing.Error()); n != 2 {
			t.Errorf("ran %d commands after the first failure, want both: %q", n, errOut.String())
		}
	})
}

// echoHandlers fails on `boom` and does nothing otherwise.
type echoHandlers struct {
	DefaultCascadingPreRun
	DefaultPreRun
	DefaultPostRun
	DefaultCascadingPostRun
	fail error
}

func (h *echoHandlers) Run(ctx context.Context, rtx *Context) {}
func (h *echoHandlers) App() Handlers                         { return h }
func (h *echoHandlers) AppBoom() Handlers                     { return &boomHandlers{fail: h.fail} }

type boomHandlers struct {
	DefaultCascadingPreRun
	DefaultPreRun
	DefaultPostRun
	DefaultCascadingPostRun
	fail error
}

func (h *boomHandlers) Run(ctx context.Context, rtx *Context) { rtx.RecordError(h.fail) }

// blockingReader never returns, standing in for a terminal nobody types into. It is what makes
// the cancellation test meaningful: a REPL that read on the calling goroutine would hang here
// forever rather than honoring the context.
type blockingReader struct{}

func (blockingReader) Read([]byte) (int, error) { select {} }
