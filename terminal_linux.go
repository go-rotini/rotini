package rotini

import (
	"syscall"
	"unsafe"
)

// isTerminalFd reports whether fd is a terminal: the TCGETS ioctl succeeds only on one.
func isTerminalFd(fd uintptr) bool {
	var t syscall.Termios
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, syscall.TCGETS, uintptr(unsafe.Pointer(&t)))
	return errno == 0
}
