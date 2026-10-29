package rotini

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// TestProgram_withReporterNilRestoresTheDefault: a nil reporter puts back the default, which
// prints to stderr and floors a failed run to 1.
func TestProgram_withReporterNilRestoresTheDefault(t *testing.T) {
	t.Parallel()
	h := &testHandlers{log: new([]string), onRun: func(rtx *Context) { rtx.RecordError(errors.New("boom")) }}
	p, _, errb := newTestProgram(h, []string{"run"})
	p.WithReporter(func(context.Context, *Context, Outcome) {}).WithReporter(nil)
	if code, _ := p.Run(p.args); code != 1 || errb.String() != "Error: boom\n" {
		t.Errorf("after WithReporter(nil): code %d, stderr %q; want the default reporter's 1 and Error line", code, errb)
	}
}

// TestReporter_insideTheReporter pins the reporter stage: HaltWithCode sets no code, records
// are dropped from the run's error, and the reporter runs once.
func TestReporter_insideTheReporter(t *testing.T) {
	t.Parallel()
	errFirst := errors.New("first")
	h := &testHandlers{log: new([]string), onRun: func(rtx *Context) { rtx.RecordError(errFirst) }}
	p, _, _ := newTestProgram(h, []string{"run"})
	calls := 0
	p.WithReporter(func(_ context.Context, rtx *Context, _ Outcome) {
		calls++
		rtx.HaltWithCode(9)
		rtx.RecordError(errors.New("late"))
		rtx.RecordWarning(errors.New("late warning"))
	})
	code, err := p.Run(p.args)
	if code != 0 {
		t.Errorf("code = %d, want 0: HaltWithCode is a no-op in the reporter and a custom reporter has no floor", code)
	}
	if !errors.Is(err, errFirst) || strings.Contains(err.Error(), "late") {
		t.Errorf("err = %v, want only the lifecycle's record", err)
	}
	if calls != 1 {
		t.Errorf("reporter called %d times, want once", calls)
	}
}

// TestReporter_receivesTheProgramsContext: the base context reaches the reporter as it reaches
// every hook.
func TestReporter_receivesTheProgramsContext(t *testing.T) {
	t.Parallel()
	type key struct{}
	h := &testHandlers{log: new([]string), onRun: func(rtx *Context) { rtx.RecordInfo("hi") }}
	p, _, _ := newTestProgram(h, []string{"run"})
	var got any
	p.WithContext(context.WithValue(context.Background(), key{}, "v")).
		WithReporter(func(ctx context.Context, _ *Context, _ Outcome) { got = ctx.Value(key{}) })
	if _, err := p.Run(p.args); err != nil {
		t.Fatal(err)
	}
	if got != "v" {
		t.Errorf("reporter ctx value = %v, want the WithContext value", got)
	}
}

// TestOutcome_slicesAreCopies: a reporter changing its Outcome changes neither the run's records
// nor what a later look at them sees.
func TestOutcome_slicesAreCopies(t *testing.T) {
	t.Parallel()
	var rtxSeen *Context
	h := &testHandlers{log: new([]string), onRun: func(rtx *Context) {
		rtx.RecordInfo("a")
		rtx.RecordInfo("b")
		rtxSeen = rtx
	}}
	p, _, _ := newTestProgram(h, []string{"run"})
	var got []string
	p.WithReporter(func(_ context.Context, _ *Context, out Outcome) {
		got = slices.Clone(out.Infos)
		out.Infos[0] = "changed"
	})
	if _, err := p.Run(p.args); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, []string{"a", "b"}) {
		t.Errorf("Infos = %q, want recording order", got)
	}
	if again := rtxSeen.copyInfos(); again[0] != "a" {
		t.Errorf("the run's records changed through the Outcome: %q", again)
	}
}

// TestProgram_withClockNilRestoresTimeNow: WithClock(nil) undoes a fixed clock.
func TestProgram_withClockNilRestoresTimeNow(t *testing.T) {
	t.Parallel()
	fixed := time.Date(2001, 2, 3, 4, 5, 6, 0, time.UTC)
	var now time.Time
	h := &testHandlers{log: new([]string), onRun: func(rtx *Context) { now = rtx.Now() }}
	p, _, _ := newTestProgram(h, []string{"run"})
	p.WithClock(func() time.Time { return fixed })
	if _, err := p.Run(p.args); err != nil || !now.Equal(fixed) {
		t.Fatalf("WithClock(fixed): Now = %v, %v", now, err)
	}
	before := time.Now()
	p.WithClock(nil)
	if _, err := p.Run(p.args); err != nil || now.Before(before) {
		t.Errorf("WithClock(nil): Now = %v (before %v), want time.Now", now, before)
	}
}

// TestProgram_withTerminationTimeoutNonPositiveIsTheDefault: d <= 0 means no bound.
func TestProgram_withTerminationTimeoutNonPositiveIsTheDefault(t *testing.T) {
	t.Parallel()
	for _, d := range []time.Duration{0, -time.Second} {
		p := NewProgram(testDef(), nil).WithTerminationTimeout(time.Second).WithTerminationTimeout(d)
		if p.terminationTimeout != 0 {
			t.Errorf("WithTerminationTimeout(%v) = %v, want the default (none)", d, p.terminationTimeout)
		}
	}
}

