//go:build darwin || freebsd || netbsd || openbsd

package rotini

import (
	"syscall"
	"unsafe"
)

// isTerminalFd reports whether fd is a terminal: the TIOCGETA ioctl succeeds only on one.
func isTerminalFd(fd uintptr) bool {
	var t syscall.Termios
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, syscall.TIOCGETA, uintptr(unsafe.Pointer(&t)))
	return errno == 0
}
