//go:build !linux && !darwin

package rotini

import (
	"os"
	"testing"
)

// openTerminal opens the console's output buffer on Windows, or skips the test where no
// console is attached or the platform has no terminal check.
func openTerminal(t *testing.T) *os.File {
	t.Helper()
	f, err := os.OpenFile("CONOUT$", os.O_RDWR, 0)
	if err != nil {
		t.Skipf("no console: %v", err)
	}
	t.Cleanup(func() { f.Close() })
	if !isTerminalFd(f.Fd()) {
		t.Skip("CONOUT$ is not a console here")
	}
	return f
}
