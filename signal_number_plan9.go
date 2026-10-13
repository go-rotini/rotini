package rotini

import (
	"os"
	"os/exec"
)

// signalNumber reports false: a Plan 9 note has no number.
func signalNumber(os.Signal) (int, bool) { return 0, false }

// exitSignal reports false: a Plan 9 exit status names no signal.
func exitSignal(*exec.ExitError) (int, bool) { return 0, false }
