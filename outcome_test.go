package rotini

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

// The funnel is handed everything a run recorded, and Run reports the same thing to its caller.
// These pin where one ends and the other begins.

type ocProgram struct{ do func(*Context) }

func (p ocProgram) App() Handlers    { return ocNoop{} }
func (p ocProgram) AppRun() Handlers { return ocLeaf{do: p.do} }

type ocNoop struct{ DefaultHooks }

func (ocNoop) Run(context.Context, *Context) {}

type ocLeaf struct {
	DefaultHooks
	do func(*Context)
}

func (h ocLeaf) Run(_ context.Context, rtx *Context) { h.do(rtx) }

// TestOutcome_funnelCannotRewriteTheRunsError is the C2 rule applied to the last value that
// crosses the boundary.
//
// Outcome is passed by value, but its slices are headers over shared arrays. Run's error used to
// be built AFTER the funnel, so a funnel writing out.Errors[0] silently changed what Run
// returned — while out.Errors = append(…) did not, because append reallocates. Propagating for
// an index write and vanishing for an append is the worst shape an aliasing bug can take.
func TestOutcome_funnelCannotRewriteTheRunsError(t *testing.T) {
	recorded := errors.New("the real failure")
	p := NewProgram(testDef(), ocProgram{do: func(rtx *Context) { rtx.RecordError(recorded) }}).
		WithStdout(io.Discard).WithStderr(io.Discard)
	p.WithFunnel(func(_ context.Context, _ *Context, out Outcome) {
		out.Errors[0] = errors.New("REPLACED")
		out.Errors = append(out.Errors, errors.New("APPENDED"))
	})

	_, err := p.Run([]string{"run", "x"})
	if err == nil || !strings.Contains(err.Error(), "the real failure") {
		t.Errorf("Run() error = %v, want the error the RUN recorded", err)
	}
	if err != nil && strings.Contains(err.Error(), "REPLACED") {
		t.Error("a funnel rewrote the run's own account of itself")
	}
}

// TestOutcome_funnelStillOwnsTheExitCode is the other half: the funnel remains the final
// authority on the code, which is what Context.Exit is for there.
func TestOutcome_funnelStillOwnsTheExitCode(t *testing.T) {
	p := NewProgram(testDef(), ocProgram{do: func(rtx *Context) { rtx.RecordError(errors.New("x")) }}).
		WithStdout(io.Discard).WithStderr(io.Discard)
	p.WithFunnel(func(_ context.Context, rtx *Context, _ Outcome) { rtx.Exit(42) })

	code, err := p.Run([]string{"run", "x"})
	if code != 42 {
		t.Errorf("exit = %d, want the funnel's 42", code)
	}
	if err == nil {
		t.Error("the run's error vanished when the funnel set a code")
	}
}

// TestOutcome_customFunnelOwnsTheExitEntirely pins the documented consequence: a custom funnel
// that sets no code exits 0, even for a run that recorded errors. "Owns the exit entirely"
// includes owning the failure to claim one.
func TestOutcome_customFunnelOwnsTheExitEntirely(t *testing.T) {
	p := NewProgram(testDef(), ocProgram{do: func(rtx *Context) { rtx.RecordError(errors.New("x")) }}).
		WithStdout(io.Discard).WithStderr(io.Discard)
	p.WithFunnel(func(context.Context, *Context, Outcome) {})

	if code, _ := p.Run([]string{"run", "x"}); code != 0 {
		t.Errorf("exit = %d, want 0 — a custom funnel that claims no code gets none", code)
	}
}

// TestOutcome_defaultFunnelOrderAndStreams pins what the WithFunnel doc promises: the order, the
// prefixes, and which stream each channel lands on. Separate streams hide the order, so this
// points both at one writer.
func TestOutcome_defaultFunnelOrderAndStreams(t *testing.T) {
	var both strings.Builder
	p := NewProgram(testDef(), ocProgram{do: func(rtx *Context) {
		rtx.RecordInfo("INFO")
		rtx.RecordSuccess("SUCCESS")
		rtx.RecordWarning(errors.New("WARNING"))
		rtx.RecordError(errors.New("ERROR"))
		panic("PANIC")
	}}).WithStdout(&both).WithStderr(&both).WithPanicRecover(true)

	code, _ := p.Run([]string{"run", "x"})
	want := "INFO\nWarning: WARNING\nError: ERROR\nFatal Error: PANIC\nSUCCESS\n"
	if both.String() != want {
		t.Errorf("default funnel emission:\n got %q\nwant %q", both.String(), want)
	}
	if code == 0 {
		t.Error("a recorded error and a panic exited 0")
	}
}

// TestOutcome_defaultFunnelFloorIsFlat pins the DELIBERATE design Category documents: rotini
// labels, the funnel decides, and the default maps every failure to 1 rather than inventing a
// category→code table nobody asked for.
func TestOutcome_defaultFunnelFloorIsFlat(t *testing.T) {
	for _, c := range []struct {
		name string
		err  error
	}{
		{"a plain error", errors.New("x")},
		{"a usage error", UsageError(errors.New("x"))},
		{"an internal error", InternalError(errors.New("x"))},
		{"a parse error", &ParseError{Kind: ParseKindUnknownFlag, Msg: "x"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := NewProgram(testDef(), ocProgram{do: func(rtx *Context) { rtx.HaltWith(c.err) }}).
				WithStdout(io.Discard).WithStderr(io.Discard)
			if code, _ := p.Run([]string{"run", "x"}); code != 1 {
				t.Errorf("exit = %d, want the flat floor of 1", code)
			}
		})
	}
}

// TestOutcome_warningsNeverRaiseTheExit: a warning is non-fatal by definition, and the channel
// would be pointless if it could fail a run.
func TestOutcome_warningsNeverRaiseTheExit(t *testing.T) {
	p, _, errb := newTestProgram(&testHandlers{log: new([]string), onRun: func(rtx *Context) {
		rtx.RecordWarning(errors.New("careful"))
	}}, []string{"run", "x"})

	code, err := p.Run(p.args)
	if code != 0 || err != nil {
		t.Errorf("a warning alone gave (%d, %v), want (0, nil)", code, err)
	}
	if !strings.Contains(errb.String(), "Warning: careful") {
		t.Errorf("stderr = %q, want the warning reported", errb.String())
	}
}
