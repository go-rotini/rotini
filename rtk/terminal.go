package rtk

import (
	"os"
	"strconv"

	xterm "golang.org/x/term"
)

// Terminal is rtk's terminal-capabilities service: it reports what the process is
// attached to — whether a stream is a TTY, the output window size, and how much
// color the output supports — and switches input to raw mode for char-at-a-time
// reading. It is the companion to the [IO] service: IO moves bytes (over io.Reader/
// Writer, so a test can swap in buffers), while Terminal answers questions about the
// physical terminal device, so its streams are *os.File.
//
// Like the other rtk services it is bound once and retrieved by handlers:
//
//	// main.go
//	rth.Program.Bind("terminal", rtk.NewTerminal()).Execute()
//
//	// a handler
//	t := rotini.MustGet[*rtk.Terminal](rtx, "terminal")
//	if t.IsTerminal() {
//	    fmt.Println(rtk.Style("ready", t.ColorLevel(), rtk.Green, rtk.Bold))
//	}
//
// The color rendering it feeds ([Color], [Style], [ColorLevel]) and the cursor/line
// control sequences ([ClearLine], [CursorUp], …) live alongside it in this package
// as plain values — Terminal supplies the level, those render to it.
type Terminal struct {
	stdin, stdout, stderr *os.File
	level                 *ColorLevel // forced color level; nil = detect
	size                  *Size       // forced size; nil = detect
}

// NewTerminal returns a Terminal wired to the process streams (os.Stdin/Stdout/
// Stderr), ready to bind under the "terminal" registry key.
func NewTerminal() *Terminal {
	return &Terminal{stdin: os.Stdin, stdout: os.Stdout, stderr: os.Stderr}
}

// WithStdin replaces the input stream and returns the receiver so overrides chain —
// for tests that point a stream at a pipe (never a terminal) or a pty.
func (t *Terminal) WithStdin(f *os.File) *Terminal { t.stdin = f; return t }

// WithStdout replaces the output stream (the one Size/ColorLevel/IsTerminal report on).
func (t *Terminal) WithStdout(f *os.File) *Terminal { t.stdout = f; return t }

// WithStderr replaces the error stream.
func (t *Terminal) WithStderr(f *os.File) *Terminal { t.stderr = f; return t }

// WithColorLevel forces the color level, overriding detection — wire it to a global
// --color / --no-color flag so the user's choice wins.
func (t *Terminal) WithColorLevel(l ColorLevel) *Terminal { t.level = &l; return t }

// WithSize forces the reported output size, overriding detection.
func (t *Terminal) WithSize(s Size) *Terminal { t.size = &s; return t }

// IsTerminal reports whether the output stream (stdout) is a terminal — the usual
// gate for color and in-place redraws.
func (t *Terminal) IsTerminal() bool { return isTTY(t.stdout) }

// StdinIsTerminal reports whether the input stream is a terminal (e.g. before
// prompting interactively).
func (t *Terminal) StdinIsTerminal() bool { return isTTY(t.stdin) }

// StderrIsTerminal reports whether the error stream is a terminal.
func (t *Terminal) StderrIsTerminal() bool { return isTTY(t.stderr) }

// Size returns the output terminal's size: the forced size if one was set, else the
// real size, else the COLUMNS/LINES environment hints, else [DefaultSize]. It never
// errors.
func (t *Terminal) Size() Size {
	if t.size != nil {
		return *t.size
	}
	if t.stdout != nil {
		if w, h, err := xterm.GetSize(int(t.stdout.Fd())); err == nil && w > 0 && h > 0 {
			return Size{Cols: w, Rows: h}
		}
	}
	return envSize()
}

// ColorLevel returns how much color the output supports: the forced level if one was
// set, else detected from whether stdout is a terminal and the conventional
// environment overrides (NO_COLOR, FORCE_COLOR, CLICOLOR/CLICOLOR_FORCE, COLORTERM,
// TERM).
func (t *Terminal) ColorLevel() ColorLevel {
	if t.level != nil {
		return *t.level
	}
	return detectColorLevel(t.IsTerminal(), os.LookupEnv)
}

// MakeRaw puts the input stream (stdin) into raw mode — bytes delivered one at a
// time, no echo, no line editing — and returns the prior state for [Terminal.Restore].
// Pair it with a deferred Restore so the terminal is returned to normal even on panic:
//
//	st, err := t.MakeRaw()
//	if err != nil { /* stdin is not a terminal */ }
//	defer t.Restore(st)
//
// It errors when stdin is not a terminal.
func (t *Terminal) MakeRaw() (*RawState, error) {
	if t.stdin == nil {
		return nil, os.ErrInvalid
	}
	st, err := xterm.MakeRaw(int(t.stdin.Fd()))
	if err != nil {
		return nil, err
	}
	return &RawState{inner: st}, nil
}

// Restore returns stdin to the state captured by [Terminal.MakeRaw]. A nil state is a
// no-op, so deferring Restore with the result of a failed MakeRaw is harmless.
func (t *Terminal) Restore(s *RawState) error {
	if t.stdin == nil || s == nil || s.inner == nil {
		return nil
	}
	return xterm.Restore(int(t.stdin.Fd()), s.inner)
}

// Size is a terminal's dimensions in character cells.
type Size struct {
	Cols int // columns (width)
	Rows int // rows (height)
}

// DefaultSize is the conventional fallback used when a real size cannot be
// determined and no COLUMNS/LINES hint is set.
var DefaultSize = Size{Cols: 80, Rows: 24}

// RawState holds terminal state captured by [Terminal.MakeRaw], to be handed back to
// [Terminal.Restore]. A nil *RawState restores to nothing (a safe no-op).
type RawState struct{ inner *xterm.State }

// isTTY reports whether f is connected to a terminal (a nil file is not).
func isTTY(f *os.File) bool { return f != nil && xterm.IsTerminal(int(f.Fd())) }

// envSize reads the COLUMNS/LINES hints over [DefaultSize]. Some shells export these
// even when an ioctl is unavailable (e.g. inside a pipeline).
func envSize() Size {
	s := DefaultSize
	if c, err := strconv.Atoi(os.Getenv("COLUMNS")); err == nil && c > 0 {
		s.Cols = c
	}
	if r, err := strconv.Atoi(os.Getenv("LINES")); err == nil && r > 0 {
		s.Rows = r
	}
	return s
}
