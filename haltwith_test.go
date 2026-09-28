package rotini

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// Failing from a hook is the most common thing a handler does, and it used to be a two-part
// ritual — record, then stop — whose second half is the half that decides anything and the half
// that can go missing. These tests pin both the shape of the hazard and the one-call cure.

// TestHalt_isLoadBearingInSetupAndInertElsewhere is the fact the whole finding rests on, and it
// was not written down anywhere before: Halt stops FORWARD progress, so it does something in
// exactly two of the five hooks.
//
// The unwind loop never consults `stopped` (see the teardown guard in dispatch — only exitNow
// and a panic cut it short), and Run is the last forward step of the default plan. So a Halt in
// Run, PostRun or CascadingPostRun changes nothing at all. The generated stub used to teach it
// in Run, which is why the ritual was learned as boilerplate and then omitted where it counts.
func TestHalt_isLoadBearingInSetupAndInertElsewhere(t *testing.T) {
	full := []string{
		"app.CascadingPreRun", "run.CascadingPreRun", "run.PreRun", "run.Run",
		"run.PostRun", "run.CascadingPostRun", "app.CascadingPostRun",
	}

	for _, tc := range []struct {
		hook string
		want []string
	}{
		// Setup: the command is prevented from proceeding. Teardown still unwinds for the
		// steps that began — that is the contract, and Halt does not touch it.
		{"CascadingPreRun", []string{
			"app.CascadingPreRun", "run.CascadingPreRun",
			"run.CascadingPostRun", "app.CascadingPostRun",
		}},
		{"PreRun", []string{
			"app.CascadingPreRun", "run.CascadingPreRun", "run.PreRun",
			"run.PostRun", "run.CascadingPostRun", "app.CascadingPostRun",
		}},
		// Inert: there is no forward progress left to stop.
		{"Run", full},
		{"PostRun", full},
		{"CascadingPostRun", full},
	} {
		t.Run(tc.hook, func(t *testing.T) {
			_, log := runActs(t, []string{"run", "x"}, map[string]act{
				"run": {at: tc.hook, do: func(rtx *Context) { rtx.Halt() }},
			})
			if fmt.Sprint(log) != fmt.Sprint(tc.want) {
				t.Errorf("Halt in %s:\n got %v\nwant %v", tc.hook, log, tc.want)
			}
		})
	}
}

// TestHaltWith_isExactlyRecordErrorPlusHalt is the equivalence contract. HaltWith is a
// convenience, not a new behaviour: if the two forms ever diverge, the convenience has become a
// second set of semantics to learn, which is the opposite of the point.
func TestHaltWith_isExactlyRecordErrorPlusHalt(t *testing.T) {
	boom := errors.New("setup failed")

	oneCall, oneLog := runActs(t, []string{"run", "x"}, map[string]act{
		"run": {at: "PreRun", do: func(rtx *Context) { rtx.HaltWith(boom) }},
	})
	twoCalls, twoLog := runActs(t, []string{"run", "x"}, map[string]act{
		"run": {at: "PreRun", do: func(rtx *Context) { rtx.RecordError(boom); rtx.Halt() }},
	})

	if oneCall != twoCalls {
		t.Errorf("exit code: HaltWith = %d, RecordError+Halt = %d", oneCall, twoCalls)
	}
	if fmt.Sprint(oneLog) != fmt.Sprint(twoLog) {
		t.Errorf("hooks run:\n HaltWith        %v\n RecordError+Halt %v", oneLog, twoLog)
	}
	if oneCall == 0 {
		t.Error("a recorded error exited 0")
	}
}

// TestHaltWith_stopsTheWorkTheHookRefused is the harm the finding is about, stated as a test.
//
// The exit code and stderr are IDENTICAL whether or not a setup hook halts — so this asserts
// the only thing that differs: whether Run got to do work the setup had already established it
// must not do. That is why no output-asserting test caught the original bug.
func TestHaltWith_stopsTheWorkTheHookRefused(t *testing.T) {
	boom := errors.New("no connection was opened")

	_, halted := runActs(t, []string{"run", "x"}, map[string]act{
		"run": {at: "CascadingPreRun", do: func(rtx *Context) { rtx.HaltWith(boom) }},
	})
	if contains(halted, "run.Run") {
		t.Errorf("Run executed after its setup hook failed: %v", halted)
	}

	// The two-part form with the stop omitted — the mistake. Kept here so the difference is
	// visible in one place: same verdict, work still done.
	_, leaked := runActs(t, []string{"run", "x"}, map[string]act{
		"run": {at: "CascadingPreRun", do: func(rtx *Context) { rtx.RecordError(boom) }},
	})
	if !contains(leaked, "run.Run") {
		t.Fatal("fixture no longer demonstrates the hazard: Run was skipped without a halt")
	}
}

