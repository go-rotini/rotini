package cfgedit

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/rogpeppe/go-internal/txtar"
)

var update = flag.Bool("update", false, "rewrite the out files of the edit cases")

// editCase is one testdata/edit/<format>/<name>.txtar: the file "in", an "op" line
// (`set <path> <json value>` or `unset <path> [keep N]`, the path dotted or a JSON array of
// segments), and either "out", the exact
// expected result, or "err", text the error must contain.
type editCase struct {
	name    string
	file    string
	archive *txtar.Archive
	in      []byte
	op      string
	out     []byte
	errText string
	hasOut  bool
}

func loadEditCases(t *testing.T, format Format) []editCase {
	t.Helper()
	files, err := filepath.Glob(filepath.Join("testdata", "edit", string(format), "*.txtar"))
	if err != nil {
		t.Fatal(err)
	}
	var cases []editCase
	for _, file := range files {
		a, err := txtar.ParseFile(file)
		if err != nil {
			t.Fatal(err)
		}
		c := editCase{name: strings.TrimSuffix(filepath.Base(file), ".txtar"), file: file, archive: a}
		for _, f := range a.Files {
			switch f.Name {
			case "in":
				c.in = f.Data
			case "op":
				c.op = strings.TrimSpace(string(f.Data))
			case "out":
				c.out, c.hasOut = f.Data, true
			case "err":
				c.errText = strings.TrimSpace(string(f.Data))
			}
		}
		cases = append(cases, c)
	}
	if len(cases) == 0 {
		t.Fatalf("no edit cases for %s", format)
	}
	return cases
}

// runOp applies an op line to src.
func runOp(t *testing.T, format Format, src []byte, op string) ([]byte, error) {
	t.Helper()
	verb, rest, _ := strings.Cut(op, " ")
	var path []string
	var arg string
	if strings.HasPrefix(rest, "[") {
		dec := json.NewDecoder(strings.NewReader(rest))
		if err := dec.Decode(&path); err != nil {
			t.Fatalf("op %q: %v", op, err)
		}
		arg = strings.TrimSpace(rest[dec.InputOffset():])
	} else {
		var key string
		key, arg, _ = strings.Cut(rest, " ")
		path = strings.Split(key, ".")
	}
	switch verb {
	case "set":
		dec := json.NewDecoder(strings.NewReader(arg))
		dec.UseNumber()
		var v any
		if err := dec.Decode(&v); err != nil {
			t.Fatalf("op %q: %v", op, err)
		}
		return Set(src, format, path, fromJSON(v))
	case "unset":
		var opts []Option
		if n, ok := strings.CutPrefix(arg, "keep "); ok {
			k, err := strconv.Atoi(n)
			if err != nil {
				t.Fatalf("op %q: %v", op, err)
			}
			opts = append(opts, KeepParents(k))
		}
		return Unset(src, format, path, opts...)
	}
	t.Fatalf("unknown op %q", op)
	return nil, nil
}

// fromJSON turns JSON numbers into int64 or float64, the way a caller passes them.
func fromJSON(v any) any {
	switch x := v.(type) {
	case json.Number:
		if i, err := x.Int64(); err == nil {
			return i
		}
		f, _ := x.Float64()
		return f
	case []any:
		for i := range x {
			x[i] = fromJSON(x[i])
		}
	case map[string]any:
		for k := range x {
			x[k] = fromJSON(x[k])
		}
	}
	return v
}

// variants are the same edit under other line endings and with a byte-order mark: the edit
// must keep both, byte for byte.
var variants = []struct {
	name string
	conv func([]byte) []byte
}{
	{"lf", func(b []byte) []byte { return b }},
	{"crlf", func(b []byte) []byte { return bytes.ReplaceAll(b, []byte("\n"), []byte("\r\n")) }},
	{"bom", func(b []byte) []byte { return append([]byte(bom), b...) }},
	{"bom-crlf", func(b []byte) []byte {
		return append([]byte(bom), bytes.ReplaceAll(b, []byte("\n"), []byte("\r\n"))...)
	}},
}

