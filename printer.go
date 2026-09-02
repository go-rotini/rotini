package rotini

import (
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"

	"github.com/go-rotini/toml"
	"github.com/go-rotini/yaml"
)

// Format names a Printer's output serialization — the value an --output flag
// carries.
type Format string

const (
	FormatText  Format = "text"  // human-readable lines; the default
	FormatJSON  Format = "json"  // indented JSON
	FormatYAML  Format = "yaml"  //
	FormatTOML  Format = "toml"  //
	FormatTable Format = "table" // aligned columns, for a slice of records
)

// Formats returns every format a [Printer] accepts, in a stable order — the
// candidate list for an --output flag's enum, its help text, or a completion
// handler.
func Formats() []Format {
	return []Format{FormatText, FormatJSON, FormatYAML, FormatTOML, FormatTable}
}

// ParseFormat resolves a format name case-insensitively, so an --output flag's
// raw value becomes a [Format] without the command re-listing the vocabulary. An
// unknown name is a [UsageError] naming what was accepted.
func ParseFormat(name string) (Format, error) {
	want := strings.ToLower(strings.TrimSpace(name))
	for _, f := range Formats() {
		if string(f) == want {
			return f, nil
		}
	}
	names := make([]string, 0, len(Formats()))
	for _, f := range Formats() {
		names = append(names, string(f))
	}
	return "", UsageError(fmt.Errorf("unknown output format %q — want one of %s", name, strings.Join(names, ", ")))
}

// Printer is the "data out" complement to [Collect]'s "data in": one writer that
// renders a value in whichever [Format] the invocation asked for, so a handler
// computes its result once and stays format-agnostic.
//
// It pairs with the spec's command `output:` key, which generates the typed
// <Prefix>Output struct — the spec declares the SHAPE, the Printer renders it,
// and neither wires a flag: the command declares its own --output and hands the
// parsed value to [Printer.WithFormat] (Pillar 1).
//
//	out := rotini.NewPrinter(rtx.Stdout).WithFormat(format)
//	if err := out.Print(result); err != nil { rtx.RecordError(err) }
//
// The zero value is not usable; start from [NewPrinter].
type Printer struct {
	w      io.Writer
	format Format
	indent string
	styler *Styler
	width  int
}

// NewPrinter returns a Printer writing to w in [FormatText]. Pass a handler's
// [Context.Stdout] so the program's stream configuration (and its tests) apply.
func NewPrinter(w io.Writer) *Printer {
	return &Printer{w: w, format: FormatText, indent: "  "}
}

// WithFormat selects the output serialization. An empty format is ignored, so a
// flag left unset keeps the default. It returns the receiver to chain.
func (p *Printer) WithFormat(f Format) *Printer {
	if f != "" {
		p.format = f
	}
	return p
}

// WithIndent sets the indent unit for JSON (default two spaces); an empty string
// emits compact JSON. It returns the receiver to chain.
func (p *Printer) WithIndent(indent string) *Printer {
	p.indent = indent
	return p
}

// WithStyler passes a [Styler] to the table backend for its header line; other
// formats ignore it (styling structured output would corrupt it). It returns the
// receiver to chain.
func (p *Printer) WithStyler(styler *Styler) *Printer {
	p.styler = styler
	return p
}

// WithWidth bounds [FormatTable] output to n display cells (see
// [Table.WithWidth]). Zero renders at natural width. It returns the receiver to
// chain.
func (p *Printer) WithWidth(n int) *Printer {
	p.width = n
	return p
}

// Print renders v in the configured format and writes it, newline-terminated.
// A nil value prints nothing at all, so an empty result stays quiet.
func (p *Printer) Print(v any) error {
	if v == nil {
		return nil
	}
	switch p.format {
	case FormatJSON:
		return p.printJSON(v)
	case FormatYAML:
		return p.printMarshaled(v, yaml.Marshal, "yaml")
	case FormatTOML:
		return p.printMarshaled(v, toml.Marshal, "toml")
	case FormatTable:
		return p.printTable(v)
	case FormatText:
		return p.printText(v)
	default:
		return InternalError(fmt.Errorf("printer: unsupported format %q", p.format))
	}
}

func (p *Printer) printJSON(v any) error {
	enc := json.NewEncoder(p.w)
	enc.SetIndent("", p.indent)
	if err := enc.Encode(v); err != nil {
		return InternalError(fmt.Errorf("printer: encode json: %w", err))
	}
	return nil
}

func (p *Printer) printMarshaled(v any, marshal func(any) ([]byte, error), name string) error {
	b, err := marshal(v)
	if err != nil {
		return InternalError(fmt.Errorf("printer: encode %s: %w", name, err))
	}
	out := strings.TrimRight(string(b), "\n")
	if out == "" {
		return nil
	}
	if _, err := io.WriteString(p.w, out+"\n"); err != nil {
		return fmt.Errorf("rotini: write output: %w", err)
	}
	return nil
}

// printTable renders a slice of records as aligned columns. A value that is not a
// record collection has no table shape, so it degrades to text rather than
// failing — an --output table on a scalar result should still print the scalar.
func (p *Printer) printTable(v any) error {
	table, ok := p.tableOf(v)
	if !ok {
		return p.printText(v)
	}
	_, err := table.Fprint(p.w)
	return err
}

