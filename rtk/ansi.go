package rtk

import (
	"regexp"
	"strconv"
	"strings"
)

// csi is the Control Sequence Introducer that begins every ANSI escape.
const csi = "\x1b["

// Reset is the SGR sequence that clears all color and attributes.
const Reset = csi + "0m"

// Cursor and line control sequences. Write these to a terminal to drive a progress
// bar, spinner, or any in-place redraw; guard with [Terminal.IsTerminal] (and render
// plainly when it is not a terminal). Use [StripANSI] to remove them again.
const (
	ClearLine        = csi + "2K" // erase the whole current line
	ClearToLineEnd   = csi + "0K" // erase from the cursor to the end of the line
	ClearToLineStart = csi + "1K" // erase from the line start to the cursor
	ClearScreen      = csi + "2J" // erase the whole screen
	HideCursor       = csi + "?25l"
	ShowCursor       = csi + "?25h"
	CursorHome       = csi + "H" // move to row 1, column 1
)

// CursorUp returns the sequence to move the cursor up n rows (n<=0 yields "").
func CursorUp(n int) string { return cursorMove(n, 'A') }

// CursorDown returns the sequence to move the cursor down n rows.
func CursorDown(n int) string { return cursorMove(n, 'B') }

// CursorForward returns the sequence to move the cursor right n columns.
func CursorForward(n int) string { return cursorMove(n, 'C') }

// CursorBack returns the sequence to move the cursor left n columns.
func CursorBack(n int) string { return cursorMove(n, 'D') }

// CursorColumn returns the sequence to move the cursor to column col (1-based) on the
// current row.
func CursorColumn(col int) string {
	if col < 1 {
		col = 1
	}
	return csi + strconv.Itoa(col) + "G"
}

func cursorMove(n int, dir byte) string {
	if n <= 0 {
		return ""
	}
	return csi + strconv.Itoa(n) + string(dir)
}

// SGR builds an ANSI Select-Graphic-Rendition sequence from the given parameter
// codes (e.g. "1", "38;2;10;20;30"). Empty codes are skipped, so the empty results of
// [Color.Foreground] / [Color.Background] can be passed straight through; with no
// non-empty code it returns "". It is the primitive [Style] composes.
func SGR(codes ...string) string {
	out := make([]string, 0, len(codes))
	for _, c := range codes {
		if c != "" {
			out = append(out, c)
		}
	}
	if len(out) == 0 {
		return ""
	}
	return csi + strings.Join(out, ";") + "m"
}

// ansiPattern matches ANSI/VT escape sequences: a CSI sequence (ESC [ … final byte)
// or a two-byte escape. It is used by [StripANSI].
var ansiPattern = regexp.MustCompile(`\x1b(?:[@-Z\\-_]|\[[0-?]*[ -/]*[@-~])`)

// StripANSI removes ANSI escape sequences from s, leaving the visible text. Use it to
// measure printable width, or to write already-styled text to a destination that does
// not support color.
func StripANSI(s string) string {
	if !strings.ContainsRune(s, 0x1b) {
		return s
	}
	return ansiPattern.ReplaceAllString(s, "")
}
