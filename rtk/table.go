package rtk

import (
	"io"
	"strings"
	"unicode/utf8"
)

// Table is a simple column-aligned table built from a [Printer], so it renders to the
// same writer at the same color level. Columns are left-aligned and sized to their
// widest cell; the header row is bold (when the level supports it). Build it fluently
// and render once:
//
//	p.NewTable("NAME", "AGE").
//	    Row("alice", "30").
//	    Row("bob", "100").
//	    Render()
type Table struct {
	p       *Printer
	headers []string
	rows    [][]string
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

// String renders the table to a string (no trailing blank line beyond each row's
// newline). Trailing padding on every line is trimmed.
func (t *Table) String() string {
	cols := len(t.headers)
	for _, r := range t.rows {
		if len(r) > cols {
			cols = len(r)
		}
	}
	if cols == 0 {
		return ""
	}

	widths := make([]int, cols)
	measure := func(cells []string) {
		for i := 0; i < cols && i < len(cells); i++ {
			if w := visibleWidth(cells[i]); w > widths[i] {
				widths[i] = w
			}
		}
	}
	measure(t.headers)
	for _, r := range t.rows {
		measure(r)
	}

	var b strings.Builder
	writeRow := func(cells []string, header bool) {
		var line strings.Builder
		for i := 0; i < cols; i++ {
			cell := ""
			if i < len(cells) {
				cell = cells[i]
			}
			line.WriteString(cell)
			if pad := widths[i] - visibleWidth(cell); pad > 0 {
				line.WriteString(strings.Repeat(" ", pad))
			}
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

// Render writes the table to the printer's output writer.
func (t *Table) Render() {
	_, _ = io.WriteString(t.p.out, t.String())
}

// visibleWidth is a string's printable width: its rune count after ANSI escapes are
// stripped, so a pre-styled cell aligns by what the eye sees.
func visibleWidth(s string) int {
	return utf8.RuneCountInString(StripANSI(s))
}
