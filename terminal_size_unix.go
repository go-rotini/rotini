//go:build unix

package rotini

import (
	"os"
	"syscall"
	"unsafe"
)

// winsize mirrors struct winsize from <termios.h>. Only the first two fields are used; the
// pixel dimensions are carried so the struct's size matches what the kernel writes.
type winsize struct {
	rows, cols     uint16
	xpixel, ypixel uint16
}

// terminalSize asks the kernel for the window size via TIOCGWINSZ.
//
// A zero in either axis is reported as no answer rather than passed on: some terminals and most
// CI environments return 0x0 for a character device, and a caller that trusted a zero width
// would divide by it or render nothing at all.
func terminalSize(file *os.File) (cols, rows int, ok bool) {
	if file == nil || !IsTerminal(file) {
		return 0, 0, false
	}
	var ws winsize
	_, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL,
		file.Fd(),
		uintptr(syscall.TIOCGWINSZ),
		uintptr(unsafe.Pointer(&ws)),
	)
	if errno != 0 || ws.cols == 0 || ws.rows == 0 {
		return 0, 0, false
	}
	return int(ws.cols), int(ws.rows), true
}
