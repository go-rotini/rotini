//go:build darwin || freebsd || netbsd || openbsd || dragonfly

package rotini

import "syscall"

// The termios ioctl requests disableEcho uses on macOS and the BSDs.
const (
	echoGet = syscall.TIOCGETA
	echoSet = syscall.TIOCSETA
)
