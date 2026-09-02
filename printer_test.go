package rotini

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

type widget struct {
	Name    string `json:"name"`
	Size    int    `json:"size"`
	hidden  string // unexported: must never reach any output
	Enabled bool   `json:"enabled"`
}

func printed(t *testing.T, f Format, v any) string {
	t.Helper()
	var buf bytes.Buffer
	if err := NewPrinter(&buf).WithFormat(f).Print(v); err != nil {
		t.Fatalf("Print(%s): %v", f, err)
	}
	return buf.String()
}

func TestParseFormat(t *testing.T) {
	for _, name := range []string{"json", "JSON", " yaml ", "Table", "text", "toml"} {
		if _, err := ParseFormat(name); err != nil {
			t.Errorf("ParseFormat(%q) = %v, want a format", name, err)
		}
	}
	err := ParseFormatErr(t, "xml")
	if !errors.Is(err, ErrUsage) {
		t.Errorf("an unknown format must be a usage error, got %v", err)
	}
	// The message names the accepted vocabulary rather than making the user guess.
	for _, f := range Formats() {
		if !strings.Contains(err.Error(), string(f)) {
			t.Errorf("error %q does not list %q", err, f)
		}
	}
}

func ParseFormatErr(t *testing.T, name string) error {
	t.Helper()
	_, err := ParseFormat(name)
	if err == nil {
		t.Fatalf("ParseFormat(%q) = nil error, want one", name)
	}
	return err
}

// Every structured format round-trips the same value; text and table are the
// human-facing views of it.
func TestPrinter_formats(t *testing.T) {
	w := widget{Name: "alpha", Size: 3, hidden: "SECRET", Enabled: true}
	for _, tc := range []struct {
		format Format
		want   []string
	}{
		{FormatJSON, []string{`"name": "alpha"`, `"size": 3`}},
		{FormatYAML, []string{"name: alpha", "size: 3"}},
		{FormatTOML, []string{`name = "alpha"`, "size = 3"}},
		{FormatText, []string{"NAME:", "alpha", "SIZE:", "3"}},
	} {
		got := printed(t, tc.format, w)
		for _, want := range tc.want {
			if !strings.Contains(got, want) {
				t.Errorf("%s output missing %q:\n%s", tc.format, want, got)
			}
		}
		if strings.Contains(got, "hidden") || strings.Contains(got, "SECRET") {
			t.Errorf("%s output leaked an unexported field:\n%s", tc.format, got)
		}
	}
}

// The default format is text, so a command that never wires --output still prints
// something readable.
func TestPrinter_defaultsToText(t *testing.T) {
	var buf bytes.Buffer
	if err := NewPrinter(&buf).Print("hello"); err != nil {
		t.Fatal(err)
	}
	if buf.String() != "hello\n" {
		t.Errorf("default Print = %q, want %q", buf.String(), "hello\n")
	}
}

// An unset flag must not clobber a configured format.
func TestPrinter_emptyFormatIsIgnored(t *testing.T) {
	var buf bytes.Buffer
	if err := NewPrinter(&buf).WithFormat(FormatJSON).WithFormat("").Print(map[string]int{"a": 1}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), `"a": 1`) {
		t.Errorf("empty WithFormat overrode the configured one: %q", buf.String())
	}
}

