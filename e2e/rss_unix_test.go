//go:build !mutation && unix && !linux

package e2e

import (
	"os/exec"
	"runtime"
	"syscall"
)

// runPeakRSS runs cmd and returns its peak resident memory in bytes, from its rusage: darwin
// reports it in bytes, the BSDs in kilobytes.
func runPeakRSS(cmd *exec.Cmd) (int64, bool, error) {
	if err := cmd.Run(); err != nil {
		return 0, false, err
	}
	ru, ok := cmd.ProcessState.SysUsage().(*syscall.Rusage)
	if !ok {
		return 0, false, nil
	}
	if runtime.GOOS == "darwin" || runtime.GOOS == "ios" {
		return ru.Maxrss, true, nil
	}
	return ru.Maxrss << 10, true, nil
}
