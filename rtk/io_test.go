package rtk_test

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/go-rotini/rotini/rtk"
)

// =============================================================================
// IO construction + stream replacement
// =============================================================================

func TestIO_NewIO_defaultsToProcessStreams(t *testing.T) {
	t.Parallel()
	rio := rtk.NewIO()
	if rio == nil {
		t.Fatal("NewIO returned nil")
	}
	if rio.Stdin == nil || rio.Stdout == nil || rio.Stderr == nil {
		t.Errorf("NewIO returned partially-initialized IO: %+v", rio)
	}
}

func TestIO_WithStdoutWriter(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	rio := rtk.NewIO().WithStdoutWriter(&buf)
	if _, err := rio.Stdout.Println("hello"); err != nil {
		t.Fatalf("Println: %v", err)
	}
	if got := buf.String(); got != "hello\n" {
		t.Errorf("Stdout: got %q, want %q", got, "hello\n")
	}
}

func TestIO_WithStderrWriter(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	rio := rtk.NewIO().WithStderrWriter(&buf)
	if _, err := rio.Stderr.Print("oops"); err != nil {
		t.Fatalf("Print: %v", err)
	}
	if got := buf.String(); got != "oops" {
		t.Errorf("Stderr: got %q, want %q", got, "oops")
	}
}

func TestIO_WithStdinReader(t *testing.T) {
	t.Parallel()
	rio := rtk.NewIO().WithStdinReader(strings.NewReader("  piped  \n"))
	got, err := rio.Stdin.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got != "piped" {
		t.Errorf("Read: got %q, want %q", got, "piped")
	}
}

func TestIO_NilStdoutRoutesToDiscard(t *testing.T) {
	t.Parallel()
	rio := rtk.NewIO().WithStdoutWriter(nil)
	n, err := rio.Stdout.Println("dropped")
	if err != nil {
		t.Fatalf("Println: %v", err)
	}
	if n == 0 {
		t.Errorf("Println: got n=0, want non-zero for io.Discard")
	}
}

func TestIO_NilStderrRoutesToDiscard(t *testing.T) {
	t.Parallel()
	rio := rtk.NewIO().WithStderrWriter(nil)
	if _, err := rio.Stderr.Println("dropped"); err != nil {
		t.Fatalf("Println: %v", err)
	}
}

func TestIO_NilStdinReadsEOF(t *testing.T) {
	t.Parallel()
	rio := rtk.NewIO().WithStdinReader(nil)
	got, err := rio.Stdin.ReadRawString()
	if err != nil {
		t.Fatalf("ReadRawString: %v", err)
	}
	if got != "" {
		t.Errorf("ReadRawString: got %q, want \"\"", got)
	}
}

// =============================================================================
// Reader semantics
// =============================================================================

func TestReader_ReadBytes_trimsWhitespace(t *testing.T) {
	t.Parallel()
	rio := rtk.NewIO().WithStdinReader(strings.NewReader("\n\t hello \r\n"))
	got, err := rio.Stdin.ReadBytes()
	if err != nil {
		t.Fatalf("ReadBytes: %v", err)
	}
	if string(got) != "hello" {
		t.Errorf("ReadBytes: got %q, want %q", got, "hello")
	}
}

func TestReader_ReadRawBytes_preservesWhitespace(t *testing.T) {
	t.Parallel()
	rio := rtk.NewIO().WithStdinReader(strings.NewReader("  hello\n"))
	got, err := rio.Stdin.ReadRawBytes()
	if err != nil {
		t.Fatalf("ReadRawBytes: %v", err)
	}
	if string(got) != "  hello\n" {
		t.Errorf("ReadRawBytes: got %q, want %q", got, "  hello\n")
	}
}

func TestReader_ReadString_trimsWhitespace(t *testing.T) {
	t.Parallel()
	rio := rtk.NewIO().WithStdinReader(strings.NewReader("  trimmed  "))
	got, err := rio.Stdin.ReadString()
	if err != nil {
		t.Fatalf("ReadString: %v", err)
	}
	if got != "trimmed" {
		t.Errorf("ReadString: got %q, want %q", got, "trimmed")
	}
}

func TestReader_ReadRawString_preservesWhitespace(t *testing.T) {
	t.Parallel()
	rio := rtk.NewIO().WithStdinReader(strings.NewReader("  raw  "))
	got, err := rio.Stdin.ReadRawString()
	if err != nil {
		t.Fatalf("ReadRawString: %v", err)
	}
	if got != "  raw  " {
		t.Errorf("ReadRawString: got %q, want %q", got, "  raw  ")
	}
}

// failingReader returns an error after the first Read call.
type failingReader struct{}

func (failingReader) Read(_ []byte) (int, error) { return 0, errFailingReader }

var errFailingReader = io.ErrUnexpectedEOF

func TestReader_PropagatesReadError(t *testing.T) {
	t.Parallel()
	rio := rtk.NewIO().WithStdinReader(failingReader{})
	_, err := rio.Stdin.ReadRawBytes()
	if err == nil {
		t.Fatal("expected error from failing reader")
	}
}

// =============================================================================
// Writer semantics
// =============================================================================

func TestWriter_WritePrintPrintfPrintln(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		op   func(*rtk.Writer) (int, error)
		want string
	}{
		{
			name: "Write",
			op:   func(w *rtk.Writer) (int, error) { return w.Write([]byte("raw")) },
			want: "raw",
		},
		{
			name: "Print",
			op:   func(w *rtk.Writer) (int, error) { return w.Print("a", 1) },
			want: "a1",
		},
		{
			name: "Printf",
			op:   func(w *rtk.Writer) (int, error) { return w.Printf("%s=%d", "k", 7) },
			want: "k=7",
		},
		{
			name: "Println",
			op:   func(w *rtk.Writer) (int, error) { return w.Println("done") },
			want: "done\n",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var buf bytes.Buffer
			rio := rtk.NewIO().WithStdoutWriter(&buf)
			if _, err := tc.op(rio.Stdout); err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			if got := buf.String(); got != tc.want {
				t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
			}
		})
	}
}

// failingWriter returns an error from every Write call.
type failingWriter struct{}

func (failingWriter) Write(_ []byte) (int, error) { return 0, io.ErrShortWrite }

func TestWriter_PropagatesWriteError(t *testing.T) {
	t.Parallel()
	rio := rtk.NewIO().WithStdoutWriter(failingWriter{})
	_, err := rio.Stdout.Println("x")
	if err == nil {
		t.Fatal("expected error from failing writer")
	}
}

// =============================================================================
// Registry binding
// =============================================================================

func TestIO_BoundInRegistry(t *testing.T) {
	t.Parallel()
	reg := rtk.NewRegistry()
	want := rtk.NewIO().WithStdoutWriter(&bytes.Buffer{})
	reg.Bind("io", want)
	got := rtk.Get[*rtk.IO](reg, "io")
	if got != want {
		t.Errorf("Get returned a different *IO than was bound")
	}
}