func TestPrinter_tableFromSliceOfStructs(t *testing.T) {
	got := printed(t, FormatTable, []widget{
		{Name: "alpha", Size: 3, hidden: "SECRET", Enabled: true},
		{Name: "b", Size: 1000, Enabled: false},
	})
	for _, want := range []string{"NAME", "SIZE", "ENABLED", "alpha", "1000"} {
		if !strings.Contains(got, want) {
			t.Errorf("table missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "hidden") || strings.Contains(got, "SECRET") {
		t.Errorf("table leaked an unexported field:\n%s", got)
	}
}

// Map columns are sorted: a map has no inherent order, and unstable columns would
// make the output undiffable between runs.
func TestPrinter_tableFromSliceOfMapsHasStableColumns(t *testing.T) {
	rows := []map[string]any{{"zeta": 1, "alpha": 2, "mid": 3}}
	first := printed(t, FormatTable, rows)
	for range 5 {
		if got := printed(t, FormatTable, rows); got != first {
			t.Fatalf("table column order is unstable:\n%s\nvs\n%s", first, got)
		}
	}
	head := strings.SplitN(first, "\n", 2)[0]
	if !strings.HasPrefix(head, "alpha") || !strings.Contains(head, "zeta") {
		t.Errorf("map columns are not sorted: %q", head)
	}
}

// A value with no table shape degrades to text rather than failing: `--output
// table` on a scalar should still print the scalar.
func TestPrinter_tableDegradesToTextForScalars(t *testing.T) {
	if got := printed(t, FormatTable, "just a string"); got != "just a string\n" {
		t.Errorf("table of a scalar = %q, want the text rendering", got)
	}
}

// An empty result set prints NOTHING, in every format that can say so.
func TestPrinter_emptyResults(t *testing.T) {
	if got := printed(t, FormatTable, []widget{}); got != "" {
		t.Errorf("table of an empty slice = %q, want empty", got)
	}
	if got := printed(t, FormatText, nil); got != "" {
		t.Errorf("text of nil = %q, want empty", got)
	}
}

// An error or Stringer renders through its own method rather than as a struct
// dump.
func TestPrinter_textUsesStringerAndError(t *testing.T) {
	if got := printed(t, FormatText, errors.New("boom")); got != "boom\n" {
		t.Errorf("text of an error = %q", got)
	}
	if got := printed(t, FormatText, FormatJSON); got != "json\n" {
		t.Errorf("text of a named string type = %q", got)
	}
}

// A slice renders one element per line in text mode.
func TestPrinter_textSliceIsOnePerLine(t *testing.T) {
	if got := printed(t, FormatText, []string{"a", "b", "c"}); got != "a\nb\nc\n" {
		t.Errorf("text of a slice = %q", got)
	}
}

// Structured output is never styled — escapes would corrupt it for a consumer
// piping into jq.
func TestPrinter_stylerNeverTouchesStructuredOutput(t *testing.T) {
	styler := NewStyler()
	styler.Define("header").Bold()
	for _, f := range []Format{FormatJSON, FormatYAML, FormatTOML} {
		var buf bytes.Buffer
		if err := NewPrinter(&buf).WithFormat(f).WithStyler(styler).Print(widget{Name: "x"}); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(buf.String(), "\x1b") {
			t.Errorf("%s output carries escapes: %q", f, buf.String())
		}
	}
}

func TestPrinter_widthBudgetReachesTheTable(t *testing.T) {
	var buf bytes.Buffer
	err := NewPrinter(&buf).WithFormat(FormatTable).WithWidth(12).
		Print([]widget{{Name: "a-very-long-name", Size: 1}})
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimRight(buf.String(), "\n"), "\n") {
		if Width(line) > 12 {
			t.Errorf("line %q exceeds the 12-cell budget", line)
		}
	}
}

func TestPrinter_unsupportedFormatIsInternal(t *testing.T) {
	var buf bytes.Buffer
	err := NewPrinter(&buf).WithFormat(Format("xml")).Print("x")
	if !errors.Is(err, ErrInternal) {
		t.Errorf("an unreachable format must be an internal error, got %v", err)
	}
}

// omitempty is honored consistently by the per-record formats: a zero field tagged
// omitempty is absent from JSON and from text alike, so one value does not render
// three different ways depending on the flag the user passed.
func TestPrinter_omitemptyIsConsistentAcrossRecordFormats(t *testing.T) {
	type row struct {
		Name  string `json:"name"`
		Owner string `json:"owner,omitempty"`
		Count int    `json:"count,omitempty"`
	}
	v := row{Name: "x"} // Owner and Count are zero

	for _, f := range []Format{FormatJSON, FormatText} {
		got := printed(t, f, v)
		if strings.Contains(strings.ToLower(got), "owner") {
			t.Errorf("%s rendered an omitempty zero field:\n%s", f, got)
		}
		if !strings.Contains(strings.ToLower(got), "name") {
			t.Errorf("%s dropped a populated field:\n%s", f, got)
		}
	}
	// A field WITHOUT omitempty still renders when zero — the tag is the contract.
	type plain struct {
		Name  string `json:"name"`
		Owner string `json:"owner"`
	}
	if got := printed(t, FormatText, plain{Name: "x"}); !strings.Contains(got, "OWNER") {
		t.Errorf("text dropped a zero field that is NOT omitempty:\n%s", got)
	}
}

// A table keeps every column regardless: columns are a property of the table, not of
// a row, so two runs of the same command produce the same shape.
func TestPrinter_tableKeepsColumnsRegardlessOfOmitempty(t *testing.T) {
	type row struct {
		Name  string `json:"name"`
		Owner string `json:"owner,omitempty"`
	}
	got := printed(t, FormatTable, []row{{Name: "a"}, {Name: "b"}})
	if !strings.Contains(got, "OWNER") {
		t.Errorf("table dropped a column because this result set left it blank:\n%s", got)
	}
}
