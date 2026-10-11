//go:build !mutation && unix

package e2e

import (
	"os"
	"runtime"
	"syscall"
)

// maxRSS is a finished process's peak resident memory in bytes. Linux reports it in kilobytes,
// darwin in bytes.
func maxRSS(ps *os.ProcessState) (int64, bool) {
	ru, ok := ps.SysUsage().(*syscall.Rusage)
	if !ok {
		return 0, false
	}
	if runtime.GOOS == "darwin" || runtime.GOOS == "ios" {
		return ru.Maxrss, true
	}
	return ru.Maxrss << 10, true
}
