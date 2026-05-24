package rtk

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

// Writer must satisfy io.Writer so it drops into fmt.Fprint*, io.Copy, etc.
var _ io.Writer = (*Writer)(nil)

func TestIO_writers(t *testing.T) {
	var out, errb bytes.Buffer
	rio := NewIO().WithStdout(&out).WithStderr(&errb)

	rio.Stdout.Println("hello")
	rio.Stdout.Printf("n=%d", 7)
	rio.Stderr.Print("oops")

	if got := out.String(); got != "hello\nn=7" {
		t.Errorf("stdout = %q, want %q", got, "hello\nn=7")
	}
	if got := errb.String(); got != "oops" {
		t.Errorf("stderr = %q, want %q", got, "oops")
	}
}

func TestIO_writerIsIOWriter(t *testing.T) {
	var buf bytes.Buffer
	w := NewIO().WithStdout(&buf).Stdout
	if _, err := io.WriteString(w, "x"); err != nil { // exercises Write
		t.Fatalf("WriteString: %v", err)
	}
	if buf.String() != "x" {
		t.Errorf("Write via io.Writer = %q, want %q", buf.String(), "x")
	}
}

func TestIO_reader(t *testing.T) {
	// A non-*os.File reader (a test buffer) counts as piped, so it is read.
	rio := NewIO().WithStdin(strings.NewReader("  piped input \n"))
	s, err := rio.Stdin.ReadString()
	if err != nil {
		t.Fatalf("ReadString: %v", err)
	}
	if s != "piped input" {
		t.Errorf("ReadString = %q, want trimmed %q", s, "piped input")
	}

	raw, err := NewIO().WithStdin(strings.NewReader("  raw \n")).Stdin.ReadRawString()
	if err != nil {
		t.Fatalf("ReadRawString: %v", err)
	}
	if raw != "  raw \n" {
		t.Errorf("ReadRawString = %q, want verbatim", raw)
	}
}

func TestIO_nilOverridesAreSafe(t *testing.T) {
	rio := NewIO().WithStdin(nil).WithStdout(nil).WithStderr(nil)

	if _, err := rio.Stdout.Println("discarded"); err != nil {
		t.Errorf("Println to discard: %v", err)
	}
	s, err := rio.Stdin.ReadString()
	if err != nil || s != "" {
		t.Errorf("nop stdin ReadString = %q, %v; want \"\", nil", s, err)
	}
}
