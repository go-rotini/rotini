package internal

import (
	"path/filepath"
	"strings"
	"testing"
)

// The yaml locator resolves pointers through mappings, sequences, and flow
// collections to the value node's own line:column.
func TestSourceLocator_yaml(t *testing.T) {
	src := []byte(`top: 1
list:
  - name: a
  - name: b
    nested:
      flow: [x, y]
`)
	locate := newSourceLocator(formatYAML, src)
	cases := []struct {
		pointer   string
		line, col int
	}{
		{"/top", 1, 6},
		{"/list/1/name", 4, 11},
		{"/list/1/nested/flow/1", 6, 17},
	}
	for _, c := range cases {
		line, col, ok := locate(c.pointer)
		if !ok || line != c.line || col != c.col {
			t.Errorf("locate(%s) = %d:%d ok=%v, want %d:%d", c.pointer, line, col, ok, c.line, c.col)
		}
	}
	if _, _, ok := locate("/list/9/name"); ok {
		t.Error("locate(out-of-range index) resolved, want ok=false")
	}
	if _, _, ok := locate("/nope"); ok {
		t.Error("locate(missing key) resolved, want ok=false")
	}
}

// The JSON locator token-walks the raw bytes to the target value's first byte.
func TestSourceLocator_json(t *testing.T) {
	src := []byte(`{
  "a": {"b": [10, 20]},
  "z": true
}`)
	locate := newSourceLocator(formatJSON, src)
	if line, col, ok := locate("/a/b/1"); !ok || line != 2 || col != 19 {
		t.Errorf("locate(/a/b/1) = %d:%d ok=%v, want 2:19 (the 20)", line, col, ok)
	}
	if line, col, ok := locate("/z"); !ok || line != 3 || col != 8 {
		t.Errorf("locate(/z) = %d:%d ok=%v, want 3:8", line, col, ok)
	}
}

// JSONC: comments and trailing commas are blanked IN PLACE, so positions match
// the bytes the user typed — comment shifts included.
func TestSourceLocator_jsonc(t *testing.T) {
	src := []byte(`{
  // leading comment
  "a": /* inline */ {
    "b": 7,
  },
}`)
	locate := newSourceLocator(formatJSONC, src)
	if line, col, ok := locate("/a/b"); !ok || line != 4 || col != 10 {
		t.Errorf("locate(/a/b) = %d:%d ok=%v, want 4:10 (the 7)", line, col, ok)
	}
	// A "comment" inside a string survives the blanking.
	src2 := []byte(`{"u": "http://x", "k": 1}`)
	locate = newSourceLocator(formatJSONC, src2)
	if line, col, ok := locate("/k"); !ok || line != 1 || col != 24 {
		t.Errorf("locate(/k) = %d:%d ok=%v, want 1:24 (string // untouched)", line, col, ok)
	}
}

// TOML positions are now supported via toml.PathPointer over the parsed AST:
// simple key paths resolve to their source line. (Indexing across
// [[array-of-tables]] entries is a documented toml limitation, exercised by the
// end-to-end TOML case below, which still degrades to pointer-only.)
func TestSourceLocator_toml(t *testing.T) {
	src := []byte("name = \"web\"\n\n[server]\nhost = \"h\"\n")
	locate := newSourceLocator(formatTOML, src)
	if locate == nil {
		t.Fatal("TOML locator should be non-nil (positions now supported)")
	}
	if _, _, ok := locate("/name"); !ok {
		t.Error("locate(/name) did not resolve")
	}
	if line, _, ok := locate("/server/host"); !ok || line != 4 {
		t.Errorf("locate(/server/host) line=%d ok=%v, want line 4", line, ok)
	}
}

// End-to-end: a deliberately-broken YAML spec reports file:line:col ahead of
// the pointer, for both a schema violation and the raw-instance zero-bound
// lint; a TOML spec reports the pointer only.
func TestValidate_sourcePositions(t *testing.T) {
	t.Run("schema violation, yaml", func(t *testing.T) {
		spec := validSpecHeader + "command:\n  name: app\n  flags:\n" +
			"    - name: x\n      schema: { type: string, enum: [1] }\n"
		path := writeTemp(t, "spec.yaml", spec)
		err := validateOnce(path, "", "", "")
		if err == nil {
			t.Fatal("want a schema violation")
		}
		if want := path + ":6:38: /command/flags/0/schema/enum/0"; !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v\nwant it to contain %q", err, want)
		}
	})

	t.Run("nested schema violation, yaml", func(t *testing.T) {
		// multipleOf must be strictly positive (the meta-schema's own
		// exclusiveMinimum: 0): a negative lands a pointer-shaped problem on
		// the exact value byte.
		spec := validSpecHeader + "command:\n  name: app\n  flags:\n" +
			"    - name: port\n      schema: { type: int, multipleOf: -2 }\n"
		path := writeTemp(t, "spec.yaml", spec)
		err := validateOnce(path, "", "", "")
		if err == nil {
			t.Fatal("want a schema violation")
		}
		if want := path + ":6:40: /command/flags/0/schema/multipleOf"; !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v\nwant it to contain %q", err, want)
		}
	})

	t.Run("toml array-of-tables pointer degrades to pointer-only", func(t *testing.T) {
		// TOML positions resolve for simple key paths, but a pointer indexing
		// across [[array-of-tables]] entries (flags/0) can't resolve — a
		// documented toml.PathPointer limitation — so it degrades gracefully.
		spec := "\"$schema\" = \"https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-spec.json\"\n" +
			"[command]\nname = \"app\"\n[[command.flags]]\nname = \"port\"\n" +
			"[command.flags.schema]\ntype = \"int\"\nmultipleOf = -2\n"
		path := writeTemp(t, "spec.toml", spec)
		err := validateOnce(path, "", "", "")
		if err == nil {
			t.Fatal("want a schema violation")
		}
		if strings.Contains(err.Error(), filepath.Base(path)+":") {
			t.Errorf("err = %v\nTOML should not claim a position", err)
		}
		if !strings.Contains(err.Error(), "/command/flags/0/schema/multipleOf") {
			t.Errorf("err = %v\nwant the pointer-only loc", err)
		}
	})
}
