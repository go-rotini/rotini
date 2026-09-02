package rotini

import (
	"fmt"
	"io"
	"strings"
)

// [Table]: rows of cells rendered as aligned columns, measured by DISPLAY width so
// styled and wide-rune text line up. The rendering half of [Printer]'s table format,
// and usable on its own.

// Align is a table column's horizontal alignment.
type Align int

// The column alignments. Left is the default; right suits numeric columns.
const (
	AlignLeft  Align = iota // the default
	AlignRight              // for numeric columns
	AlignCenter
)

// Table renders rows of cells as aligned columns, configured by chaining and rendered with
// [Table.Render] or [Table.Fprint]. Column widths are measured with [Width], so cells carrying
// ANSI styling align by what the terminal displays rather than by byte length.
//
// Terminal width is not detected: an unbounded table renders at its natural width, and a
// program that wants it fitted passes the budget in with [Table.WithWidth].
//
// The zero value is not usable; start from [NewTable].
type Table struct {
	headers []string
	rows    [][]string
	aligns  []Align
	pad     int
	width   int
	styler  *Styler
	headKey string
}

// NewTable returns a table with the given header cells. Passing none renders the
// rows with no header line.
func NewTable(headers ...string) *Table {
	return &Table{headers: headers, pad: 2}
}

// Row appends one row of cells. A row shorter than the widest row is padded with
// empty cells at render time, so ragged input is legal. It returns the receiver
// to chain.
func (t *Table) Row(cells ...string) *Table {
	t.rows = append(t.rows, cells)
	return t
}

// WithAlign sets the per-column alignment, left to right. Columns past the end of
// aligns keep [AlignLeft].
func (t *Table) WithAlign(aligns ...Align) *Table {
	t.aligns = aligns
	return t
}

// WithPadding sets the gap between columns in cells (default 2).
func (t *Table) WithPadding(n int) *Table {
	if n >= 0 {
		t.pad = n
	}
	return t
}

// WithWidth bounds the rendered table to n display cells, truncating the widest
// columns (with a trailing "…") until it fits. Zero — the default — renders at
// natural width.
func (t *Table) WithWidth(n int) *Table {
	if n >= 0 {
		t.width = n
	}
	return t
}

// WithStyler renders the header line through styler's named style (default
// "header"), leaving body cells unstyled. A table without a styler renders plain,
// so styling stays optional.
func (t *Table) WithStyler(styler *Styler) *Table {
	t.styler = styler
	if t.headKey == "" {
		t.headKey = "header"
	}
	return t
}

// WithHeaderStyle names the [Styler] key the header line renders through
// (default "header").
func (t *Table) WithHeaderStyle(key string) *Table {
	t.headKey = key
	return t
}

// Render returns the aligned table as text, one line per row, with no trailing
// newline after the last line. An empty table renders as "".
func (t *Table) Render() string {
	lines := t.lines()
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n")
}

// Fprint writes the table to w followed by a newline. An empty table writes
// nothing at all — so a command with no results prints no stray blank line.
func (t *Table) Fprint(w io.Writer) (int, error) {
	out := t.Render()
	if out == "" {
		return 0, nil
	}
	n, err := io.WriteString(w, out)
	if err != nil {
		return n, fmt.Errorf("rotini: write table: %w", err)
	}
	m, err := io.WriteString(w, "\n")
	if err != nil {
		return n + m, fmt.Errorf("rotini: write table: %w", err)
	}
	return n + m, nil
}

// lines renders each row into a single padded line.
func (t *Table) lines() []string {
	cols := t.columnCount()
	if cols == 0 {
		return nil
	}
	widths := t.columnWidths(cols)
	t.fit(widths)

	out := make([]string, 0, len(t.rows)+1)
	if len(t.headers) > 0 {
		head := t.line(t.headers, widths)
		if t.styler != nil {
			head = t.styler.Render(t.headKey, head)
		}
		out = append(out, head)
	}
	for _, r := range t.rows {
		out = append(out, t.line(r, widths))
	}
	return out
}

// columnCount is the widest row (headers included).
func (t *Table) columnCount() int {
	n := len(t.headers)
	for _, r := range t.rows {
		if len(r) > n {
			n = len(r)
		}
	}
	return n
}

// columnWidths measures each column at its widest cell.
func (t *Table) columnWidths(cols int) []int {
	widths := make([]int, cols)
	measure := func(cells []string) {
		for i, c := range cells {
			if w := Width(c); w > widths[i] {
				widths[i] = w
			}
		}
	}
	measure(t.headers)
	for _, r := range t.rows {
		measure(r)
	}
	return widths
}

// fit shrinks the widest column repeatedly until the total fits the width budget.
// Columns never shrink below one cell (the ellipsis), so a budget too small to
// hold the table simply yields the narrowest table possible rather than an error.
func (t *Table) fit(widths []int) {
	if t.width <= 0 {
		return
	}
	gaps := t.pad * (len(widths) - 1)
	total := gaps
	for _, w := range widths {
		total += w
	}
	for total > t.width {
		widest, idx := 0, -1
		for i, w := range widths {
			if w > widest {
				widest, idx = w, i
			}
		}
		if idx < 0 || widths[idx] <= 1 {
			return // nothing left to give
		}
		widths[idx]--
		total--
	}
}

// line renders one row's cells into a padded, aligned line with trailing
// whitespace trimmed (so a short last column adds no invisible padding).
func (t *Table) line(cells []string, widths []int) string {
	var b strings.Builder
	for i, w := range widths {
		cell := ""
		if i < len(cells) {
			cell = cells[i]
		}
		if i > 0 {
			b.WriteString(strings.Repeat(" ", t.pad))
		}
		b.WriteString(pad(truncate(cell, w), w, t.alignOf(i)))
	}
	return strings.TrimRight(b.String(), " ")
}

func (t *Table) alignOf(i int) Align {
	if i < len(t.aligns) {
		return t.aligns[i]
	}
	return AlignLeft
}

// truncate shortens text to at most width display cells, marking the cut with a trailing "…"
// that itself costs one cell. Styled text is truncated by display width, its escape sequences
// dropped along with the runes they would have decorated, since a partial sequence would
// corrupt the terminal.
func truncate(text string, width int) string {
	if width <= 0 || Width(text) <= width {
		return text
	}
	plain := Strip(text)
	var b strings.Builder
	used := 0
	for _, r := range plain {
		rw := runeWidth(r)
		if used+rw > width-1 {
			break
		}
		b.WriteRune(r)
		used += rw
	}
	return b.String() + "…"
}

// pad grows text to width display cells in the given alignment.
func pad(text string, width int, align Align) string {
	gap := width - Width(text)
	if gap <= 0 {
		return text
	}
	switch align {
	case AlignRight:
		return strings.Repeat(" ", gap) + text
	case AlignCenter:
		left := gap / 2
		return strings.Repeat(" ", left) + text + strings.Repeat(" ", gap-left)
	default:
		return text + strings.Repeat(" ", gap)
	}
}
