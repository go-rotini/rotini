//go:build unix

package rotini

import (
	"context"
	"io"
	"os"
	"os/signal"
	"syscall"
	"testing"
)

type execNoop struct{ NoHooks }

func (execNoop) Run(context.Context, *Context) {}

// countStops counts stopSignals calls for the rest of the test.
func countStops(t *testing.T) *int {
	t.Helper()
	var n int
	prev := stopSignals
	stopSignals = func(c chan<- os.Signal) {
		n++
		prev(c)
	}
	t.Cleanup(func() {
		stopSignals = prev
		signal.Reset(syscall.SIGUSR1) // a handler Execute left registered
	})
	return &n
}

func execProgram() *Program {
	def := Definition{Name: "app", Handler: "App"}
	return NewWiredProgram(def, func(string) (Handler, bool) { return execNoop{}, true }).
		WithArgs(nil).WithStdout(io.Discard).WithStderr(io.Discard)
}

// TestExecute_signalStop pins when a run unregisters its signal handler: never when Execute
// runs under the default exit action, since the process exits next; always for Run,
// RunContext with WithSignals, and Execute under WithExit.
func TestExecute_signalStop(t *testing.T) {
	defer swapTrapSignals(syscall.SIGUSR1)()

	t.Run("Execute, default exit", func(t *testing.T) {
		stops := countStops(t)
		p := execProgram()
		code := -1
		p.exit = func(c int) { code = c } // the default action, without ending the test
		if !p.exitDefault {
			t.Fatal("exitDefault = false for a new program")
		}
		if err := p.Execute(); err != nil || code != 0 {
			t.Fatalf("Execute = %v, exit %d; want nil, 0", err, code)
		}
		if *stops != 0 {
			t.Errorf("signal.Stop called %d times; want 0", *stops)
		}
	})
	t.Run("Execute, WithExit", func(t *testing.T) {
		stops := countStops(t)
		code := -1
		p := execProgram().WithExit(func(c int) { code = c })
		if err := p.Execute(); err != nil || code != 0 {
			t.Fatalf("Execute = %v, exit %d; want nil, 0", err, code)
		}
		if *stops != 1 {
			t.Errorf("signal.Stop called %d times; want 1", *stops)
		}
	})
	t.Run("Execute, WithExit(nil) restores the default", func(t *testing.T) {
		p := execProgram().WithExit(func(int) {}).WithExit(nil)
		if !p.exitDefault {
			t.Error("exitDefault = false after WithExit(nil)")
		}
	})
	t.Run("Run", func(t *testing.T) {
		stops := countStops(t)
		if code, err := execProgram().Run(nil); err != nil || code != 0 {
			t.Fatalf("Run = %d, %v; want 0, nil", code, err)
		}
		if *stops != 1 {
			t.Errorf("signal.Stop called %d times; want 1", *stops)
		}
	})
	t.Run("RunContext with WithSignals", func(t *testing.T) {
		stops := countStops(t)
		p := execProgram().WithSignals(syscall.SIGUSR1)
		if code, err := p.RunContext(t.Context(), nil); err != nil || code != 0 {
			t.Fatalf("RunContext = %d, %v; want 0, nil", code, err)
		}
		if *stops != 1 {
			t.Errorf("signal.Stop called %d times; want 1", *stops)
		}
	})
	t.Run("Execute without a trap", func(t *testing.T) {
		stops := countStops(t)
		p := execProgram().WithoutSignalHandling()
		p.exit = func(int) {}
		if err := p.Execute(); err != nil {
			t.Fatal(err)
		}
		if *stops != 0 {
			t.Errorf("signal.Stop called %d times; want 0", *stops)
		}
	})
}
