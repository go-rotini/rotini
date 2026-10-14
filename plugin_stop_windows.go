package rotini

import (
	"context"
	"os"
)

// stopPlugin acts when a running plugin's context ends. After a trapped Ctrl-C it does
// nothing: the console delivered the same event to the plugin, which gets the grace period to
// stop. Anything else (a timeout or another cancellation) kills it, since Windows can't send it
// a signal.
func stopPlugin(runCtx context.Context, proc *os.Process) error {
	if signalCanceled(runCtx) {
		return nil
	}
	return proc.Kill()
}
