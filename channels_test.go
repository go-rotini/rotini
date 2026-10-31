package rotini

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
)

type skippedFile struct{ Path, Reason string }

func (s skippedFile) String() string { return "skipped " + s.Path + ": " + s.Reason }

type conflictError struct{ Key string }

func (e *conflictError) Error() string { return "conflict on " + e.Key }

var (
	chSkipped  = NewChannel[skippedFile]("skipped", ChannelClassInfo)
	chDone     = NewChannel[string]("done", ChannelClassSuccess)
	chStale    = NewChannel[string]("stale", ChannelClassWarning)
	chConflict = NewChannel[*conflictError]("conflict", ChannelClassError)
)

// runChannels runs a one-command program whose Run calls run, with reporter in place of the
// default when it is non-nil.
func runChannels(t *testing.T, run func(rtx *Context), reporter Reporter) (code int, err error, stderr string) {
	t.Helper()
	p := NewProgramFunc(Definition{Name: "app", Handler: "App"}, func(string) (Handler, bool) {
		return &funcRun{run: run}, true
	})
	if reporter != nil {
		p.WithReporter(reporter)
	}
	var o, e bytes.Buffer
	p.stdout, p.stderr = &o, &e
	code, err = p.Run(nil)
	return code, err, e.String()
}

type funcRun struct {
	NoHooks
	run func(rtx *Context)
}

func (h *funcRun) Run(_ context.Context, rtx *Context) { h.run(rtx) }

func TestChannels_defaultReporter(t *testing.T) {
	t.Parallel()
	code, err, stderr := runChannels(t, func(rtx *Context) {
		rtx.RecordSuccess("built-in success")
		rtx.Record(chDone, "custom success")
		rtx.Record(chSkipped, skippedFile{Path: "a.bin", Reason: "binary"})
		rtx.RecordInfo("built-in info")
		rtx.Record(chStale, "cache is old")
		rtx.RecordWarning(errors.New("built-in warning"))
		rtx.Record(chConflict, &conflictError{Key: "k"})
		rtx.RecordError(errors.New("built-in error"))
	}, nil)
	want := strings.Join([]string{
		"built-in info",
		"skipped a.bin: binary",
		"Warning: built-in warning",
		"Warning: cache is old",
		"Error: built-in error",
		"Error: conflict on k",
		"built-in success",
		"custom success",
	}, "\n") + "\n"
	if stderr != want {
		t.Errorf("stderr =\n%s\nwant\n%s", stderr, want)
	}
	var ce *conflictError
	if code != 1 || !errors.As(err, &ce) || ce.Key != "k" {
		t.Errorf("code=%d err=%v; want 1 and the conflict joined into Run's error", code, err)
	}
}

func TestChannels_errorClassAloneFails(t *testing.T) {
	t.Parallel()
	var failed bool
	code, err, _ := runChannels(t, func(rtx *Context) {
		rtx.Record(chConflict, &conflictError{Key: "k"})
		failed = rtx.Failed()
	}, nil)
	if code != 1 || err == nil || !failed {
		t.Errorf("code=%d err=%v Failed=%v; a ChannelClassError record is a failure", code, err, failed)
	}
}

func TestChannels_nonErrorClassesDoNotFail(t *testing.T) {
	t.Parallel()
	code, err, _ := runChannels(t, func(rtx *Context) {
		rtx.Record(chSkipped, skippedFile{Path: "a"})
		rtx.Record(chStale, "old")
		rtx.Record(chDone, "ok")
	}, nil)
	if code != 0 || err != nil {
		t.Errorf("code=%d err=%v; want a clean run", code, err)
	}
}

func TestChannels_customReporterReadsTyped(t *testing.T) {
	t.Parallel()
	var got []skippedFile
	var other []string
	var empty bool
	code, _, stderr := runChannels(t, func(rtx *Context) {
		rtx.Record(chSkipped, skippedFile{Path: "a", Reason: "binary"})
		rtx.Record(chSkipped, skippedFile{Path: "b", Reason: "too big"})
	}, func(_ context.Context, rtx *Context, out Outcome) {
		got = out.Records(chSkipped)
		other = out.Records(chDone)
		empty = out.Empty()
		got[0].Path = "changed" // a copy: must not reach a second read
		if out.Records(chSkipped)[0].Path != "a" {
			rtx.Exit(9)
		}
	})
	if code != 0 || stderr != "" || empty || other != nil {
		t.Errorf("code=%d stderr=%q empty=%v other=%v", code, stderr, empty, other)
	}
	if len(got) != 2 || got[1].Reason != "too big" {
		t.Errorf("records = %v", got)
	}
}

