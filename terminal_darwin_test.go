package rotini

import (
	"bytes"
	"os"
	"syscall"
	"testing"
	"unsafe"
)

// openTerminal opens the terminal side of a new pseudo-terminal, or skips the test where none
// can be opened.
func openTerminal(t *testing.T) *os.File {
	t.Helper()
	const (
		ptyGrant  = 0x20007454 // TIOCPTYGRANT
		ptyUnlock = 0x20007452 // TIOCPTYUNLK
		ptyName   = 0x40807453 // TIOCPTYGNAME
	)
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		t.Skipf("no pseudo-terminal: %v", err)
	}
	t.Cleanup(func() { master.Close() })
	var name [128]byte
	for _, req := range []uintptr{ptyGrant, ptyUnlock, ptyName} {
		var arg uintptr
		if req == ptyName {
			arg = uintptr(unsafe.Pointer(&name[0]))
		}
		if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), req, arg); errno != 0 {
			t.Skipf("pseudo-terminal setup: %v", errno)
		}
	}
	return openPeer(t, string(name[:bytes.IndexByte(name[:], 0)]))
}

func openPeer(t *testing.T, path string) *os.File {
	t.Helper()
	f, err := os.OpenFile(path, os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Skipf("open %s: %v", path, err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}
