//go:build unix

package rotini

import (
	"context"
	"os/signal"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// A run that ignores cancellation is ended by the termination timeout with the first signal's
// code, not the second-signal code.
func TestTerminationTimeout_exitsWithSignalCode(t *testing.T) {
	defer swapTrapSignals(syscall.SIGUSR1)()

	var exited atomic.Int32
	release := make(chan struct{})
	h := sigRec{log: &[]string{}, fire: func(ctx context.Context) {
		_ = syscall.Kill(syscall.Getpid(), syscall.SIGUSR1)
		<-ctx.Done()
		select { // a handler that ignores the halt and keeps working
		case <-release:
		case <-time.After(2 * time.Second):
			t.Error("the termination timeout never called the exit action")
		}
	}}
	p := newSigProgram(h).WithTerminationTimeout(50 * time.Millisecond)
	p.exit = func(code int) {
		exited.Store(int32(code))
		close(release)
	}
	p.Run(nil)

	if got, want := int(exited.Load()), 128+int(syscall.SIGUSR1); got != want {
		t.Errorf("exit action got %d, want %d (the first signal's code)", got, want)
	}
}

// A run that finishes before the timeout never calls the exit action.
func TestTerminationTimeout_finishedRunNeverExits(t *testing.T) {
	defer swapTrapSignals(syscall.SIGUSR1)()

	var called atomic.Bool
	h := sigRec{log: &[]string{}, fire: func(ctx context.Context) {
		_ = syscall.Kill(syscall.Getpid(), syscall.SIGUSR1)
		<-ctx.Done()
	}}
	p := newSigProgram(h).WithTerminationTimeout(100 * time.Millisecond)
	p.exit = func(int) { called.Store(true) }
	code, _ := p.Run(nil)
	time.Sleep(200 * time.Millisecond)

	if called.Load() {
		t.Error("the exit action ran for a run that had already returned")
	}
	if want := 128 + int(syscall.SIGUSR1); code != want {
		t.Errorf("code = %d, want %d", code, want)
	}
}

// d <= 0 keeps the default: no exit until the run returns.
func TestTerminationTimeout_zeroKeepsDefault(t *testing.T) {
	defer swapTrapSignals(syscall.SIGUSR1)()

	var called atomic.Bool
	h := sigRec{log: &[]string{}, fire: func(ctx context.Context) {
		_ = syscall.Kill(syscall.Getpid(), syscall.SIGUSR1)
		<-ctx.Done()
		time.Sleep(100 * time.Millisecond)
	}}
	p := newSigProgram(h).WithTerminationTimeout(10 * time.Millisecond).WithTerminationTimeout(0)
	p.exit = func(int) { called.Store(true) }
	p.Run(nil)

	if called.Load() {
		t.Error("the exit action ran with the timeout reset to the default")
	}
}

// Without a trap there is nothing to time: a signal neither halts the run nor starts a timer.
func TestTerminationTimeout_noTrapNoTimeout(t *testing.T) {
	defer swapTrapSignals(syscall.SIGUSR1)()
	signal.Ignore(syscall.SIGUSR1)
	defer signal.Reset(syscall.SIGUSR1)

	var called atomic.Bool
	h := sigRec{log: &[]string{}, fire: func(context.Context) {
		_ = syscall.Kill(syscall.Getpid(), syscall.SIGUSR1)
		time.Sleep(100 * time.Millisecond)
	}}
	p := newSigProgram(h).WithoutSignalHandling().WithTerminationTimeout(10 * time.Millisecond)
	p.exit = func(int) { called.Store(true) }
	if code, _ := p.Run(nil); code != 0 {
		t.Errorf("code = %d, want 0", code)
	}
	if called.Load() {
		t.Error("the exit action ran without a signal trap")
	}
}

// A second signal still forces 130 before the timer runs out.
func TestTerminationTimeout_secondSignalFirst(t *testing.T) {
	defer swapTrapSignals(syscall.SIGUSR1)()

	var forced atomic.Int32
	h := sigRec{log: &[]string{}, fire: func(ctx context.Context) {
		_ = syscall.Kill(syscall.Getpid(), syscall.SIGUSR1)
		<-ctx.Done()
		deadline := time.After(2 * time.Second)
		for forced.Load() == 0 {
			_ = syscall.Kill(syscall.Getpid(), syscall.SIGUSR1)
			select {
			case <-deadline:
				t.Error("second signal did not force exit")
				return
			case <-time.After(30 * time.Millisecond):
			}
		}
	}}
	p := newSigProgram(h).WithTerminationTimeout(time.Hour)
	p.exit = func(code int) { forced.CompareAndSwap(0, int32(code)) }
	p.Run(nil)

	if got := forced.Load(); got != forceExitCode {
		t.Errorf("exit code = %d, want %d", got, forceExitCode)
	}
}
