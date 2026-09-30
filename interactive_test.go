package rotini

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ReadSecret is tested through a PIPE and a FILE, never a terminal — which is deliberate, and
// the same bargain every interactive test in this repo makes. A tty is what the ioctl path
// needs, and it is also the environment that hides the two defects worth guarding: input eaten
// past the line, and a caller that cannot run in CI at all.
//
// What a terminal alone would prove — that ECHO is actually cleared — is a three-line ioctl
// whose correctness is the restore, and the restore is `defer`. There is nothing a test can
// observe there that reading the code does not already say.

// tempFile writes content to a real file and returns it open, because ReadSecret takes an
// *os.File: it has to be able to find the terminal underneath, which an io.Reader would hide.
func tempFile(t *testing.T, content string) *os.File {
	t.Helper()
	path := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}

func TestReadSecret_readsOneLine(t *testing.T) {
	for name, content := range map[string]string{
		"newline-terminated":  "hunter2\n",
		"no trailing newline": "hunter2",
		"crlf":                "hunter2\r\n",
	} {
		t.Run(name, func(t *testing.T) {
			got, err := ReadSecret(tempFile(t, content))
			if err != nil {
				t.Fatalf("ReadSecret: %v", err)
			}
			if string(got) != "hunter2" {
				t.Errorf("ReadSecret = %q, want %q — the terminator leaked into the secret", got, "hunter2")
			}
		})
	}
}

// TestReadSecret_leavesTheRestOfTheStream is the defect a buffered read would reintroduce.
//
// A reader that fills a private buffer takes everything available, returns one line, and drops
// the rest when it goes out of scope. The next consumer — a prompt, a subprocess, a scanner —
// sees EOF with its input still in the pipe. A terminal hides it completely, because a tty
// hands over one line at a time and there is never a surplus to lose.
func TestReadSecret_leavesTheRestOfTheStream(t *testing.T) {
	f := tempFile(t, "hunter2\nkeep me\nand me\n")

	if _, err := ReadSecret(f); err != nil {
		t.Fatalf("ReadSecret: %v", err)
	}

	// The file on disk is untouched; what matters is what the open HANDLE has left.
	var remaining strings.Builder
	buf := make([]byte, 64)
	for {
		n, err := f.Read(buf)
		remaining.Write(buf[:n])
		if err != nil {
			break
		}
	}
	if remaining.String() != "keep me\nand me\n" {
		t.Errorf("after ReadSecret the stream held %q, want the two lines that followed the secret",
			remaining.String())
	}
}

// TestReadSecret_endOfInputIsNamed: the CI case. An empty stream must not read as an empty
// password, and must not hang.
func TestReadSecret_endOfInputIsNamed(t *testing.T) {
	got, err := ReadSecret(tempFile(t, ""))
	if !errors.Is(err, ErrNotInteractive) {
		t.Errorf("ReadSecret on an empty stream = (%q, %v), want ErrNotInteractive", got, err)
	}
	if CategoryOf(err) != CategoryUsage {
		t.Errorf("CategoryOf(ErrNotInteractive) = %v, want usage — the environment is wrong, not the program",
			CategoryOf(err))
	}
}

// TestReadSecret_nilFile: a caller that passes an unset stream gets the same named answer
// rather than a nil dereference.
func TestReadSecret_nilFile(t *testing.T) {
	if _, err := ReadSecret(nil); !errors.Is(err, ErrNotInteractive) {
		t.Errorf("ReadSecret(nil) = %v, want ErrNotInteractive", err)
	}
}

// TestReadSecret_emptyLineIsAnAnswer distinguishes "typed nothing and pressed Enter" from "the
// stream ended". The first is an empty secret, which a caller may accept or reject; the second
// is ErrNotInteractive. Collapsing them would make a scripted blank line indistinguishable from
// a CI job with no stdin at all.
func TestReadSecret_emptyLineIsAnAnswer(t *testing.T) {
	got, err := ReadSecret(tempFile(t, "\nafter\n"))
	if err != nil {
		t.Fatalf("ReadSecret on a blank line = %v, want an empty answer", err)
	}
	if len(got) != 0 {
		t.Errorf("ReadSecret = %q, want an empty secret", got)
	}
}

// TestTerminalHelpers_takeTheStreamsAHandlerHas proves the two terminal helpers accept rtx.Stdin
// and rtx.Stdout as they are. They used to take *os.File, so every prompt guard began with a
// type assertion and a script- or test-friendly fallback reader (rubectl R-14, R-38).
func TestTerminalHelpers_takeTheStreamsAHandlerHas(t *testing.T) {
	var nilFile *os.File
	for name, stream := range map[string]any{
		"a buffer": &bytes.Buffer{}, "a reader": strings.NewReader("x"), "nil": nil, "a nil *os.File": nilFile,
	} {
		if IsTerminal(stream) {
			t.Errorf("IsTerminal(%s) = true", name)
		}
	}

	rtx := newContext()
	rtx.Stdin = strings.NewReader("s3cret\r\nnext line")
	if got, err := ReadSecret(rtx.Stdin); err != nil || string(got) != "s3cret" {
		t.Errorf("ReadSecret(a reader) = %q, %v, want the first line without its CRLF", got, err)
	}
	for name, r := range map[string]io.Reader{"empty": strings.NewReader(""), "nil": nil, "a nil *os.File": nilFile} {
		if _, err := ReadSecret(r); !errors.Is(err, ErrNotInteractive) {
			t.Errorf("ReadSecret(%s) = %v, want ErrNotInteractive", name, err)
		}
	}
}
