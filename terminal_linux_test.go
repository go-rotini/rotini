package rotini

import (
	"os"
	"strconv"
	"syscall"
	"testing"
	"unsafe"
)

// openTerminal opens the terminal side of a new pseudo-terminal, or skips the test where none
// can be opened.
func openTerminal(t *testing.T) *os.File {
	t.Helper()
	const (
		ptyUnlock = 0x40045431 // TIOCSPTLCK
		ptyNumber = 0x80045430 // TIOCGPTN
	)
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		t.Skipf("no pseudo-terminal: %v", err)
	}
	t.Cleanup(func() { master.Close() })
	var unlock int32
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), ptyUnlock, uintptr(unsafe.Pointer(&unlock))); errno != 0 {
		t.Skipf("pseudo-terminal unlock: %v", errno)
	}
	var n uint32
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), ptyNumber, uintptr(unsafe.Pointer(&n))); errno != 0 {
		t.Skipf("pseudo-terminal number: %v", errno)
	}
	path := "/dev/pts/" + strconv.FormatUint(uint64(n), 10)
	f, err := os.OpenFile(path, os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Skipf("open %s: %v", path, err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}
