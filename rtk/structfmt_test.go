package rtk

import (
	"bytes"
	"strings"
	"testing"
)

type sfUser struct {
	Name   string
	Age    int    `table:"AGE" csv:"age"`
	Secret string `table:"-" csv:"-"` // never shown by either formatter
	pw     string // unexported → skipped
}

func TestPrinter_TableOf(t *testing.T) {
	var out bytes.Buffer
	p := NewPrinter().WithOutput(&out) // ColorNone → no styling to strip

	tbl, err := p.TableOf([]sfUser{
		{Name: "alice", Age: 30, Secret: "x", pw: "y"},
		{Name: "bob", Age: 100},
	})
	if err != nil {
		t.Fatalf("TableOf: %v", err)
	}
	tbl.Render()

	got := out.String()
	// Header = field name verbatim (untagged "Name"); the `table:"AGE"` tag overrides Age.
	want := "Name   AGE\nalice  30\nbob    100\n"
	if got != want {
		t.Errorf("TableOf render =\n%q\nwant\n%q", got, want)
	}
	if strings.Contains(got, "Secret") || strings.Contains(got, "pw") || strings.Contains(got, "x") {
		t.Errorf("skipped/unexported fields leaked: %q", got)
	}
}

func TestPrinter_TableOf_pointersAndChaining(t *testing.T) {
	var out bytes.Buffer
	p := NewPrinter().WithOutput(&out)

	tbl, err := p.TableOf([]*sfUser{{Name: "alice", Age: 30}, nil})
	if err != nil {
		t.Fatalf("TableOf: %v", err)
	}
	// Returned Table is fully configurable before rendering (right-align AGE).
	tbl.Align(AlignLeft, AlignRight).Render()

	got := out.String()
	// A nil-pointer element renders empty cells; the header still sizes the column.
	if !strings.Contains(got, "alice   30") {
		t.Errorf("pointer-slice render missing aligned row:\n%s", got)
	}
}

func TestPrinter_TableOf_errors(t *testing.T) {
	p := NewPrinter().WithOutput(&bytes.Buffer{})
	if _, err := p.TableOf(42); err == nil {
		t.Error("TableOf(non-slice) should error")
	}
	if _, err := p.TableOf([]string{"a", "b"}); err == nil {
		t.Error("TableOf(slice of non-structs) should error")
	}
}

func TestPrinter_CSV_structs(t *testing.T) {
	var out bytes.Buffer
	p := NewPrinter().WithOutput(&out)

	err := p.CSV([]sfUser{
		{Name: "alice", Age: 30},
		{Name: "bob, jr.", Age: 100}, // the comma must be quoted by the encoder
	})
	if err != nil {
		t.Fatalf("CSV: %v", err)
	}
	got := out.String()
	want := "Name,age\nalice,30\n\"bob, jr.\",100\n"
	if got != want {
		t.Errorf("CSV =\n%q\nwant\n%q", got, want)
	}
}

func TestPrinter_CSV_rawRows(t *testing.T) {
	var out bytes.Buffer
	p := NewPrinter().WithOutput(&out)

	if err := p.CSV([][]string{{"a", "b"}, {"c", "d"}}); err != nil {
		t.Fatalf("CSV: %v", err)
	}
	if got := out.String(); got != "a,b\nc,d\n" {
		t.Errorf("CSV([][]string) = %q, want %q", got, "a,b\nc,d\n")
	}
}

func TestPrinter_CSV_error(t *testing.T) {
	p := NewPrinter().WithOutput(&bytes.Buffer{})
	if err := p.CSV("not a slice"); err == nil {
		t.Error("CSV(non-slice) should error")
	}
}
