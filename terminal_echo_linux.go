//go:build linux

package rotini

import "syscall"

// The termios ioctl requests disableEcho uses on Linux.
const (
	echoGet = syscall.TCGETS
	echoSet = syscall.TCSETS
)