func TestChannels_onlyChannelRecordsReachReporter(t *testing.T) {
	t.Parallel()
	called := false
	runChannels(t, func(rtx *Context) { rtx.Record(chDone, "ok") },
		func(context.Context, *Context, Outcome) { called = true })
	if !called {
		t.Error("an Outcome holding only channel records counted as empty")
	}
}

func TestChannels_nilIgnoredAndMismatchFaults(t *testing.T) {
	t.Parallel()
	var nilConflict *conflictError
	errCh := NewChannel[error]("errs", ChannelClassError)
	sameName := NewChannel[int]("skipped", ChannelClassInfo)
	var out Outcome
	code, _, _ := runChannels(t, func(rtx *Context) {
		rtx.Record(errCh, nil)
		rtx.Record(chConflict, nilConflict) // a typed nil pointer is a value, and is kept
		rtx.Record(chSkipped, skippedFile{Path: "a"})
		rtx.Record(sameName, 1)
		rtx.Record(Channel[string]{}, "zero channel")
	}, func(_ context.Context, rtx *Context, o Outcome) { out = o; rtx.Exit(0) })
	if code != 0 {
		t.Fatalf("code = %d", code)
	}
	if n := len(out.Records(errCh)); n != 0 {
		t.Errorf("nil error recorded %d times", n)
	}
	if n := len(out.Records(chConflict)); n != 1 {
		t.Errorf("typed nil pointer: %d records, want 1", n)
	}
	if len(out.Records(sameName)) != 0 || len(out.Records(chSkipped)) != 1 {
		t.Errorf("a same-named channel of another type shared records")
	}
	var we *WiringError
	if len(out.Panics) != 1 || !errors.As(out.Panics[0], &we) || !strings.Contains(we.Msg, `channel "skipped"`) {
		t.Errorf("panics = %v, want one WiringError naming the channel", out.Panics)
	}
}

func TestChannels_concurrentRecords(t *testing.T) {
	t.Parallel()
	var n int
	runChannels(t, func(rtx *Context) {
		var wg sync.WaitGroup
		for i := range 50 {
			wg.Go(func() { rtx.Record(chDone, fmt.Sprint(i)) })
		}
		wg.Wait()
	}, func(_ context.Context, _ *Context, out Outcome) { n = len(out.Records(chDone)) })
	if n != 50 {
		t.Errorf("records = %d, want 50", n)
	}
}

func TestChannels_structuredReporter(t *testing.T) {
	t.Parallel()
	_, _, stderr := runChannels(t, func(rtx *Context) {
		rtx.Record(chSkipped, skippedFile{Path: "a", Reason: "binary"})
		rtx.Record(chConflict, &conflictError{Key: "k"})
	}, StructuredReporter(func(*Context) bool { return true }))
	want := []string{
		`{"schema_version":1,"info":{"command":"app","message":"skipped a: binary"}}`,
		`{"schema_version":1,"error":{"category":"none","command":"app","exit_code":1,"message":"conflict on k"}}`,
	}
	if got := strings.Split(strings.TrimSuffix(stderr, "\n"), "\n"); !slices.Equal(got, want) {
		t.Errorf("stderr =\n%s\nwant\n%s", stderr, strings.Join(want, "\n"))
	}
}

func TestNewChannel_panics(t *testing.T) {
	t.Parallel()
	for name, fn := range map[string]func(){
		"empty name":                  func() { NewChannel[string]("", ChannelClassInfo) },
		"built-in name":               func() { NewChannel[string]("warning", ChannelClassInfo) },
		"unknown class":               func() { NewChannel[string]("x", ChannelClass(9)) },
		"ChannelClassError non-error": func() { NewChannel[string]("x", ChannelClassError) },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			defer func() {
				if recover() == nil {
					t.Error("no panic")
				}
			}()
			fn()
		})
	}
}

func TestChannelClass_String(t *testing.T) {
	t.Parallel()
	for c, want := range map[ChannelClass]string{
		ChannelClassInfo: "info", ChannelClassSuccess: "success", ChannelClassWarning: "warning", ChannelClassError: "error", 7: "ChannelClass(7)",
	} {
		if got := c.String(); got != want {
			t.Errorf("%d.String() = %q, want %q", c, got, want)
		}
	}
	if chSkipped.Name() != "skipped" || chSkipped.String() != "skipped" || chSkipped.Class() != ChannelClassInfo {
		t.Error("channel accessors")
	}
}
