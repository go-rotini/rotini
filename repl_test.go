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
	p, _ := replProgram(t, func(rtx *Context) { seen = append(seen, strings.Join(rtx.Args, " ")) })
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
			rtx.SignalExit(2)
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
