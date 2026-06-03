//go:build unix

package rotini

import (
	"context"
	"syscall"
	"testing"
	"time"
)

// On a Unix host, WithSignals must cancel the run context when the configured signal
// arrives, so a long-running handler that selects on ctx.Done() can shut down
// gracefully. SIGUSR1 is used (not SIGINT/SIGTERM) so the test never disturbs the
// test runner; signal.NotifyContext catches it, so it does not terminate the process.
func TestProgram_WithSignals_cancelsContextOnSignal(t *testing.T) {
	done := make(chan struct{})
	h := ctxRec{run: func(ctx context.Context) {
		_ = syscall.Kill(syscall.Getpid(), syscall.SIGUSR1)
		select {
		case <-ctx.Done():
			close(done)
		case <-time.After(2 * time.Second):
		}
	}}
	newCtxProgram(h).WithSignals(syscall.SIGUSR1).run(nil)

	select {
	case <-done:
		// the signal canceled the run context — graceful-shutdown wiring works
	default:
		t.Error("WithSignals did not cancel the run context when the configured signal arrived")
	}
}