// TestProgram_runContextLeavesTheProgramAlone: RunContext's context is for that one run.
func TestProgram_runContextLeavesTheProgramAlone(t *testing.T) {
	t.Parallel()
	type key struct{}
	var got []any
	p := newCtxProgram(ctxRec{run: func(ctx context.Context) { got = append(got, ctx.Value(key{})) }})
	if _, err := p.RunContext(context.WithValue(context.Background(), key{}, "v"), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := p.WithoutSignalHandling().Run(nil); err != nil {
		t.Fatal(err)
	}
	if p.ctx != nil || !slices.Equal(got, []any{"v", nil}) {
		t.Errorf("values seen = %v (p.ctx %v), want the context in its own run only", got, p.ctx)
	}
}

// TestProgram_withDependencyRegistersNil: a nil value is registered, not skipped.
func TestProgram_withDependencyRegistersNil(t *testing.T) {
	t.Parallel()
	dep := NewDependency[*int]("claims.nil")
	var val *int
	found := false
	h := &testHandlers{log: new([]string), onRun: func(rtx *Context) { val, found = rtx.GetDependency(dep) }}
	p, _, _ := newTestProgram(h, []string{"run"})
	if _, err := p.WithDependency(dep, nil).Run(p.args); err != nil {
		t.Fatal(err)
	}
	if !found || val != nil {
		t.Errorf("GetDependency = %v, %v; want a registered nil", val, found)
	}
}

// TestProgram_signalSettersLaterCallWins: of WithSignals and WithoutSignalHandling, the later
// call decides.
func TestProgram_signalSettersLaterCallWins(t *testing.T) {
	t.Parallel()
	if p := NewProgram(testDef(), nil).WithSignals(os.Interrupt).WithoutSignalHandling(); p.signalMode != signalOff {
		t.Errorf("WithSignals then WithoutSignalHandling: mode %v, want off", p.signalMode)
	}
	if p := NewProgram(testDef(), nil).WithoutSignalHandling().WithSignals(os.Interrupt); p.signalMode != signalOn {
		t.Errorf("WithoutSignalHandling then WithSignals: mode %v, want on", p.signalMode)
	}
}

// TestProgram_withDirAndWithEnviron pins the run's directory and environment: a relative dir
// is made absolute, "" and nil are the process's, a non-nil environment is the whole one and
// a later duplicate wins.
func TestProgram_withDirAndWithEnviron(t *testing.T) {
	t.Setenv("ROTINI_CLAIMS_PROCESS", "yes")
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	var dir string
	var env []string
	var procVal string
	var procOK bool
	var dup string
	h := &testHandlers{log: new([]string), onRun: func(rtx *Context) {
		dir, env = rtx.Dir(), rtx.Environ()
		procVal, procOK = rtx.LookupEnv("ROTINI_CLAIMS_PROCESS")
		dup, _ = rtx.LookupEnv("A")
	}}
	p, _, _ := newTestProgram(h, []string{"run"})
	run := func() {
		t.Helper()
		if _, err := p.WithoutSignalHandling().Run(p.args); err != nil {
			t.Fatal(err)
		}
	}

	p.WithDir("sub").WithEnviron([]string{"A=1", "A=2"})
	run()
	if dir != filepath.Join(wd, "sub") {
		t.Errorf("WithDir(sub): Dir = %q, want %q", dir, filepath.Join(wd, "sub"))
	}
	if procOK {
		t.Error("a non-nil environment still read the process's")
	}
	if dup != "2" {
		t.Errorf("LookupEnv(A) = %q, want the later duplicate", dup)
	}

	p.WithEnviron([]string{})
	run()
	if len(env) != 0 || procOK {
		t.Errorf("WithEnviron([]): Environ = %q, want empty", env)
	}

	p.WithDir("").WithEnviron(nil)
	run()
	if dir != wd || !procOK || procVal != "yes" {
		t.Errorf("WithDir(\"\")/WithEnviron(nil): Dir %q, process var %q %v; want the process's", dir, procVal, procOK)
	}
}

// TestWithResolver_errorChannelAndChain pins where a resolver error goes: a usage error is a
// recorded error, anything else a fault; and the chain it returns is what the reporter's
// CommandPath names.
func TestWithResolver_errorChannelAndChain(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name          string
		err           error
		errs, panics  int
		path          string
		partialChain  bool
		wantCategory  Category
		wantInMessage string
	}{
		{"usage error, partial chain", UsageError(errors.New("bad token")), 1, 0, "app run", true, CategoryUsage, "bad token"},
		{"plain error, no chain", errors.New("on fire"), 0, 1, "app", false, CategoryInternal, "on fire"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, _, _ := newTestProgram(&testHandlers{log: new([]string)}, []string{"run"})
			p.WithResolver(func(def Definition, argv []string) (Resolution, error) {
				res, _ := DefaultResolver(def, argv)
				if !tc.partialChain {
					res = Resolution{}
				}
				return res, tc.err
			})
			var out Outcome
			var path string
			p.WithReporter(func(_ context.Context, rtx *Context, o Outcome) { out, path = o, rtx.CommandPath() })
			_, err := p.Run(p.args)
			if len(out.Errors) != tc.errs || len(out.Panics) != tc.panics {
				t.Errorf("errors %d, panics %d; want %d, %d", len(out.Errors), len(out.Panics), tc.errs, tc.panics)
			}
			if CategoryOf(err) != tc.wantCategory || !strings.Contains(err.Error(), tc.wantInMessage) {
				t.Errorf("err = %v (%v), want %v", err, CategoryOf(err), tc.wantCategory)
			}
			if path != tc.path {
				t.Errorf("CommandPath = %q, want %q", path, tc.path)
			}
		})
	}
}

