//go:build !plan9

package rotini

import (
	"os"
	"os/exec"
	"syscall"
)

// signalNumber is s's number, false for a signal that has none.
func signalNumber(s os.Signal) (int, bool) {
	sig, ok := s.(syscall.Signal)
	return int(sig), ok
}

// exitSignal is the number of the signal that ended the process ee reports, false when no
// signal ended it.
func exitSignal(ee *exec.ExitError) (int, bool) {
	if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return int(ws.Signal()), true
	}
	return 0, false
}
