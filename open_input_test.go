package rotini

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOpenInput_dashIsStdinByteForByte(t *testing.T) {
	t.Parallel()
	payload := "\xef\xbb\xbfline one\r\nline two\n\n"
	rtx := newContext().WithStdin(strings.NewReader(payload))
	r, err := OpenInput(rtx, "-")
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(r)
	if err != nil || string(data) != payload {
		t.Fatalf("read %q, %v; want the payload unchanged", data, err)
	}
	if err := r.Close(); err != nil {
		t.Errorf("Close = %v", err)
	}
}

func TestOpenInput_interleavesFilesAndStdin(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for name, body := range map[string]string{"a.txt": "a\n", "b.txt": "b\n"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	rtx := newContext().WithDir(dir).WithStdin(strings.NewReader("piped\n"))
	var got strings.Builder
	for _, name := range []string{"a.txt", "-", "b.txt"} {
		r, err := OpenInput(rtx, name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.Copy(&got, r); err != nil {
			t.Fatal(err)
		}
		r.Close()
	}
	if got.String() != "a\npiped\nb\n" {
		t.Errorf("got %q", got.String())
	}
}

func TestOpenInput_replaysASlurp(t *testing.T) {
	t.Parallel()
	rtx := newContext().WithStdin(strings.NewReader("once"))
	if text, err := rtx.slurpStdin(); err != nil || text != "once" {
		t.Fatalf("slurp = %q, %v", text, err)
	}
	r, err := OpenInput(rtx, "-")
	if err != nil {
		t.Fatal(err)
	}
	if data, _ := io.ReadAll(r); string(data) != "once" {
		t.Errorf("after a slurp, - read %q", data)
	}
}

func TestOpenInput_errors(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	rtx := newContext().WithDir(dir)
	for path, want := range map[string]string{
		"missing.txt": `no such file: "missing.txt"`,
		"sub":         `"sub" is a directory, not a file`,
	} {
		_, err := OpenInput(rtx, path)
		if err == nil || err.Error() != want || CategoryOf(err) != CategoryUsage {
			t.Errorf("%s: err = %v, want the usage error %q", path, err, want)
		}
	}
	_, err := OpenInput(rtx, "missing.txt")
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("err = %v, want it to match os.ErrNotExist", err)
	}
}

func TestOpenInput_terminalStdin(t *testing.T) {
	t.Parallel()
	tty := openTerminal(t)
	_, err := OpenInput(newContext().WithStdin(tty), "-")
	if err == nil || CategoryOf(err) != CategoryUsage || !strings.Contains(err.Error(), "stdin is a terminal") {
		t.Errorf("err = %v, want a usage error", err)
	}
}

func TestOpenInput_nullDeviceIsEmpty(t *testing.T) {
	t.Parallel()
	null, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer null.Close()
	r, err := OpenInput(newContext().WithStdin(null), "-")
	if err != nil {
		t.Fatal(err)
	}
	if data, err := io.ReadAll(r); err != nil || len(data) != 0 {
		t.Errorf("read %q, %v", data, err)
	}
}

func TestOpenInput_canceledRead(t *testing.T) {
	t.Parallel()
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer pw.Close()
	defer pr.Close()
	rtx := newContext().WithStdin(pr)
	ctx, cancel := context.WithCancelCause(context.Background())
	rtx.bindRun(ctx)
	r, err := OpenInput(rtx, "-")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := r.Read(make([]byte, 8))
		done <- err
	}()
	cancel(ExitCause(130))
	select {
	case err := <-done:
		if _, ok := errors.AsType[*InputError](err); !ok || !errors.Is(err, ExitCause(130)) {
			t.Errorf("err = %v, want an *InputError carrying the cancellation", err)
		}
	case <-time.After(time.Second):
		t.Fatal("the read did not end when the run was canceled")
	}
	rtx.endStdin()
}