func runEditCases(t *testing.T, format Format) {
	for _, c := range loadEditCases(t, format) {
		t.Run(c.name, func(t *testing.T) {
			got, err := runOp(t, format, c.in, c.op)
			if c.errText != "" {
				if err == nil || !strings.Contains(err.Error(), c.errText) {
					t.Fatalf("error = %v, want one containing %q", err, c.errText)
				}
				return
			}
			if err != nil {
				t.Fatalf("%s: %v", c.op, err)
			}
			if *update {
				writeOut(t, c, got)
				return
			}
			if !c.hasOut {
				t.Fatalf("no out file; run with -update")
			}
			if !bytes.Equal(got, c.out) {
				t.Fatalf("%s\n--- got\n%s\n--- want\n%s", c.op, got, c.out)
			}
			for _, v := range variants[1:] {
				if !bytes.Contains(c.in, []byte("\n")) && strings.Contains(v.name, "crlf") {
					continue // nothing to tell the line ending from
				}
				got, err := runOp(t, format, v.conv(c.in), c.op)
				if err != nil {
					t.Fatalf("%s: %s: %v", v.name, c.op, err)
				}
				if want := v.conv(c.out); !bytes.Equal(got, want) {
					t.Fatalf("%s: %s\n--- got\n%q\n--- want\n%q", v.name, c.op, got, want)
				}
			}
		})
	}
}

func writeOut(t *testing.T, c editCase, got []byte) {
	t.Helper()
	found := false
	for i := range c.archive.Files {
		if c.archive.Files[i].Name == "out" {
			c.archive.Files[i].Data, found = got, true
		}
	}
	if !found {
		c.archive.Files = append(c.archive.Files, txtar.File{Name: "out", Data: got})
	}
	if err := os.WriteFile(c.file, txtar.Format(c.archive), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestEditYAML(t *testing.T)   { runEditCases(t, YAML) }
func TestEditJSON(t *testing.T)   { runEditCases(t, JSON) }
func TestEditJSONC(t *testing.T)  { runEditCases(t, JSONC) }
func TestEditDotenv(t *testing.T) { runEditCases(t, Dotenv) }

func TestEditErrors(t *testing.T) {
	if _, err := Set([]byte("a: 1\n"), YAML, nil, "x"); err == nil {
		t.Error("an empty path should fail")
	}
	if _, err := Set([]byte("a: 1\n"), YAML, []string{"a", ""}, "x"); err == nil {
		t.Error("an empty segment should fail")
	}
	if _, err := Set([]byte("a: 1\n"), "ini", []string{"a"}, "x"); !errors.Is(err, ErrUnsupported) {
		t.Errorf("unknown format: %v", err)
	}
	if _, err := Set([]byte("a: 1\n"), YAML, []string{"a"}, struct{}{}); err == nil {
		t.Error("a struct value should fail")
	}
	if _, err := Set([]byte("a: 1\n"), YAML, []string{"a"}, [][]string{{"x"}}); err == nil {
		t.Error("a nested list should fail")
	}
	if _, err := Unset([]byte("a: 1\n"), YAML, nil); err == nil {
		t.Error("an empty path should fail on unset")
	}
	_, err := Set([]byte("a: &x 1\nb: *x\n"), YAML, []string{"a"}, int64(2))
	var re *RefusedError
	if !errors.As(err, &re) || !errors.Is(err, ErrRefused) || re.Path != "a" {
		t.Errorf("anchor: %v", err)
	}
	if _, err := Set([]byte("a: [\n"), YAML, []string{"a"}, int64(2)); err == nil || errors.Is(err, ErrRefused) {
		t.Errorf("a broken file is a parse error, not a refusal: %v", err)
	}
}

func TestNormalizeValue(t *testing.T) {
	for _, v := range []any{1, int8(1), uint16(1), float32(1.5), "s", true, []string{"a"}, map[string]int{"a": 1}, []int{1, 2}} {
		if _, err := normalizeValue(v); err != nil {
			t.Errorf("%T: %v", v, err)
		}
	}
	type named string
	if got, err := normalizeValue(named("x")); err != nil || got != "x" {
		t.Errorf("named string: %v %v", got, err)
	}
	for _, v := range []any{nil, struct{}{}, map[int]int{1: 1}, []any{[]any{1}}, math.NaN(), math.Inf(1), "\xff", map[string]any{"\xff": 1}} {
		if _, err := normalizeValue(v); err == nil {
			t.Errorf("%T should fail", v)
		}
	}
}
