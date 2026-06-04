package rtk

import (
	"encoding/csv"
	"fmt"
	"reflect"
)

// TableOf builds a [Table] from rows — a slice (or array) of structs, or pointers to
// structs. It derives one column per exported field, headed by the field's `table:"…"`
// tag when present (else the field name) and skipping any field tagged `table:"-"`, and
// one table row per element. It returns the populated Table so a handler can still set
// alignment, borders, or a width cap before rendering:
//
//	type user struct {
//	    Name string
//	    Age  int `table:"AGE"`
//	    pw   string // unexported → never shown
//	}
//	t, err := p.TableOf([]user{{Name: "alice", Age: 30}, {Name: "bob", Age: 100}})
//	if err != nil { /* handler owns it */ }
//	t.Align(rtk.AlignLeft, rtk.AlignRight).Render()
//
// It is the structured-data sibling of [Printer.NewTable] (which takes raw string
// cells). An argument that is not a slice/array of structs is an error; a nil-pointer
// element renders empty cells.
func (p *Printer) TableOf(rows any) (*Table, error) {
	headers, cells, err := structRows(rows, "table")
	if err != nil {
		return nil, err
	}
	t := p.NewTable(headers...)
	t.rows = cells
	return t, nil
}

// CSV writes rows as RFC 4180 CSV to the output writer. rows may be:
//
//   - a slice/array of structs (or pointers to structs) — a header row from the field
//     names (or their `csv:"…"` tags; `csv:"-"` skips a field), then one record per
//     element: the same column model as [Printer.TableOf], keyed off the `csv` tag; or
//   - a [][]string — written verbatim, with no header row inserted.
//
// It is the spreadsheet-friendly formatter sibling of [Printer.JSON]/[Printer.YAML]. A
// field value is rendered with fmt's default verb, so a comma or quote in a cell is
// quoted by the CSV encoder, not mangled.
func (p *Printer) CSV(rows any) error {
	w := csv.NewWriter(p.out)
	if raw, ok := rows.([][]string); ok {
		if err := w.WriteAll(raw); err != nil { // WriteAll flushes
			return err
		}
		return w.Error()
	}
	headers, records, err := structRows(rows, "csv")
	if err != nil {
		return err
	}
	if err := w.Write(headers); err != nil {
		return err
	}
	if err := w.WriteAll(records); err != nil { // WriteAll flushes
		return err
	}
	return w.Error()
}

// structRows reflects a slice/array of structs into a header row and string cells, one
// row per element. tag names the struct-tag whose value overrides a field's header (and
// whose "-" value skips the field); unexported fields are always skipped. fmt's default
// formatting renders each cell, so a field's Stringer (if any) is honored.
func structRows(v any, tag string) (headers []string, rows [][]string, err error) {
	rv := reflect.ValueOf(v)
	if !rv.IsValid() || (rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array) {
		return nil, nil, fmt.Errorf("rtk: expected a slice of structs, got %T", v)
	}
	elem := rv.Type().Elem()
	for elem.Kind() == reflect.Pointer {
		elem = elem.Elem()
	}
	if elem.Kind() != reflect.Struct {
		return nil, nil, fmt.Errorf("rtk: expected a slice of structs, got slice of %s", elem.Kind())
	}

	// The visible columns: exported, non-skipped fields, in declaration order.
	var indexes []int
	for i := 0; i < elem.NumField(); i++ {
		f := elem.Field(i)
		if f.PkgPath != "" { // unexported
			continue
		}
		t := f.Tag.Get(tag)
		if t == "-" {
			continue
		}
		header := t
		if header == "" {
			header = f.Name
		}
		indexes = append(indexes, i)
		headers = append(headers, header)
	}

	rows = make([][]string, rv.Len())
	for r := 0; r < rv.Len(); r++ {
		ev := rv.Index(r)
		for ev.Kind() == reflect.Pointer {
			if ev.IsNil() {
				ev = reflect.Value{}
				break
			}
			ev = ev.Elem()
		}
		row := make([]string, len(indexes))
		if ev.IsValid() {
			for j, idx := range indexes {
				row[j] = fmt.Sprint(ev.Field(idx).Interface())
			}
		}
		rows[r] = row
	}
	return headers, rows, nil
}
