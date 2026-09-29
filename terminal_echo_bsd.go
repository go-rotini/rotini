//go:build darwin || freebsd || netbsd || openbsd || dragonfly

package rotini

import (
	"os"
	"syscall"
	"unsafe"
)

const (
	echoGet = syscall.TIOCGETA
	echoSet = syscall.TIOCSETA
)

// disableEcho clears the ECHO bit on file's terminal, returning a function that puts it back.
// See the linux build of this file for why the restore captures the original state.
func disableEcho(file *os.File) (restore func(), ok bool) {
	if file == nil || !IsTerminal(file) {
		return nil, false
	}
	var before syscall.Termios
	if !termiosIoctl(file, echoGet, &before) {
		return nil, false
	}
	after := before
	after.Lflag &^= syscall.ECHO
	if !termiosIoctl(file, echoSet, &after) {
		return nil, false
	}
	original := before
	return func() { termiosIoctl(file, echoSet, &original) }, true
}

func termiosIoctl(file *os.File, req uintptr, t *syscall.Termios) bool {
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, file.Fd(), req, uintptr(unsafe.Pointer(t)))
	return errno == 0
}
