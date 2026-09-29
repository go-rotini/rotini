package rotini

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"
	"slices"
	"strings"
	"sync"
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

// ── the five seams ───────────────────────────────────────────────────────────
//
// rotini owns turning a line into a dispatched invocation; it does not own reading the line.
// These cover the boundary between the two, and the interrupt semantics that were wrong before
// the seam existed.

// blockingHandlers runs a command that blocks until its context ends, reporting when it started
// and how it finished — which is how a test can tell "the command was cancelled" from "the
// session was".
type blockingHandlers struct {
	started chan struct{}
	ended   chan struct{}
	once    sync.Once
}

func (h *blockingHandlers) App() Handlers    { return replNoop{} }
func (h *blockingHandlers) AppRun() Handlers { return replBlock{h: h} }

type replNoop struct{ DefaultHooks }

func (replNoop) Run(context.Context, *Context) {}

type replBlock struct {
	DefaultHooks
	h *blockingHandlers
}

// Run blocks only for the line that asked for it, so a session can carry on afterwards —
// which is the whole property under test.
func (b replBlock) Run(ctx context.Context, rtx *Context) {
	if !slices.Contains(rtx.Argv, "blocking") {
		return
	}
	b.h.once.Do(func() { close(b.h.started) })
	<-ctx.Done()
	close(b.h.ended)
}

// TestREPL_interruptCancelsTheLineNotTheSession is the defect the seam was built for.
//
// Every line used to run on the SESSION's context, so a ^C that should abort one command tore
// down the shell. Measured before the fix: a blocking command plus a cancel, and Run returned
// nil with the session gone. In bash, python, psql and redis-cli, ^C returns you to the prompt
// — it is the most-pressed key in a REPL.
func TestREPL_interruptCancelsTheLineNotTheSession(t *testing.T) {
	h := &blockingHandlers{started: make(chan struct{}), ended: make(chan struct{})}
	p := NewProgram(testDef(), h).WithStdout(io.Discard).WithStderr(io.Discard)

	interrupts := make(chan struct{}, 1)
	var seen []string
	lines := []string{"run blocking", "run after"}

	done := make(chan error, 1)
	go func() {
		i := 0
		done <- NewREPL(p).
			WithInterrupts(interrupts).
			WithLineReader(func(context.Context, string) (string, error) {
				if i >= len(lines) {
					return "", ErrNotInteractive
				}
				line := lines[i]
				i++
				seen = append(seen, line)
				return line, nil
			}).
			WithOutput(io.Discard).
			Run(context.Background())
	}()

	<-h.started
	interrupts <- struct{}{} // the ^C

	select {
	case <-h.ended:
	case <-time.After(2 * time.Second):
		t.Fatal("the interrupt did not cancel the running command")
	}

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run = %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the REPL never finished")
	}

	// The session SURVIVED the interrupt: the line after the cancelled one still ran.
	if len(seen) != 2 || seen[1] != "run after" {
		t.Errorf("read %v — the interrupt ended the session instead of the command", seen)
	}
}

// TestREPL_interruptAtThePromptDoesNotKillTheNextCommand is the race that `-race -count=2`
// caught before this shipped.
//
// A background watcher received an interrupt from the channel and THEN took a lock to find the
// line to cancel — so a ^C pressed at an empty prompt could win that race against the next line
// and kill a command the user typed afterwards. Intermittent, and exactly the kind of thing a
// user would report as "sometimes it just cancels".
//
// The fix made the window explicit: anything already pending when a line is submitted arrived
// before the command existed and is dropped. This sends an interrupt before EVERY line, so the
// old design loses reliably under -count.
func TestREPL_interruptAtThePromptDoesNotKillTheNextCommand(t *testing.T) {
	var ran []string
	p, _ := replProgram(t, func(rtx *Context) { ran = append(ran, strings.Join(rtx.Argv, " ")) })

	interrupts := make(chan struct{}, 1)
	lines := []string{"run one", "run two"}
	i := 0

	err := NewREPL(p).
		WithInterrupts(interrupts).
		WithLineReader(func(context.Context, string) (string, error) {
			// A ^C at the prompt, before every line — the documented non-blocking send.
			select {
			case interrupts <- struct{}{}:
			default:
			}
			if i >= len(lines) {
				return "", ErrNotInteractive
			}
			line := lines[i]
			i++
			return line, nil
		}).
		WithOutput(io.Discard).
		Run(context.Background())
	if err != nil {
		t.Fatalf("Run = %v", err)
	}
	if !slices.Equal(ran, []string{"run one", "run two"}) {
		t.Errorf("ran %v, want both lines — a stray interrupt cost a command", ran)
	}
}

