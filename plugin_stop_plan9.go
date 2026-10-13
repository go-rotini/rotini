package rotini

import (
	"context"
	"os"
)

// stopPlugin asks a running plugin to stop when its context ends by posting it the interrupt
// note. The plugin is killed if it is still running after the grace period.
func stopPlugin(_ context.Context, proc *os.Process) error {
	return proc.Signal(os.Interrupt)
}