// TestResolution_nilArgvKeepsTheOriginal: a Resolution without Argv leaves Context.Argv as the
// words the program was given.
func TestResolution_nilArgvKeepsTheOriginal(t *testing.T) {
	t.Parallel()
	var argv []string
	h := &testHandlers{log: new([]string), onRun: func(rtx *Context) { argv = rtx.Argv }}
	p, _, _ := newTestProgram(h, []string{"st", "alice"})
	p.WithResolver(func(def Definition, _ []string) (Resolution, error) {
		res, err := DefaultResolver(def, []string{"run", "alice"})
		return Resolution{Chain: res.Chain}, err
	})
	if _, err := p.Run(p.args); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(argv, []string{"st", "alice"}) {
		t.Errorf("Argv = %q, want the original words", argv)
	}
}

// TestProgram_completeWithNoWords: no words completes a new first word.
func TestProgram_completeWithNoWords(t *testing.T) {
	t.Parallel()
	p := NewProgram(testDef(), nil)
	for _, words := range [][]string{nil, {""}} {
		got := values(resultOf(t, p, words...))
		if !slices.Contains(got, "run") {
			t.Errorf("Complete(%q) = %q, want the root's commands", words, got)
		}
	}
}

// TestCompletionRules_askedOncePerRequest: the messages and descriptions rules are asked once
// each, and the messages rule not at all when the program has no completion messages.
func TestCompletionRules_askedOncePerRequest(t *testing.T) {
	t.Parallel()
	for _, on := range []bool{true, false} {
		def := msgDef("")
		if !on {
			def.CompletionMessages = nil
		}
		messages, descriptions := 0, 0
		p := NewProgram(def, &msgHandlers{offer: []string{"api", "web"}}).
			WithCompletionMessages(func(*Context) bool { messages++; return true }).
			WithCompletionDescriptions(func(*Context) bool { descriptions++; return true })
		got := messagesOf(t, p, "deploy", "")
		wantMessages := 0
		if on {
			wantMessages = 1
		}
		if messages != wantMessages || descriptions != 1 {
			t.Errorf("messages on=%v: rules asked %d and %d times, want %d and 1", on, messages, descriptions, wantMessages)
		}
		if !on && got != nil {
			t.Errorf("messages off in the definition: Messages = %q, want none", got)
		}
	}
}

// TestRun_pluginNonZeroExitSkipsTheReporter: a plugin's own failure is its exit code and its
// *exec.ExitError; nothing is recorded, so the reporter is not called.
func TestRun_pluginNonZeroExitSkipsTheReporter(t *testing.T) {
	writeFakeBinary(t, "app-fail", "#!/bin/sh\nexit 3\n")
	def := Definition{Name: "app", Handler: "App", Plugins: []PluginDef{{Name: "fail", Binary: "app-fail"}}}
	p, _, _ := pluginProgram(def, []string{"fail"})
	called := false
	p.WithReporter(func(context.Context, *Context, Outcome) { called = true })
	code, err := p.Run(p.args)
	var ee *exec.ExitError
	if code != 3 || !errors.As(err, &ee) || ee.ExitCode() != 3 {
		t.Errorf("Run = %d, %v; want 3 and the plugin's *exec.ExitError", code, err)
	}
	if called {
		t.Error("the reporter ran for a plugin's own exit")
	}
}

// TestFeature_userHomes: UserHomes wires "~user" lookup alone, and PathExpansion includes it.
func TestFeature_userHomes(t *testing.T) {
	t.Parallel()
	if got := UserHomes().String(); got != "user-homes" {
		t.Errorf("UserHomes() = %q", got)
	}
	homes := newFeatureSet([]Feature{UserHomes()})
	if homes.lookupUser == nil || homes.expand != nil {
		t.Error("UserHomes did not wire exactly the ~user lookup")
	}
	if expand := newFeatureSet([]Feature{PathExpansion()}); expand.lookupUser == nil || expand.expand == nil {
		t.Error("PathExpansion does not include UserHomes")
	}
}
