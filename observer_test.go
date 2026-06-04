package rotini

import (
	"bytes"
	"strconv"
	"strings"
	"testing"
)

// evToken renders an Event as a short, order-revealing token for sequence assertions.
func evToken(e Event) string {
	switch e.Phase {
	case PhaseResolved:
		return "resolved:" + strings.Join(e.Path, ">")
	case PhaseRemoteExec:
		return "remote:" + e.Command
	case PhaseHookStart:
		return e.Command + "." + e.Hook + ":start"
	case PhaseHookEnd:
		return e.Command + "." + e.Hook + ":end"
	case PhaseExit:
		return "exit:" + strconv.Itoa(e.Code)
	}
	return "?"
}

func tokens(events []Event) []string {
	out := make([]string, len(events))
	for i, e := range events {
		out[i] = evToken(e)
	}
	return out
}

// A clean run emits Resolved, a HookStart/HookEnd pair around every lifecycle hook in
// dispatch order, and a final Exit with the settled code.
func TestObserver_eventSequence(t *testing.T) {
	var events []Event
	p, _, _ := newTestProgram(&testHandlers{log: new([]string)}, []string{"run"})
	p.WithObserver(func(e Event) { events = append(events, e) })

	if code := p.run(p.args); code != 0 {
		t.Fatalf("run = %d, want 0", code)
	}

	want := []string{
		"resolved:app>run",
		"app.CascadingPreRun:start", "app.CascadingPreRun:end",
		"run.CascadingPreRun:start", "run.CascadingPreRun:end",
		"run.PreRun:start", "run.PreRun:end",
		"run.Run:start", "run.Run:end",
		"run.PostRun:start", "run.PostRun:end",
		"run.CascadingPostRun:start", "run.CascadingPostRun:end",
		"app.CascadingPostRun:start", "app.CascadingPostRun:end",
		"exit:0",
	}
	if got := tokens(events); !equalStrings(got, want) {
		t.Errorf("event sequence:\n got=%v\nwant=%v", got, want)
	}
	// Every HookEnd on a clean run carries no error.
	for _, e := range events {
		if e.Phase == PhaseHookEnd && e.Err != nil {
			t.Errorf("%s.%s reported an error on a clean run: %v", e.Command, e.Hook, e.Err)
		}
	}
}

// A panic in a hook is reported on THAT hook's HookEnd (independent of the first-failure
// funnel); teardown hooks still emit, and the run exits non-zero.
func TestObserver_panicReportedOnHookEnd(t *testing.T) {
	var events []Event
	log := []string{}
	p, _, _ := newTestProgram(&actProgram{log: &log, actions: map[string]act{
		"run": {at: "Run", do: func(*Context) { panic("boom") }},
	}}, []string{"run"})
	p.WithObserver(func(e Event) { events = append(events, e) })

	if code := p.run(p.args); code != 1 {
		t.Fatalf("run = %d, want 1 (default OnError floors a panic to 1)", code)
	}

	var runEndErr error
	withErr := 0
	for _, e := range events {
		if e.Phase == PhaseHookEnd && e.Err != nil {
			withErr++
			if e.Command == "run" && e.Hook == "Run" {
				runEndErr = e.Err
			}
		}
	}
	if runEndErr == nil || !strings.Contains(runEndErr.Error(), "boom") {
		t.Errorf("run.Run HookEnd should carry the panic, got %v", runEndErr)
	}
	if withErr != 1 {
		t.Errorf("exactly one HookEnd should report the panic, got %d", withErr)
	}
	// Teardown still ran (its HookEnd events are present) and the run exited 1.
	got := tokens(events)
	if !contains(got, "app.CascadingPostRun:end") {
		t.Errorf("teardown HookEnd missing after a panic: %v", got)
	}
	if got[len(got)-1] != "exit:1" {
		t.Errorf("last event = %q, want exit:1", got[len(got)-1])
	}
}

// ExitNow still brackets the calling hook (its HookEnd fires during unwind) but skips all
// teardown — no PostRun/CascadingPostRun events — and the run exits with the given code.
func TestObserver_exitNowBracketsHookButSkipsTeardown(t *testing.T) {
	var events []Event
	log := []string{}
	p, _, _ := newTestProgram(&actProgram{log: &log, actions: map[string]act{
		"run": {at: "Run", do: func(rtx *Context) { rtx.ExitNow(3) }},
	}}, []string{"run"})
	p.WithObserver(func(e Event) { events = append(events, e) })

	if code := p.run(p.args); code != 3 {
		t.Fatalf("run = %d, want 3", code)
	}
	got := tokens(events)
	if !contains(got, "run.Run:start") || !contains(got, "run.Run:end") {
		t.Errorf("ExitNow hook should still be bracketed start/end: %v", got)
	}
	for _, tok := range got {
		if strings.Contains(tok, "PostRun") {
			t.Errorf("ExitNow must skip teardown, but saw %q in %v", tok, got)
		}
	}
	if got[len(got)-1] != "exit:3" {
		t.Errorf("last event = %q, want exit:3", got[len(got)-1])
	}
}

// A remote dispatch emits Resolved (the path up to the remote token), RemoteExec, and Exit —
// no hook events, since no local lifecycle runs.
func TestObserver_remoteDispatch(t *testing.T) {
	writeFakeBinary(t, "app-ext", "#!/bin/sh\nexit 0\n")
	def := Definition{Name: "app", Handler: "App", RemoteCommands: []RemoteDef{{Name: "ext", Binary: "app-ext"}}}

	var events []Event
	p := NewProgram(def, &testHandlers{log: new([]string)}).
		WithArguments([]string{"ext", "x"}).
		WithObserver(func(e Event) { events = append(events, e) })
	p.stdout, p.stderr = &bytes.Buffer{}, &bytes.Buffer{}

	if code := p.run(p.args); code != 0 {
		t.Fatalf("run = %d, want 0", code)
	}
	want := []string{"resolved:app", "remote:ext", "exit:0"}
	if got := tokens(events); !equalStrings(got, want) {
		t.Errorf("remote events:\n got=%v\nwant=%v", got, want)
	}
}

// WithObserver(nil) clears a prior observer; a run with no observer emits nothing and is
// unaffected (the zero-cost default).
func TestObserver_nilEmitsNothing(t *testing.T) {
	var events []Event
	p, _, _ := newTestProgram(&testHandlers{log: new([]string)}, []string{"run"})
	p.WithObserver(func(e Event) { events = append(events, e) }).WithObserver(nil)
	if code := p.run(p.args); code != 0 {
		t.Fatalf("run = %d, want 0", code)
	}
	if len(events) != 0 {
		t.Errorf("cleared observer still received %d events", len(events))
	}
}

// equalStrings reports whether two string slices are element-wise equal.
func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