// tableOf builds a Table from a slice of structs or maps, taking the column
// order from the first element: struct fields in declaration order, map keys
// sorted (a map has no inherent order, and unstable columns would make output
// undiffable).
func (p *Printer) tableOf(v any) (*Table, bool) {
	rv := reflect.Indirect(reflect.ValueOf(v))
	if rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array {
		return nil, false
	}
	if rv.Len() == 0 {
		return NewTable(), true // an empty result set prints nothing (Table.Fprint)
	}
	first := reflect.Indirect(rv.Index(0))
	var columns []string
	switch first.Kind() {
	case reflect.Struct:
		for i := range first.NumField() {
			if f := first.Type().Field(i); f.IsExported() {
				columns = append(columns, fieldColumnName(f))
			}
		}
	case reflect.Map:
		if first.Type().Key().Kind() != reflect.String {
			return nil, false
		}
		for _, k := range first.MapKeys() {
			columns = append(columns, k.String())
		}
		sort.Strings(columns)
	default:
		return nil, false
	}
	if len(columns) == 0 {
		return nil, false
	}

	table := NewTable(columns...).WithWidth(p.width)
	if p.styler != nil {
		table.WithStyler(p.styler)
	}
	for i := range rv.Len() {
		elem := reflect.Indirect(rv.Index(i))
		cells := make([]string, 0, len(columns))
		for ci, name := range columns {
			cells = append(cells, cellText(elem, ci, name))
		}
		table.Row(cells...)
	}
	return table, true
}

// fieldColumnName is a struct field's column heading: its json/rotini tag name
// when it carries one (the name the user already sees in --output json), else the
// Go field name upper-cased the way CLI tables conventionally render headings.
func fieldColumnName(f reflect.StructField) string {
	for _, tag := range []string{"json", "rotini"} {
		if v, ok := f.Tag.Lookup(tag); ok {
			if name, _, _ := strings.Cut(v, ","); name != "" && name != "-" {
				return strings.ToUpper(name)
			}
		}
	}
	return strings.ToUpper(f.Name)
}

// cellText renders one cell: the ci'th exported field of a struct, or the map
// entry named name.
func cellText(elem reflect.Value, ci int, name string) string {
	switch elem.Kind() {
	case reflect.Map:
		v := elem.MapIndex(reflect.ValueOf(name))
		if !v.IsValid() {
			return ""
		}
		return scalarText(v)
	case reflect.Struct:
		seen := 0
		for i := range elem.NumField() {
			if !elem.Type().Field(i).IsExported() {
				continue
			}
			if seen == ci {
				return scalarText(elem.Field(i))
			}
			seen++
		}
	}
	return ""
}

// scalarText renders a single value for a table cell, unwrapping interfaces and
// pointers and rendering a nil as empty rather than "<nil>".
func scalarText(v reflect.Value) string {
	for v.Kind() == reflect.Interface || v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return ""
		}
		v = v.Elem()
	}
	if !v.IsValid() {
		return ""
	}
	return fmt.Sprint(v.Interface())
}

// printText renders v for human reading: a string as itself, an error or
// [fmt.Stringer] through its own method, a slice one element per line, a struct
// or map as "key: value" lines, and anything else through fmt.Sprint.
func (p *Printer) printText(v any) error {
	out := textOf(v, 0)
	if out == "" {
		return nil
	}
	if _, err := io.WriteString(p.w, out+"\n"); err != nil {
		return fmt.Errorf("rotini: write output: %w", err)
	}
	return nil
}

func textOf(v any, depth int) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case error:
		return t.Error()
	case fmt.Stringer:
		return t.String()
	}

	rv := reflect.Indirect(reflect.ValueOf(v))
	if !rv.IsValid() {
		return ""
	}
	switch rv.Kind() {
	case reflect.Slice, reflect.Array:
		lines := make([]string, 0, rv.Len())
		for i := range rv.Len() {
			lines = append(lines, textOf(rv.Index(i).Interface(), depth))
		}
		return strings.Join(lines, "\n")
	case reflect.Struct:
		return keyedText(structPairs(rv), depth)
	case reflect.Map:
		if rv.Type().Key().Kind() != reflect.String {
			return fmt.Sprint(v)
		}
		return keyedText(mapPairs(rv), depth)
	default:
		return fmt.Sprint(rv.Interface())
	}
}

// pair is one "key: value" line of text output.
type pair struct{ key, value string }

func structPairs(rv reflect.Value) []pair {
	var out []pair
	for i := range rv.NumField() {
		f := rv.Type().Field(i)
		if !f.IsExported() {
			continue
		}
		out = append(out, pair{fieldColumnName(f), scalarText(rv.Field(i))})
	}
	return out
}

func mapPairs(rv reflect.Value) []pair {
	keys := make([]string, 0, rv.Len())
	for _, k := range rv.MapKeys() {
		keys = append(keys, k.String())
	}
	sort.Strings(keys) // a map has no order; unstable output would be undiffable
	out := make([]pair, 0, len(keys))
	for _, k := range keys {
		out = append(out, pair{k, scalarText(rv.MapIndex(reflect.ValueOf(k)))})
	}
	return out
}

// keyedText renders pairs as aligned "key: value" lines, indented by depth.
func keyedText(pairs []pair, depth int) string {
	if len(pairs) == 0 {
		return ""
	}
	prefix := strings.Repeat("  ", depth)
	width := 0
	for _, p := range pairs {
		if w := Width(p.key); w > width {
			width = w
		}
	}
	lines := make([]string, 0, len(pairs))
	for _, p := range pairs {
		lines = append(lines, prefix+pad(p.key+":", width+1, AlignLeft)+" "+p.value)
	}
	return strings.Join(lines, "\n")
}