// TestREPL_lineReaderContract pins all three of the reader's errors, since each means something
// different and only one of them ends the session.
func TestREPL_lineReaderContract(t *testing.T) {
	boom := errors.New("the terminal fell over")
	cases := map[string]struct {
		err      error
		wantRun  error
		wantMore bool // did the loop ask for another line?
	}{
		"interrupted":      {err: ErrInterrupted, wantRun: nil, wantMore: true},
		"not interactive":  {err: ErrNotInteractive, wantRun: nil},
		"plain EOF":        {err: io.EOF, wantRun: nil},
		"wrapped EOF":      {err: fmt.Errorf("reader: %w", io.EOF), wantRun: nil},
		"real IO failure":  {err: boom, wantRun: boom},
		"wrapped failure":  {err: fmt.Errorf("reader: %w", boom), wantRun: boom},
		"context canceled": {err: context.Canceled, wantRun: nil},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			p, _ := replProgram(t, func(*Context) {})
			calls := 0
			err := NewREPL(p).
				WithLineReader(func(context.Context, string) (string, error) {
					calls++
					if calls == 1 {
						return "", tc.err
					}
					return "", ErrNotInteractive // end the loop on the second ask
				}).
				WithOutput(io.Discard).
				Run(context.Background())

			if !errors.Is(err, tc.wantRun) {
				t.Errorf("Run = %v, want %v", err, tc.wantRun)
			}
			if asked := calls > 1; asked != tc.wantMore {
				t.Errorf("asked for another line = %v, want %v", asked, tc.wantMore)
			}
		})
	}
}

// TestREPL_lineReaderOwnsThePrompt: a reader that draws its own prompt must not have rotini's
// printed in front of it. The prompt is still COMPUTED and handed over, so the reader can use it.
func TestREPL_lineReaderOwnsThePrompt(t *testing.T) {
	p, _ := replProgram(t, func(*Context) {})
	out := &bytes.Buffer{}
	var handed []string

	err := NewREPL(p).
		WithPrompt("app> ").
		WithLineReader(func(_ context.Context, prompt string) (string, error) {
			handed = append(handed, prompt)
			return "", ErrNotInteractive
		}).
		WithOutput(out).
		Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(handed) != 1 || handed[0] != "app> " {
		t.Errorf("reader was handed %v, want the configured prompt", handed)
	}
	if out.Len() != 0 {
		t.Errorf("rotini printed a prompt as well as the reader: %q", out.String())
	}
}

// TestREPL_promptFuncSeesLiveState: a prompt that cannot change is not a prompt a real shell
// would have — psql shows the database, a transaction, a continuation.
func TestREPL_promptFuncSeesLiveState(t *testing.T) {
	var ran int
	p, _ := replProgram(t, func(*Context) { ran++ })
	out := &bytes.Buffer{}

	err := NewREPL(p).
		WithPrompt("ignored> ").
		WithPromptFunc(func() string { return fmt.Sprintf("app(%d)> ", ran) }).
		WithInput(strings.NewReader("run a\nrun b\n")).
		WithOutput(out).
		Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{"app(0)> ", "app(1)> ", "app(2)> "} {
		if !strings.Contains(got, want) {
			t.Errorf("prompt %q missing from %q — it did not follow the state", want, got)
		}
	}
	if strings.Contains(got, "ignored") {
		t.Error("WithPrompt won over WithPromptFunc")
	}
}

// ── intercept ────────────────────────────────────────────────────────────────

