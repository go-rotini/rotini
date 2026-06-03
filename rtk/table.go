package rtk

import (
	"io"
	"strings"
	"unicode/utf8"
)

// Align is a table column's horizontal alignment.
type Align int

const (
	AlignLeft   Align = iota // pad on the right (the default)
	AlignRight               // pad on the left
	AlignCenter              // pad on both sides
)

// Table is a column-aligned table built from a [Printer], so it renders to the same
// writer at the same color level. By default columns are left-aligned and sized to their
// widest cell, the header row is bold (when the level supports it), and there are no
// borders. Build it fluently and render once:
//
//	p.NewTable("NAME", "AGE").
//	    Row("alice", "30").
//	    Row("bob", "100").
//	    Render()
//
// Opt into [Table.Align] (per-column alignment), [Table.MaxWidth] (cap + truncate wide
// columns), and [Table.Bordered] (box-drawing borders). Multi-line cells are not split —
// a cell's own newlines are its responsibility.
type Table struct {
	p        *Printer
	headers  []string
	rows     [][]string
	aligns   []Align
	maxWidth int // per-column visible-width cap; 0 = unbounded
	bordered bool
}

// NewTable starts a table on the printer with the given column headers (pass none for
// a headerless table).
func (p *Printer) NewTable(headers ...string) *Table {
	return &Table{p: p, headers: headers}
}

// Row appends a row of cells and returns the table so calls chain. Short rows are
// padded with empty cells; cells may themselves contain styling (their visible width,
// ANSI stripped, is used for alignment).
func (t *Table) Row(cells ...string) *Table {
	t.rows = append(t.rows, cells)
	return t
}

// Align sets per-column horizontal alignment (column 0 first); columns past the end of
// the list keep the default [AlignLeft]. Returns the table so calls chain.
func (t *Table) Align(aligns ...Align) *Table { t.aligns = aligns; return t }

// MaxWidth caps every column to at most n visible columns, truncating longer cells with
// an ellipsis (see [Printer.Truncate]). n <= 0 (the default) leaves columns unbounded.
// Returns the table.
func (t *Table) MaxWidth(n int) *Table {
	if n > 0 {
		t.maxWidth = n
	}
	return t
}

// Bordered draws box-drawing borders around the table and between the header and the
// rows. Returns the table.
func (t *Table) Bordered() *Table { t.bordered = true; return t }

// String renders the table to a string (each row ends in a newline).
func (t *Table) String() string {
	cols := t.columns()
	if cols == 0 {
		return ""
	}
	widths := t.widths(cols)
	if t.bordered {
		return t.renderBordered(cols, widths)
	}
	return t.renderPlain(cols, widths)
}

// Render writes the table to the printer's output writer.
func (t *Table) Render() {
	_, _ = io.WriteString(t.p.out, t.String())
}

// columns is the column count: the widest of the header and any row.
func (t *Table) columns() int {
	cols := len(t.headers)
	for _, r := range t.rows {
		if len(r) > cols {
			cols = len(r)
		}
	}
	return cols
}

// widths is each column's visible width (after MaxWidth truncation).
func (t *Table) widths(cols int) []int {
	widths := make([]int, cols)
	measure := func(cells []string) {
		for i := 0; i < cols; i++ {
			if w := visibleWidth(t.cell(cells, i)); w > widths[i] {
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

// cell returns column i of cells (empty when absent), truncated to MaxWidth when set.
func (t *Table) cell(cells []string, i int) string {
	c := ""
	if i < len(cells) {
		c = cells[i]
	}
	if t.maxWidth > 0 {
		c = truncateVisible(c, t.maxWidth)
	}
	return c
}

// alignOf is column i's alignment (AlignLeft past the end of the list).
func (t *Table) alignOf(i int) Align {
	if i < len(t.aligns) {
		return t.aligns[i]
	}
	return AlignLeft
}

func (t *Table) renderPlain(cols int, widths []int) string {
	var b strings.Builder
	writeRow := func(cells []string, header bool) {
		var line strings.Builder
		for i := 0; i < cols; i++ {
			line.WriteString(padCell(t.cell(cells, i), widths[i], t.alignOf(i)))
			if i < cols-1 {
				line.WriteString("  ") // gutter
			}
		}
		s := strings.TrimRight(line.String(), " ")
		if header {
			s = Style(s, t.p.level, Color{}, Bold)
		}
		b.WriteString(s)
		b.WriteByte('\n')
	}
	if len(t.headers) > 0 {
		writeRow(t.headers, true)
	}
	for _, r := range t.rows {
		writeRow(r, false)
	}
	return b.String()
}

func (t *Table) renderBordered(cols int, widths []int) string {
	rule := func(left, mid, right string) string {
		var b strings.Builder
		b.WriteString(left)
		for i := 0; i < cols; i++ {
			b.WriteString(strings.Repeat("─", widths[i]+2)) // +2 for the 1-space cell padding
			if i < cols-1 {
				b.WriteString(mid)
			}
		}
		b.WriteString(right + "\n")
		return b.String()
	}
	cellLine := func(cells []string, header bool) string {
		var b strings.Builder
		b.WriteString("│")
		for i := 0; i < cols; i++ {
			b.WriteString(" " + padCell(t.cell(cells, i), widths[i], t.alignOf(i)) + " │")
		}
		s := b.String()
		if header {
			s = Style(s, t.p.level, Color{}, Bold)
		}
		return s + "\n"
	}

	var b strings.Builder
	b.WriteString(rule("┌", "┬", "┐"))
	if len(t.headers) > 0 {
		b.WriteString(cellLine(t.headers, true))
		b.WriteString(rule("├", "┼", "┤"))
	}
	for _, r := range t.rows {
		b.WriteString(cellLine(r, false))
	}
	b.WriteString(rule("└", "┴", "┘"))
	return b.String()
}

// padCell pads cell to width visible columns per alignment a.
func padCell(cell string, width int, a Align) string {
	gap := width - visibleWidth(cell)
	if gap <= 0 {
		return cell
	}
	switch a {
	case AlignRight:
		return strings.Repeat(" ", gap) + cell
	case AlignCenter:
		left := gap / 2
		return strings.Repeat(" ", left) + cell + strings.Repeat(" ", gap-left)
	default:
		return cell + strings.Repeat(" ", gap)
	}
}

// visibleWidth is a string's printable width: its rune count after ANSI escapes are
// stripped, so a pre-styled cell aligns by what the eye sees.
func visibleWidth(s string) int {
	return utf8.RuneCountInString(StripANSI(s))
}
