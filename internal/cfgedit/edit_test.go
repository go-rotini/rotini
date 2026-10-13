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

// TestSetUnsetKeptSet sets a profile's key in an empty file, unsets it keeping the profile, and
// sets it again.
func TestSetUnsetKeptSet(t *testing.T) {
	for _, format := range []Format{YAML, JSON, JSONC, TOML} {
		t.Run(string(format), func(t *testing.T) {
			path := []string{"profiles", "prod", "deploy", "replicas"}
			out, err := Set(nil, format, path, int64(3))
			if err != nil {
				t.Fatalf("set: %v", err)
			}
			if out, err = Unset(out, format, path, KeepParents(2)); err != nil {
				t.Fatalf("unset: %v\n%s", err, out)
			}
			if out, err = Set(out, format, path, int64(4)); err != nil {
				t.Fatalf("set after unset: %v\n%s", err, out)
			}
			if !bytes.Contains(out, []byte("4")) {
				t.Errorf("set after unset:\n%s", out)
			}
		})
	}
}

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

// Removing a key that is absent under an empty mapping leaves the file as it is, in every format
// that nests.
func TestUnsetAbsentUnderEmptyParent(t *testing.T) {
	for format, src := range map[Format]string{
		YAML:  "deploy: {}\n",
		JSON:  `{"deploy": {}}`,
		JSONC: "{\n  \"deploy\": {} // none yet\n}\n",
		TOML:  "[deploy]\n# timeout = 5\n",
	} {
		got, err := Unset([]byte(src), format, []string{"deploy", "timeout"})
		if err != nil || string(got) != src {
			t.Errorf("%s: Unset = %q, %v; want the file unchanged", format, got, err)
		}
	}
}

// Removing a key from a blank strict-JSON file (missing, empty or a byte-order mark alone) leaves
// it as it is.
func TestUnsetBlankJSON(t *testing.T) {
	for _, src := range []string{"", "  \n", bom} {
		got, err := Unset([]byte(src), JSON, []string{"deploy", "timeout"})
		if err != nil || string(got) != src {
			t.Errorf("Unset(%q) = %q, %v; want the file unchanged", src, got, err)
		}
	}
}

// An integer a format, or the reader rotini uses for it, can't hold exactly is refused before
// the edit as a value error, not a verification failure. One the reader holds exactly is
// written and reads back as the same number.
func TestSetIntegerBeyondFormat(t *testing.T) {
	for _, tc := range []struct {
		format Format
		value  any
		want   string // the refusal when the reader can't hold the value
	}{
		{JSON, int64(9007199254740993), "JSON can't hold 9007199254740993 exactly"},
		{JSONC, int64(math.MinInt64), "JSON can't hold"},
		{JSON, []any{uint64(1 << 60)}, "JSON can't hold"},
		{JSON, uint64(math.MaxUint64), "JSON can't hold"},
		{YAML, uint64(math.MaxUint64), "the YAML reader can't hold 18446744073709551615 exactly"},
		{YAML, uint64(math.MaxInt64 + 1), "the YAML reader can't hold"},
		{TOML, map[string]any{"a": uint64(math.MaxUint64)}, "TOML integers are 64-bit signed"},
	} {
		out, err := Set(nil, tc.format, []string{"big"}, tc.value)
		if tc.format == TOML || !readsIntegersExactly(tc.format) {
			if err == nil || !strings.Contains(err.Error(), tc.want) || !errors.Is(err, ErrValue) || errors.Is(err, ErrVerify) {
				t.Errorf("%s %v: err = %v, want a value error %q", tc.format, tc.value, err, tc.want)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s %v: %v", tc.format, tc.value, err)
			continue
		}
		got, err := decode(tc.format, out)
		if err != nil || !equalValue(got["big"], tc.value) {
			t.Errorf("%s %v: wrote %q, which reads back as %v (%v)", tc.format, tc.value, out, got["big"], err)
		}
	}
	for format, v := range map[Format]any{JSON: int64(1 << 53), YAML: uint64(math.MaxInt64), TOML: int64(math.MinInt64)} {
		if _, err := Set(nil, format, []string{"big"}, v); err != nil {
			t.Errorf("%s %v: %v", format, v, err)
		}
	}
}

// A key holding U+2028 or U+2029, or a value holding them or U+0085, is written escaped, so
// the next edit of the file finds no line break it refuses.
func TestSetYAMLUnicodeLineBreaks(t *testing.T) {
	out, err := Set([]byte("z: 1\n"), YAML, []string{"a b"}, "x y\u0085z")
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(string(out), "  \u0085") {
		t.Errorf("wrote %q, which holds a raw Unicode line break", out)
	}
	if _, err := Set(out, YAML, []string{"z"}, "2"); err != nil {
		t.Errorf("editing %q again: %v", out, err)
	}
}

// A TOML key holding U+0000 is written when the TOML reader reads it back, and refused as a
// value error otherwise; it never fails verification.
func TestSetTOMLKeyWithNUL(t *testing.T) {
	out, err := Set([]byte("[deploy]\ntimeout = \"\"\n"), TOML, []string{"deploy\x00timeout"}, "0")
	if !tomlReadsNUL() {
		if !errors.Is(err, ErrValue) {
			t.Errorf("err = %v, want a value error", err)
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	got, err := decode(TOML, out)
	if v, ok := lookup(got, []string{"deploy\x00timeout"}); err != nil || !ok || v != "0" {
		t.Errorf("wrote %q, which reads back as %v (%v)", out, got, err)
	}
}
