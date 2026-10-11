package rotini

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestIsTerminal_notTerminals(t *testing.T) {
	t.Parallel()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	regular, err := os.Create(filepath.Join(t.TempDir(), "f"))
	if err != nil {
		t.Fatal(err)
	}
	defer regular.Close()
	devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer devNull.Close()
	var nilFile *os.File

	for name, stream := range map[string]any{
		"pipe read end":  r,
		"pipe write end": w,
		"regular file":   regular,
		"null device":    devNull,
		"buffer":         &bytes.Buffer{},
		"nil":            nil,
		"nil file":       nilFile,
		"string":         "stdout",
	} {
		if IsTerminal(stream) {
			t.Errorf("IsTerminal(%s) = true, want false", name)
		}
	}
}

type fdOnly uintptr

func (f fdOnly) Fd() uintptr { return uintptr(f) }

func TestIsTerminal_usesFd(t *testing.T) {
	t.Parallel()
	if IsTerminal(fdOnly(^uintptr(0))) {
		t.Error("an invalid descriptor reported a terminal")
	}
	tty := openTerminal(t)
	if !IsTerminal(fdOnly(tty.Fd())) {
		t.Error("a value whose Fd is a terminal did not report one")
	}
	if !IsTerminal(tty) {
		t.Error("a terminal *os.File did not report one")
	}
}
