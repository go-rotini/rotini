package rtk

import (
	"bytes"
	"fmt"
	"io"
	"os"
)

// IO bundles the three standard streams a handler interacts with: stdin,
// stdout, stderr. Generated code binds an [*IO] under the "io" registry
// key; handlers retrieve it via rtk.Get[*rtk.IO].
//
// The default value (returned by [NewIO]) reads from os.Stdin and writes
// to os.Stdout / os.Stderr. Tests and embedded uses swap streams via the
// chained With* methods:
//
//	io := rtk.NewIO().WithStdoutWriter(&buf)
type IO struct {
	// Stdin is the input stream the handler reads from.
	Stdin *Reader

	// Stdout is the main output stream the handler writes to.
	Stdout *Writer

	// Stderr is the diagnostic output stream the handler writes to.
	Stderr *Writer
}

// NewIO returns an [*IO] bound to the OS-default streams. Use the With*
// methods to swap streams for tests or embedded contexts.
func NewIO() *IO {
	return &IO{
		Stdin:  &Reader{reader: os.Stdin},
		Stdout: &Writer{writer: os.Stdout},
		Stderr: &Writer{writer: os.Stderr},
	}
}

// WithStdinReader replaces the stdin reader. A nil reader is treated as
// an immediately-EOF stream.
func (rio *IO) WithStdinReader(r io.Reader) *IO {
	if r == nil {
		r = nopReader{}
	}
	rio.Stdin.reader = r
	return rio
}

// WithStdoutWriter replaces the stdout writer. A nil writer routes to
// [io.Discard].
func (rio *IO) WithStdoutWriter(w io.Writer) *IO {
	if w == nil {
		w = io.Discard
	}
	rio.Stdout.writer = w
	return rio
}

// WithStderrWriter replaces the stderr writer. A nil writer routes to
// [io.Discard].
func (rio *IO) WithStderrWriter(w io.Writer) *IO {
	if w == nil {
		w = io.Discard
	}
	rio.Stderr.writer = w
	return rio
}

// Reader wraps an [io.Reader] (defaulting to os.Stdin) with convenience
// helpers handlers reach for: read-all-to-string, read-all-to-bytes, with
// and without trimming.
//
// Reader detects whether the underlying stream is "piped" (a non-tty
// file). For a TTY-bound stdin, the raw-read helpers return nil/empty —
// they do not block waiting for terminal input. This matches the standard
// "stdin is only consumed when something is piped" semantic.
type Reader struct {
	reader io.Reader
}

// ReadBytes returns the stream's content with leading/trailing whitespace
// trimmed. Returns nil bytes (no error) when the stream is a TTY.
func (r *Reader) ReadBytes() ([]byte, error) {
	data, err := r.ReadRawBytes()
	if err != nil {
		return nil, err
	}
	return bytes.TrimSpace(data), nil
}

// ReadRawBytes returns the stream's content verbatim (no trimming).
// Returns nil bytes (no error) when the stream is a TTY.
func (r *Reader) ReadRawBytes() ([]byte, error) {
	if !r.isPiped() {
		return nil, nil
	}
	data, err := io.ReadAll(r.reader)
	if err != nil {
		return nil, fmt.Errorf("rtk: read stdin: %w", err)
	}
	return data, nil
}

// ReadRawString returns the stream's content as a string with no trimming.
func (r *Reader) ReadRawString() (string, error) {
	data, err := r.ReadRawBytes()
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// ReadString returns the stream's content as a string with leading/
// trailing whitespace trimmed.
func (r *Reader) ReadString() (string, error) {
	data, err := r.ReadBytes()
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// Read is an alias for ReadString — the most common handler operation.
func (r *Reader) Read() (string, error) {
	return r.ReadString()
}

// isPiped reports whether the underlying reader is a non-TTY stream
// (i.e., something was piped or redirected into stdin). When the reader
// is not an *os.File (e.g., a bytes.Buffer in tests), it is assumed to
// carry data and is treated as piped.
func (r *Reader) isPiped() bool {
	f, ok := r.reader.(*os.File)
	if !ok {
		return true
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return (info.Mode() & os.ModeCharDevice) == 0
}

// Writer wraps an [io.Writer] (defaulting to os.Stdout/Stderr) with the
// fmt.Fprint/Fprintf/Fprintln triad handlers reach for.
type Writer struct {
	writer io.Writer
}

// Write satisfies the [io.Writer] interface.
func (w *Writer) Write(b []byte) (int, error) {
	n, err := w.writer.Write(b)
	if err != nil {
		return n, fmt.Errorf("rtk: write: %w", err)
	}
	return n, nil
}

// Print writes the operands using their default formats.
func (w *Writer) Print(a ...any) (int, error) {
	n, err := fmt.Fprint(w.writer, a...)
	if err != nil {
		return n, fmt.Errorf("rtk: print: %w", err)
	}
	return n, nil
}

// Printf writes the operands per the supplied format specifier.
func (w *Writer) Printf(format string, a ...any) (int, error) {
	n, err := fmt.Fprintf(w.writer, format, a...)
	if err != nil {
		return n, fmt.Errorf("rtk: printf: %w", err)
	}
	return n, nil
}

// Println writes the operands using their default formats with a trailing
// newline.
func (w *Writer) Println(a ...any) (int, error) {
	n, err := fmt.Fprintln(w.writer, a...)
	if err != nil {
		return n, fmt.Errorf("rtk: println: %w", err)
	}
	return n, nil
}

// nopReader is the substitute reader used when [IO.WithStdinReader] is
// passed nil: it immediately returns EOF.
type nopReader struct{}

func (nopReader) Read(_ []byte) (int, error) { return 0, io.EOF }
