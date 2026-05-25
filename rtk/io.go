package rtk

import (
	"bytes"
	"fmt"
	"io"
	"os"
)

// IO is an opt-in stdio service: injectable, testable stdin/stdout/stderr that a
// handler pulls off the context instead of touching os.Stdin/Stdout/Stderr and
// fmt directly. Like the [Parser], it is bound once and retrieved by handlers, so
// production wiring and a test's in-memory buffers swap at the registry seam
// without changing handler code:
//
//	// main.go
//	rth.Program.Bind("io", rtk.NewIO()).Execute()
//
//	// a handler
//	out, ok := rtx.Value("io").(*rtk.IO)
//	if !ok { /* not bound */ }
//	out.Stdout.Println("done")
//	piped, _ := out.Stdin.ReadString()
type IO struct {
	Stdin  *Reader
	Stdout *Writer
	Stderr *Writer
}

// NewIO returns an IO wired to the process streams (os.Stdin/Stdout/Stderr).
func NewIO() *IO {
	return &IO{
		Stdin:  &Reader{r: os.Stdin},
		Stdout: &Writer{w: os.Stdout},
		Stderr: &Writer{w: os.Stderr},
	}
}

// WithStdin replaces the input source and returns the receiver so overrides chain.
// A nil reader becomes an immediate-EOF nop. Use it to feed a test an in-memory
// reader.
func (s *IO) WithStdin(r io.Reader) *IO {
	if r == nil {
		r = nopReader{}
	}
	s.Stdin.r = r
	return s
}

// WithStdout replaces the standard-output sink (a nil writer becomes io.Discard).
func (s *IO) WithStdout(w io.Writer) *IO {
	if w == nil {
		w = io.Discard
	}
	s.Stdout.w = w
	return s
}

// WithStderr replaces the standard-error sink (a nil writer becomes io.Discard).
func (s *IO) WithStderr(w io.Writer) *IO {
	if w == nil {
		w = io.Discard
	}
	s.Stderr.w = w
	return s
}

// Reader is IO's input side: a handle over an io.Reader whose convenience readers
// distinguish piped input from an interactive terminal.
type Reader struct {
	r io.Reader
}

// ReadBytes reads all of the input and trims surrounding whitespace. It returns
// nil when stdin is an interactive terminal rather than a pipe/redirect (see
// [Reader.ReadRawBytes]).
func (r *Reader) ReadBytes() ([]byte, error) {
	data, err := r.ReadRawBytes()
	if err != nil {
		return nil, err
	}
	return bytes.TrimSpace(data), nil
}

// ReadRawBytes reads all of the input verbatim. When the input is an interactive
// terminal (an os.File that is a character device) rather than a pipe or redirect,
// it returns nil, nil instead of blocking on a read that would never complete — so
// a command can offer "read from stdin" without hanging when run interactively.
func (r *Reader) ReadRawBytes() ([]byte, error) {
	if !r.isPiped() {
		return nil, nil
	}
	return io.ReadAll(r.r)
}

// ReadString reads all of the input and trims surrounding whitespace.
func (r *Reader) ReadString() (string, error) {
	data, err := r.ReadBytes()
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// ReadRawString reads all of the input verbatim (see [Reader.ReadRawBytes]).
func (r *Reader) ReadRawString() (string, error) {
	data, err := r.ReadRawBytes()
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// isPiped reports whether the reader is a pipe/redirect rather than an interactive
// terminal. A non-file reader (e.g. a test buffer) counts as piped.
func (r *Reader) isPiped() bool {
	f, ok := r.r.(*os.File)
	if !ok {
		return true
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice == 0
}

// Writer is IO's output side: an io.Writer with fmt-style conveniences.
type Writer struct {
	w io.Writer
}

// Write implements [io.Writer], so a Writer can be passed anywhere an io.Writer is
// expected (e.g. fmt.Fprintln(io.Stdout, …)).
func (w *Writer) Write(b []byte) (int, error) {
	return w.w.Write(b)
}

// Print writes its operands with [fmt.Fprint] semantics.
func (w *Writer) Print(a ...any) (int, error) {
	return fmt.Fprint(w.w, a...)
}

// Printf writes a formatted string with [fmt.Fprintf] semantics.
func (w *Writer) Printf(format string, a ...any) (int, error) {
	return fmt.Fprintf(w.w, format, a...)
}

// Println writes its operands with [fmt.Fprintln] semantics.
func (w *Writer) Println(a ...any) (int, error) {
	return fmt.Fprintln(w.w, a...)
}

// nopReader is the stand-in WithStdin uses for a nil reader: every read is EOF.
type nopReader struct{}

func (nopReader) Read(_ []byte) (int, error) {
	return 0, io.EOF
}
