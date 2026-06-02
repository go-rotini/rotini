package rtk

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	xterm "golang.org/x/term"
)

// Printer is rtk's styled-output service: a writer pair (out + err) that knows the
// output's color level and width, so handlers emit color, semantic messages, and
// tables that degrade correctly on a pipe, an old terminal, or with NO_COLOR — without
// re-deriving any of that. It is the output counterpart to the [Terminal] service
// (which it uses to detect the level/width) and pairs with a command's generated
// <Prefix>Output type via [Printer.JSON].
//
// Like the other rtk services it is bound once and retrieved by handlers:
//
//	// main.go
//	rth.Program.Bind("printer", rtk.NewPrinter()).Execute()
//
//	// a handler
//	p := rotini.MustGet[*rtk.Printer](rtx, "printer")
//	p.Success("created %s", name)
//	p.NewTable("NAME", "AGE").Row("alice", "30").Render()
//
// Writes that are informational (Print/Println/Printf, Success, Info, tables) go to
// out; Warning and Error go to err — the conventional split. Color is gated by the
// detected level, so the same calls are safe whether or not the destination supports
// it.
type Printer struct {
	out, err io.Writer
	level    ColorLevel
	width    int
}

// NewPrinter returns a Printer writing to os.Stdout/os.Stderr, with the color level
// and width detected from stdout, ready to bind under the "printer" registry key.
func NewPrinter() *Printer {
	level, width := detectFor(os.Stdout)
	return &Printer{out: os.Stdout, err: os.Stderr, level: level, width: width}
}

// WithOutput replaces the output writer and re-detects the color level and width from
// it (a non-*os.File, e.g. a test buffer, yields [ColorNone] and [DefaultSize] width).
// Force a level/width afterward with [Printer.WithColorLevel]/[Printer.WithWidth] —
// the last override wins.
func (p *Printer) WithOutput(w io.Writer) *Printer {
	p.out = w
	p.level, p.width = detectFor(w)
	return p
}

// WithError replaces the error writer (Warning/Error go here).
func (p *Printer) WithError(w io.Writer) *Printer { p.err = w; return p }

// WithColorLevel forces the color level — wire it to a global --color/--no-color flag.
func (p *Printer) WithColorLevel(l ColorLevel) *Printer { p.level = l; return p }

// WithWidth forces the reported width (used by tables and wrapping); n<=0 is ignored.
func (p *Printer) WithWidth(n int) *Printer {
	if n > 0 {
		p.width = n
	}
	return p
}

// ColorLevel and Width expose the resolved settings (e.g. to lay out to the width).
func (p *Printer) ColorLevel() ColorLevel { return p.level }
func (p *Printer) Width() int             { return p.width }

// Print, Printf, and Println write unstyled to the output writer (fmt.Fprint* semantics).
func (p *Printer) Print(a ...any) (int, error) { return fmt.Fprint(p.out, a...) }
func (p *Printer) Printf(format string, a ...any) (int, error) {
	return fmt.Fprintf(p.out, format, a...)
}
func (p *Printer) Println(a ...any) (int, error) { return fmt.Fprintln(p.out, a...) }

// Style renders text at the printer's color level (a convenience over the package-level
// [Style], which it calls with that level).
func (p *Printer) Style(text string, fg Color, attrs ...Attr) string {
	return Style(text, p.level, fg, attrs...)
}

// Success prints a green line to out.
func (p *Printer) Success(format string, a ...any) {
	_, _ = fmt.Fprintln(p.out, Style(fmt.Sprintf(format, a...), p.level, Green))
}

// Info prints an unstyled line to out.
func (p *Printer) Info(format string, a ...any) {
	_, _ = fmt.Fprintln(p.out, fmt.Sprintf(format, a...))
}

// Warning prints a "warning: …" line (yellow, bold label) to err.
func (p *Printer) Warning(format string, a ...any) {
	_, _ = fmt.Fprintln(p.err, Style("warning", p.level, Yellow, Bold)+": "+fmt.Sprintf(format, a...))
}

// Error prints an "error: …" line (red, bold label) to err.
func (p *Printer) Error(format string, a ...any) {
	_, _ = fmt.Fprintln(p.err, Style("error", p.level, Red, Bold)+": "+fmt.Sprintf(format, a...))
}

// JSON pretty-prints v as indented JSON to out — the formatter for a command's
// generated <Prefix>Output value (or any data a handler wants to emit as JSON).
func (p *Printer) JSON(v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(p.out, string(b))
	return err
}

// detectFor resolves the color level and width for a writer: from the real terminal
// when w is an *os.File, else [ColorNone] and the [DefaultSize] width (a buffer/pipe).
func detectFor(w io.Writer) (ColorLevel, int) {
	f, ok := w.(*os.File)
	if !ok {
		return ColorNone, DefaultSize.Cols
	}
	level := detectColorLevel(isTTY(f), os.LookupEnv)
	width := envSize().Cols
	if cw, _, err := xterm.GetSize(int(f.Fd())); err == nil && cw > 0 {
		width = cw
	}
	return level, width
}
