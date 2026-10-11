package rotini

import "syscall"

// isTerminalFd reports whether fd is a console handle.
func isTerminalFd(fd uintptr) bool {
	var mode uint32
	return syscall.GetConsoleMode(syscall.Handle(fd), &mode) == nil
}
