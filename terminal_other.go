//go:build !linux && !darwin && !freebsd && !netbsd && !openbsd && !windows

package rotini

// isTerminalFd reports false: this platform has no terminal check.
func isTerminalFd(uintptr) bool { return false }
