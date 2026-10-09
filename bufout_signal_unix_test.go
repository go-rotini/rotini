//go:build unix

package rotini

import (
	"bytes"
	"fmt"
	"syscall"
	"testing"
	"time"
)

func TestBufferedOutput_signalFlushes(t *testing.T) {
	defer swapTrapSignals(syscall.SIGUSR1)()
	var out bytes.Buffer
	p := NewProgram(Definition{Name: "app", Handler: "App"}, &funcHandlers{run: func(rtx *Context) {
		fmt.Fprint(rtx.Stdout, "partial")
		_ = syscall.Kill(syscall.Getpid(), syscall.SIGUSR1)
		select {
		case <-rtx.Context().Done():
		case <-time.After(2 * time.Second):
			t.Error("the signal did not cancel the run")
		}
	}}).WithBufferedOutput(true).WithStdout(&out).WithStderr(&bytes.Buffer{})

	code, _ := p.Run(nil)
	if want := 128 + int(syscall.SIGUSR1); code != want {
		t.Errorf("exit = %d, want %d", code, want)
	}
	if out.String() != "partial" {
		t.Errorf("stdout = %q, want %q", out.String(), "partial")
	}
}

func TestBufferedOutput_forcedExitFlushes(t *testing.T) {
	defer swapTrapSignals(syscall.SIGUSR1)()
	var out bytes.Buffer
	exited := make(chan string, 1)
	p := NewProgram(Definition{Name: "app", Handler: "App"}, &funcHandlers{run: func(rtx *Context) {
		fmt.Fprint(rtx.Stdout, "partial")
		_ = syscall.Kill(syscall.Getpid(), syscall.SIGUSR1)
		<-rtx.Context().Done()
		_ = syscall.Kill(syscall.Getpid(), syscall.SIGUSR1)
		select {
		case got := <-exited:
			if got != "partial" {
				t.Errorf("stdout at the forced exit = %q, want %q", got, "partial")
			}
		case <-time.After(2 * time.Second):
			t.Error("the second signal did not force an exit")
		}
	}}).WithBufferedOutput(true).WithStdout(&out).WithStderr(&bytes.Buffer{}).
		WithExit(func(int) { exited <- out.String() })
	p.Run(nil)
}
