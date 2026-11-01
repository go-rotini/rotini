package rotini

import (
	"bytes"
	"context"
	"testing"
)

// By default a run that records nothing is not reported; WithReporterOnEmpty(true) calls the
// reporter with the empty Outcome, carrying the exit code a hook set.
func TestWithReporterOnEmpty(t *testing.T) {
	for _, tc := range []struct {
		name     string
		on       bool
		halt     int
		wantCall bool
		wantCode int
	}{
		{name: "default skips", wantCode: 0},
		{name: "on calls", on: true, wantCall: true, wantCode: 0},
		{name: "on keeps a set code", on: true, halt: 3, wantCall: true, wantCode: 3},
		{name: "off keeps a set code", halt: 3, wantCode: 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, _ := sharedProgram(func(_ context.Context, rtx *Context) error {
				if tc.halt != 0 {
					rtx.HaltWithCode(tc.halt)
				}
				return nil
			})
			var calls int
			var got Outcome
			p.WithReporterOnEmpty(tc.on).WithReporter(func(_ context.Context, _ *Context, out Outcome) {
				calls++
				got = out
			})
			code, err := p.Run(nil)
			if err != nil || code != tc.wantCode {
				t.Fatalf("Run = %d, %v; want %d", code, err, tc.wantCode)
			}
			if (calls == 1) != tc.wantCall || calls > 1 {
				t.Fatalf("reporter called %d times, want called %v", calls, tc.wantCall)
			}
			if tc.wantCall && (!got.Empty() || got.ExitCode != tc.halt) {
				t.Errorf("Outcome = %+v; want empty with ExitCode %d", got, tc.halt)
			}
		})
	}
}

// rotini's reporters print nothing for an empty Outcome and leave the exit code alone.
func TestWithReporterOnEmpty_builtInReportersPrintNothing(t *testing.T) {
	for name, report := range map[string]Reporter{
		"default":    DefaultReporter,
		"structured": StructuredReporter(func(*Context) bool { return true }),
		"text":       StructuredReporter(func(*Context) bool { return false }),
	} {
		t.Run(name, func(t *testing.T) {
			var out bytes.Buffer
			p, errs := sharedProgram(func(context.Context, *Context) error { return nil })
			p.WithStdout(&out).WithReporterOnEmpty(true).WithReporter(report)
			if code, err := p.Run(nil); code != 0 || err != nil || out.Len() != 0 || errs.Len() != 0 {
				t.Errorf("Run = %d, %v, stdout %q, stderr %q; want a silent clean run", code, err, out.String(), errs.String())
			}
		})
	}
}

// A run that records something is reported once either way.
func TestWithReporterOnEmpty_nonEmptyReportedOnce(t *testing.T) {
	p, _ := sharedProgram(func(_ context.Context, rtx *Context) error {
		rtx.RecordInfo("done")
		return nil
	})
	var calls int
	p.WithReporterOnEmpty(true).WithReporter(func(_ context.Context, _ *Context, out Outcome) {
		calls++
		if len(out.Infos) != 1 {
			t.Errorf("Infos = %q", out.Infos)
		}
	})
	if _, err := p.Run(nil); err != nil || calls != 1 {
		t.Errorf("Run = %v, reporter called %d times; want once", err, calls)
	}
}
