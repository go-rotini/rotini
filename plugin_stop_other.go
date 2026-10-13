//go:build !windows && !plan9

package rotini

import (
	"context"
	"os"
	"syscall"
)

// stopPlugin asks a running plugin to stop when its context ends: the signal that canceled the
// run, so the plugin sees what the host saw, and SIGTERM for a timeout or any other
// cancellation. The plugin is killed if it is still running after the grace period.
func stopPlugin(runCtx context.Context, proc *os.Process) error {
	sig := os.Signal(syscall.SIGTERM)
	if code := canceledExitCode(runCtx); signalCanceled(runCtx) && code > 128 {
		sig = syscall.Signal(code - 128)
	}
	return proc.Signal(sig) //nolint:wrapcheck // exec reports it as the plugin's own
}
