//go:build unix

package rotini

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"syscall"
	"testing"
	"time"
)

// raiseAndWait sends the process SIGUSR1 and waits for the run context to see it.
func raiseAndWait(t *testing.T, ctx context.Context) {
	t.Helper()
	_ = syscall.Kill(syscall.Getpid(), syscall.SIGUSR1)
	select {
	case <-ctx.Done():
	case <-time.After(2 * time.Second):
		t.Error("the signal did not cancel the run context")
	}
}

// A signal during teardown ends the run with 128+n however forward progress stopped: a clean
// halt, a halt with an error, or none.
func TestSignal_duringTeardownAfterAHalt(t *testing.T) {
	want := 128 + int(syscall.SIGUSR1)
	for _, tc := range []struct {
		name string
		stop comboHook
		code int
	}{
		{"no halt", func(context.Context, *Context) {}, want},
		{"Halt", func(_ context.Context, rtx *Context) { rtx.Halt() }, want},
		{"Halt in Run", nil, want},
		{"HaltWith", func(_ context.Context, rtx *Context) { rtx.HaltWith(errors.New("stop")) }, want},
		{"HaltWithCode keeps its code", func(_ context.Context, rtx *Context) { rtx.HaltWithCode(3) }, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			actions := map[string]comboHook{
				"c1.CascadingPreRun":  tc.stop,
				"c0.CascadingPostRun": func(ctx context.Context, _ *Context) { raiseAndWait(t, ctx) },
			}
			if tc.stop == nil {
				actions["c1.Run"] = func(_ context.Context, rtx *Context) { rtx.Halt() }
			}
			p, argv, _, _ := comboTree(2, actions)
			p.WithSignals(syscall.SIGUSR1)
			if code, _ := p.Run(argv); code != tc.code {
				t.Errorf("code = %d, want %d", code, tc.code)
			}
		})
	}
}

// A signal in each forward hook stops forward progress there, unwinds the begun steps and
// exits 128+n, with nothing reported.
func TestSignal_inEachForwardHook(t *testing.T) {
	want := 128 + int(syscall.SIGUSR1)
	for _, tc := range []struct {
		hook  string
		after []string // the hooks that run after it
	}{
		{"c0.CascadingPreRun", []string{"c0.CascadingPostRun"}},
		{"c1.CascadingPreRun", []string{"c1.CascadingPostRun", "c0.CascadingPostRun"}},
		{"c1.PreRun", []string{"c1.PostRun", "c1.CascadingPostRun", "c0.CascadingPostRun"}},
		{"c1.Run", []string{"c1.PostRun", "c1.CascadingPostRun", "c0.CascadingPostRun"}},
		{"c1.PostRun", []string{"c1.CascadingPostRun", "c0.CascadingPostRun"}},
	} {
		t.Run(tc.hook, func(t *testing.T) {
			p, argv, log, errs := comboTree(2, map[string]comboHook{
				tc.hook: func(ctx context.Context, _ *Context) { raiseAndWait(t, ctx) },
			})
			p.WithSignals(syscall.SIGUSR1)
			code, err := p.Run(argv)
			if code != want || err != nil {
				t.Errorf("Run = %d, %v; want %d, nil", code, err, want)
			}
			i := slices.Index(*log, tc.hook)
			if got := (*log)[i+1:]; i < 0 || !slices.Equal(got, tc.after) {
				t.Errorf("after %s ran %v, want %v", tc.hook, got, tc.after)
			}
			if errs.Len() != 0 {
				t.Errorf("stderr = %q, want nothing reported", errs.String())
			}
		})
	}
}

// The structured reporter leaves out the error the signal caused as the default does, and
// reports any other error with the signal's exit code.
func TestSignal_structuredReporter(t *testing.T) {
	want := 128 + int(syscall.SIGUSR1)
	p, argv, _, errs := comboTree(1, map[string]comboHook{
		"c0.Run": func(ctx context.Context, rtx *Context) {
			raiseAndWait(t, ctx)
			rtx.RecordError(ctx.Err())
			rtx.HaltWith(UsageError(errors.New("half done")))
		},
	})
	p.WithSignals(syscall.SIGUSR1).WithReporter(StructuredReporter(func(*Context) bool { return true }))
	if code, _ := p.Run(argv); code != want {
		t.Errorf("code = %d, want %d", code, want)
	}
	line := `{"schema_version":1,"error":{"category":"usage","command":"c0","exit_code":` + strconv.Itoa(want) + `,"message":"half done"}}` + "\n"
	if errs.String() != line {
		t.Errorf("stderr:\n got %q\nwant %q", errs.String(), line)
	}
}
