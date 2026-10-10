//go:build unix

package rotini

import (
	"bytes"
	"context"
	"errors"
	"os"
	"slices"
	"syscall"
	"testing"
	"time"
)

// The first trapped signal interrupts rotini's own read of a pipe whose writer stays open: the
// run returns promptly with 128+signum, teardown runs, and the recorded error carries the run's
// cancellation cause.
func TestDefaultSignals_interruptsStdinRead(t *testing.T) {
	defer swapTrapSignals(syscall.SIGUSR1)()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close() // held open for the whole run: the read never sees EOF

	var log []string
	var readErr error
	stdinReader := stdinHook{log: &log, run: func(_ context.Context, rtx *Context) {
		time.AfterFunc(50*time.Millisecond, func() { _ = syscall.Kill(syscall.Getpid(), syscall.SIGUSR1) })
		_, readErr = rtx.Inputs[tbTextInputs]()
		rtx.HaltWith(readErr)
	}}
	p := NewProgram(Definition{Name: "app", Handler: "Main"}, stdinAgg{stdinReader})
	p.stdout, p.stderr = &bytes.Buffer{}, &bytes.Buffer{}
	p.WithStdin(r)

	start := time.Now()
	code, runErr := p.Run(nil)
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("Run took %v, want the read interrupted at once", elapsed)
	}
	if want := 128 + int(syscall.SIGUSR1); code != want {
		t.Errorf("exit code = %d, want %d", code, want)
	}
	if !slices.Contains(log, "PostRun") || !slices.Contains(log, "CascadingPostRun") {
		t.Errorf("hooks = %v, want teardown to run", log)
	}
	var ie *InputError
	if !errors.As(readErr, &ie) || ie.Channel != channelStdin {
		t.Fatalf("Inputs error = %v, want the stdin interruption", readErr)
	}
	if !errors.Is(runErr, ExitCause(128+int(syscall.SIGUSR1))) {
		t.Errorf("Run error = %v, want the signal cause reachable", runErr)
	}
}

// A deadline a hook sets with SetContext ends rotini's own read of a pipe whose writer stays
// open: the read returns the deadline as an interruption, and the run exits 1 with teardown.
func TestSetContext_deadlineEndsStdinRead(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close() // held open for the whole run: the read never sees EOF

	var log []string
	var readErr error
	stdinReader := stdinHook{log: &log, run: func(ctx context.Context, rtx *Context) {
		ctx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
		defer cancel()
		rtx.SetContext(ctx)
		_, readErr = rtx.Inputs[tbTextInputs]()
		rtx.HaltWith(readErr)
	}}
	p := NewProgram(Definition{Name: "app", Handler: "Main"}, stdinAgg{stdinReader})
	p.stdout, p.stderr = &bytes.Buffer{}, &bytes.Buffer{}
	p.WithStdin(r)

	start := time.Now()
	code, _ := p.Run(nil)
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("Run took %v, want the read ended by the deadline", elapsed)
	}
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !slices.Contains(log, "PostRun") || !slices.Contains(log, "CascadingPostRun") {
		t.Errorf("hooks = %v, want teardown to run", log)
	}
	var ie *InputError
	if !errors.As(readErr, &ie) || ie.Channel != channelStdin || ie.Msg != "reading stdin was interrupted" {
		t.Fatalf("Inputs error = %v, want the stdin interruption", readErr)
	}
	if !errors.Is(readErr, context.DeadlineExceeded) {
		t.Errorf("Inputs error = %v, want the deadline reachable", readErr)
	}
}

// stdinHook is sigRec with its own Run.
type stdinHook struct {
	sigRec
	run func(context.Context, *Context)
}

func (h stdinHook) Run(ctx context.Context, rtx *Context) {
	*h.log = append(*h.log, "Run")
	h.run(ctx, rtx)
}

type stdinAgg struct{ h stdinHook }

func (a stdinAgg) Main() Handler { return a.h }