// TestHaltWith_isCorrectInEveryHook is the property that removes the hook-dependent knowledge:
// whichever hook a handler is in, one call records the failure and stops whatever forward
// progress remains. Where Halt is inert HaltWith degrades to recording, which is what a failure
// in Run or a teardown hook wants anyway.
func TestHaltWith_isCorrectInEveryHook(t *testing.T) {
	for _, hook := range []string{"CascadingPreRun", "PreRun", "Run", "PostRun", "CascadingPostRun"} {
		t.Run(hook, func(t *testing.T) {
			log := []string{}
			p, _, errb := newTestProgram(&actProgram{log: &log, actions: map[string]act{
				"run": {at: hook, do: func(rtx *Context) { rtx.HaltWith(errors.New("failed in " + hook)) }},
			}}, []string{"run", "x"})

			code, _ := p.Run(p.args)
			if code == 0 {
				t.Errorf("HaltWith in %s exited 0", hook)
			}
			if got := errb.String(); !strings.Contains(got, "failed in "+hook) {
				t.Errorf("HaltWith in %s did not reach the funnel; stderr = %q", hook, got)
			}
		})
	}
}

// TestHaltWith_nilErrorStillHalts: a nil error records nothing (RecordError's documented
// behaviour) but the halt stands, so a caller passing a maybe-nil error needs no guard and
// cannot accidentally turn the stop into a fall-through.
func TestHaltWith_nilErrorStillHalts(t *testing.T) {
	code, log := runActs(t, []string{"run", "x"}, map[string]act{
		"run": {at: "PreRun", do: func(rtx *Context) { rtx.HaltWith(nil) }},
	})
	if contains(log, "run.Run") {
		t.Errorf("HaltWith(nil) did not halt: %v", log)
	}
	if code != 0 {
		t.Errorf("exit = %d, want 0 — nothing was recorded, so nothing failed", code)
	}
}

// TestRecordError_aloneStillContinues guards the choice HaltWith must NOT take away. Recording
// without stopping is a real pattern — collect every problem, or let a later hook decide by
// gating on Failed — and it has to keep working exactly as before.
func TestRecordError_aloneStillContinues(t *testing.T) {
	var sawFailed bool
	code, log := runActs(t, []string{"run", "x"}, map[string]act{
		"run": {at: "PreRun", do: func(rtx *Context) {
			rtx.RecordError(errors.New("first problem"))
			rtx.RecordError(errors.New("second problem"))
		}},
		"app": {at: "CascadingPreRun", do: func(rtx *Context) { sawFailed = rtx.Failed() }},
	})
	if !contains(log, "run.Run") {
		t.Errorf("RecordError alone stopped the run: %v", log)
	}
	if code == 0 {
		t.Error("two recorded errors exited 0")
	}
	if sawFailed {
		t.Error("Failed() was true before anything had been recorded")
	}
}

// TestHaltWith_inTheFunnelDoesNotReenter: Halt is documented as a no-op inside the funnel, where
// the lifecycle has already run. HaltWith inherits that, so a funnel that fails while reporting
// cannot stall the settle it is part of.
func TestHaltWith_inTheFunnelDoesNotReenter(t *testing.T) {
	h := &testHandlers{log: new([]string), onRun: func(rtx *Context) {
		rtx.RecordError(errors.New("original"))
	}}
	p, _, _ := newTestProgram(h, []string{"run", "x"})
	p.WithFunnel(func(_ context.Context, rtx *Context, _ Outcome) {
		rtx.HaltWith(errors.New("while reporting"))
	})

	done := make(chan int, 1)
	go func() { code, _ := p.Run(p.args); done <- code }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("HaltWith inside the funnel did not return")
	}
}