// TestREPL_interceptHandlesMetaCommands covers the seam's reason to exist: psql's \d and
// sqlite's .schema are not commands of the program and cannot be, because they mean nothing
// outside a session.
func TestREPL_interceptHandlesMetaCommands(t *testing.T) {
	var dispatched []string
	p, _ := replProgram(t, func(rtx *Context) { dispatched = append(dispatched, rtx.Argv[0]) })
	out := &bytes.Buffer{}

	err := NewREPL(p).
		WithIntercept(func(_ context.Context, line string) (bool, error) {
			if !strings.HasPrefix(line, `\`) {
				return false, nil
			}
			fmt.Fprintf(out, "meta:%s\n", strings.TrimPrefix(line, `\`))
			return true, nil
		}).
		WithInput(strings.NewReader("\\d\nrun x\n\\q\n")).
		WithOutput(out).
		Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := out.String(); !strings.Contains(got, "meta:d") || !strings.Contains(got, "meta:q") {
		t.Errorf("meta-commands not handled: %q", got)
	}
	if len(dispatched) != 1 || dispatched[0] != "run" {
		t.Errorf("dispatched %v — an intercepted line reached the command tree", dispatched)
	}
}

// TestREPL_interceptSeesEverything: blank lines and exit words included, so a program can
// override either. An interceptor that saw only "interesting" lines could not implement a
// pager toggle on an empty line, or replace the exit word with a confirmation.
func TestREPL_interceptSeesEverything(t *testing.T) {
	p, _ := replProgram(t, func(*Context) {})
	var seen []string

	err := NewREPL(p).
		WithIntercept(func(_ context.Context, line string) (bool, error) {
			seen = append(seen, line)
			return false, nil
		}).
		WithInput(strings.NewReader("run x\n\nexit\n")).
		WithOutput(io.Discard).
		Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"run x", "", "exit"}; !reflect.DeepEqual(seen, want) {
		t.Errorf("intercept saw %q, want %q", seen, want)
	}
}

// TestREPL_interceptErrorEndsTheSession: an interceptor that fails has lost track of its own
// state, which is not something to keep prompting through. A meta-command that merely failed
// reports it and returns (true, nil).
func TestREPL_interceptErrorEndsTheSession(t *testing.T) {
	boom := errors.New("meta state is corrupt")
	p, _ := replProgram(t, func(*Context) { t.Error("nothing should have dispatched") })

	err := NewREPL(p).
		WithIntercept(func(context.Context, string) (bool, error) { return false, boom }).
		WithInput(strings.NewReader("run x\nrun y\n")).
		WithOutput(io.Discard).
		Run(context.Background())
	if !errors.Is(err, boom) {
		t.Errorf("Run = %v, want the interceptor's error", err)
	}
}

// ── completion ───────────────────────────────────────────────────────────────

// TestREPL_completeKnowsTheCommandTree is the capability no line editor can supply, because it
// does not know your commands. It is the same completion the generated shell scripts use, from
// the same Definition.
func TestREPL_completeKnowsTheCommandTree(t *testing.T) {
	p, _ := replProgram(t, func(*Context) {})
	repl := NewREPL(p)

	cases := []struct {
		name string
		line string
		want string
	}{
		{"a partial command", "ru", "run"},
		{"an empty line offers the tree", "", "run"},
		{"a partial flag", "run --co", "--count"},
		{"a root flag", "--verb", "--verbose"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := repl.Complete(tc.line, len(tc.line))
			if !slices.Contains(got, tc.want) {
				t.Errorf("Complete(%q) = %v, want it to offer %q", tc.line, got, tc.want)
			}
		})
	}
}

// TestREPL_completeReadsTheCursorLikeAShell: a trailing space means the NEXT word is being
// completed. Getting this wrong makes "run " offer completions of the word "run", which is the
// single most visible way a completer can be broken.
func TestREPL_completeReadsTheCursorLikeAShell(t *testing.T) {
	p, _ := replProgram(t, func(*Context) {})
	repl := NewREPL(p)

	if got := repl.Complete("run", 3); !slices.Contains(got, "run") {
		t.Errorf(`Complete("run", 3) = %v, want the word itself completed`, got)
	}
	// After the space, "run" is context and the flags are what is on offer.
	if got := repl.Complete("run ", 4); slices.Contains(got, "run") {
		t.Errorf(`Complete("run ", 4) = %v, still offering the completed word`, got)
	}
	// A cursor mid-line completes what is under it, not the whole line.
	if got := repl.Complete("ru --count 3", 2); !slices.Contains(got, "run") {
		t.Errorf("Complete with the cursor at 2 = %v, want run", got)
	}
}

// TestREPL_completeIsSafeOnNonsense: a completer runs on every keystroke, so it must never
// panic and never report an error — an empty result means "nothing to offer".
func TestREPL_completeIsSafeOnNonsense(t *testing.T) {
	p, _ := replProgram(t, func(*Context) {})
	repl := NewREPL(p)

	for _, pos := range []int{-1, 0, 3, 999} {
		_ = repl.Complete("run", pos) // must not panic
	}
	if got := repl.Complete("nosuchcommand ", 14); len(got) != 0 {
		t.Errorf("Complete under an unknown command = %v, want nothing", got)
	}
	var nilREPL *REPL
	if got := nilREPL.Complete("run", 3); got != nil {
		t.Errorf("nil REPL completed %v", got)
	}
	if got := NewREPL(nil).Complete("run", 3); got != nil {
		t.Errorf("REPL with no program completed %v", got)
	}
}

// ── the seams compose ────────────────────────────────────────────────────────

// TestREPL_seamsComposeIntoARealSession is the plug-and-play claim, exercised end to end: a
// custom reader that also completes, a live prompt, meta-commands, and an interrupt — all at
// once, which is how a program actually assembles one.
func TestREPL_seamsComposeIntoARealSession(t *testing.T) {
	var ran []string
	p, _ := replProgram(t, func(rtx *Context) { ran = append(ran, strings.Join(rtx.Argv, " ")) })
	out := &bytes.Buffer{}
	repl := NewREPL(p)

	// A "reader" that expands a tab into the single completion for the line so far — which
	// is, in miniature, what a readline AutoCompleter does.
	script := []string{"ru\t x", `\meta`, "", "run second", "exit"}
	i := 0
	var completed []string

	err := repl.
		WithPromptFunc(func() string { return fmt.Sprintf("app(%d)> ", len(ran)) }).
		WithIntercept(func(_ context.Context, line string) (bool, error) {
			if strings.HasPrefix(line, `\`) {
				fmt.Fprintln(out, "meta!")
				return true, nil
			}
			return false, nil
		}).
		WithLineReader(func(_ context.Context, prompt string) (string, error) {
			if i >= len(script) {
				return "", ErrNotInteractive
			}
			line := script[i]
			i++
			fmt.Fprint(out, prompt)
			if head, tail, found := strings.Cut(line, "\t"); found {
				hits := repl.Complete(head, len(head))
				if len(hits) == 0 {
					return "", errors.New("nothing to complete")
				}
				completed = append(completed, hits[0])
				line = hits[0] + tail
			}
			return line, nil
		}).
		WithOutput(out).
		Run(context.Background())
	if err != nil {
		t.Fatalf("Run = %v", err)
	}

	if !slices.Equal(completed, []string{"run"}) {
		t.Errorf("completion produced %v, want [run]", completed)
	}
	if !slices.Equal(ran, []string{"run x", "run second"}) {
		t.Errorf("dispatched %v, want the completed line and the plain one", ran)
	}
	got := out.String()
	if !strings.Contains(got, "meta!") {
		t.Errorf("the interceptor never fired: %q", got)
	}
	// The prompt followed the state through the session.
	for _, want := range []string{"app(0)> ", "app(1)> ", "app(2)> "} {
		if !strings.Contains(got, want) {
			t.Errorf("prompt %q missing: %q", want, got)
		}
	}
}

// TestREPL_isReusable backs the doc's claim: per-session state lives on the stack, so one
// configured REPL can run more than one session.
func TestREPL_isReusable(t *testing.T) {
	var ran int
	p, _ := replProgram(t, func(*Context) { ran++ })
	repl := NewREPL(p).WithOutput(io.Discard)

	for range 3 {
		if err := repl.WithInput(strings.NewReader("run x\n")).Run(context.Background()); err != nil {
			t.Fatalf("Run = %v", err)
		}
	}
	if ran != 3 {
		t.Errorf("ran %d times across 3 sessions", ran)
	}
}

// ── the built-in reader ──────────────────────────────────────────────────────

// plainReader is an io.Reader and DELIBERATELY not an io.ByteReader, which is the shape
// os.Stdin has. It reports how much was taken from it, so a test can prove the REPL consumed
// exactly the line it returned and no more.
type plainReader struct {
	data []byte
	read int
}

func (r *plainReader) Read(p []byte) (int, error) {
	if r.read >= len(r.data) {
		return 0, io.EOF
	}
	n := copy(p, r.data[r.read:])
	r.read += n
	return n, nil
}

// TestREPL_builtinReaderDoesNotStealTheRestOfTheStream is the defect the byte-at-a-time
// adapter exists for, on the path production actually takes.
//
// os.Stdin is an *os.File, which is NOT an io.ByteReader, so a real session goes through
// byteAtATime. A buffered reader here would fill its private buffer with every remaining line,
// return one, and drop the rest when it went out of scope — invisible at a terminal, where a
// tty hands over one line at a time, and fatal for a pipe, a test or a subprocess reading the
// same stdin afterwards.
func TestREPL_builtinReaderDoesNotStealTheRestOfTheStream(t *testing.T) {
	var ran []string
	p, _ := replProgram(t, func(rtx *Context) { ran = append(ran, strings.Join(rtx.Argv, " ")) })

	// The exit word ends the session with two lines still unread behind it.
	in := &plainReader{data: []byte("run one\nrun two\nexit\nLEFT BEHIND\nSO IS THIS\n")}

	if err := NewREPL(p).WithInput(in).WithOutput(io.Discard).Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(ran, []string{"run one", "run two"}) {
		t.Errorf("dispatched %v, want both lines before the exit word", ran)
	}

	rest, err := io.ReadAll(in)
	if err != nil {
		t.Fatal(err)
	}
	if want := "LEFT BEHIND\nSO IS THIS\n"; string(rest) != want {
		t.Errorf("the stream held %q afterwards, want %q — the reader consumed past its line",
			rest, want)
	}
}

// TestREPL_builtinReaderHandlesLineEndings: a final line with no newline is still a line, and a
// CRLF stream must not leave a carriage return on the end of every command.
func TestREPL_builtinReaderHandlesLineEndings(t *testing.T) {
	cases := map[string]string{
		"no trailing newline": "run alpha",
		"crlf":                "run alpha\r\n",
		"lf":                  "run alpha\n",
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			var ran []string
			p, _ := replProgram(t, func(rtx *Context) { ran = append(ran, strings.Join(rtx.Argv, " ")) })
			in := &plainReader{data: []byte(input)}

			if err := NewREPL(p).WithInput(in).WithOutput(io.Discard).Run(context.Background()); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(ran, []string{"run alpha"}) {
				t.Errorf("dispatched %v, want [run alpha]", ran)
			}
		})
	}
}

// TestREPL_builtinReaderHonorsTheContext: a blocked read must not outlive a canceled session.
// The read goroutine may survive — a blocked Read cannot be interrupted — but Run must not.
func TestREPL_builtinReaderHonorsTheContext(t *testing.T) {
	p, _ := replProgram(t, func(*Context) {})
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- NewREPL(p).WithInput(blockingReader{}).WithOutput(io.Discard).Run(ctx)
	}()
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run = %v, want nil — a canceled session ended on request", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after its context was canceled")
	}
}

// failingReader returns a genuine I/O failure — not EOF, not an interrupt — which is the one
// read outcome that must reach the caller of Run.
type failingReader struct{ err error }

func (r failingReader) Read([]byte) (int, error) { return 0, r.err }

// TestREPL_builtinReaderReportsRealFailures: a broken pipe or a closed terminal is not a clean
// end of session, and swallowing it would leave a caller thinking the user typed "exit".
func TestREPL_builtinReaderReportsRealFailures(t *testing.T) {
	boom := errors.New("the tty went away")
	p, _ := replProgram(t, func(*Context) {})

	err := NewREPL(p).WithInput(failingReader{err: boom}).WithOutput(io.Discard).Run(context.Background())
	if !errors.Is(err, boom) {
		t.Errorf("Run = %v, want the underlying read failure", err)
	}
}

// TestREPL_builtinReaderCancelDuringARead covers the read that is already blocked when the
// session ends — the goroutine cannot be interrupted, so the select is what returns.
func TestREPL_builtinReaderCancelDuringARead(t *testing.T) {
	p, _ := replProgram(t, func(*Context) {})
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- NewREPL(p).WithInput(blockingReader{}).WithOutput(io.Discard).Run(ctx)
	}()

	time.Sleep(50 * time.Millisecond) // let the read block first
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run = %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a blocked read outlived its canceled session")
	}
}

// TestREPL_nothingReadsInterruptsBetweenCommands pins the contract the drop rule rests on: the
// channel is read ONLY while a command is running. A REPL sitting at its prompt — or one whose
// session has ended — consumes nothing, which is what makes "pending means stale" well defined.
func TestREPL_nothingReadsInterruptsBetweenCommands(t *testing.T) {
	p, _ := replProgram(t, func(*Context) {})
	interrupts := make(chan struct{}) // unbuffered: a receive here is observable
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- NewREPL(p).
			WithInterrupts(interrupts).
			WithInput(blockingReader{}).
			WithOutput(io.Discard).
			Run(ctx)
	}()

	// Sitting at the prompt with no command running: nobody should take this.
	select {
	case interrupts <- struct{}{}:
		t.Error("an interrupt was consumed while no command was running")
	case <-time.After(100 * time.Millisecond):
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run = %v", err)
	}

	// And after the session, still nobody.
	select {
	case interrupts <- struct{}{}:
		t.Error("something outlived the session and is still consuming interrupts")
	case <-time.After(100 * time.Millisecond):
	}
}

// TestByteAtATime_nilReader: the adapter is constructed from whatever a caller supplied, and a
// nil reader must read as an empty stream rather than panicking inside a session.
func TestByteAtATime_nilReader(t *testing.T) {
	if _, err := (byteAtATime{}).ReadByte(); !errors.Is(err, io.EOF) {
		t.Errorf("ReadByte on a nil reader = %v, want io.EOF", err)
	}
}
