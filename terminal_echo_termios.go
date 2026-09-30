//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly

package rotini

import (
	"os"
	"syscall"
	"unsafe"
)

// disableEcho clears the ECHO bit on file's terminal, returning a function that puts it back.
// echoGet and echoSet are the platform's termios ioctl requests (terminal_echo_linux.go,
// terminal_echo_bsd.go).
//
// The restore closure captures the ORIGINAL termios rather than flipping the bit again: a
// terminal may have had echo off already, and turning it on would be as wrong as leaving it off.
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

// termiosIoctl issues one termios get or set request on file, reporting whether it succeeded.
func termiosIoctl(file *os.File, req uintptr, t *syscall.Termios) bool {
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, file.Fd(), req, uintptr(unsafe.Pointer(t)))
	return errno == 0
}
