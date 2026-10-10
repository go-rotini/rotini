package codegen

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-rotini/rotini/internal/cfgedit"
	"github.com/rogpeppe/go-internal/txtar"
)

// TestFormatYAMLGolden formats each testdata/fmt/<case>.txtar "in" (with an optional "kind")
// and compares the result with "out". The same input with CRLF line endings, and with a
// byte-order mark, must format to the same output with those kept.
func TestFormatYAMLGolden(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("testdata", "fmt", "*.txtar"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no fmt cases: %v", err)
	}
	for _, file := range files {
		t.Run(strings.TrimSuffix(filepath.Base(file), ".txtar"), func(t *testing.T) {
			a, err := txtar.ParseFile(file)
			if err != nil {
				t.Fatal(err)
			}
			var in, want []byte
			var kind string
			for _, f := range a.Files {
				switch f.Name {
				case "in":
					in = f.Data
				case "out":
					want = f.Data
				case "kind":
					kind = strings.TrimSpace(string(f.Data))
				}
			}
			got, err := FormatYAML(in, kind, "spec.yaml")
			if err != nil {
				t.Fatal(err)
			}
			if *updateGolden {
				writeFmtOut(t, file, a, got)
				return
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("--- got\n%s\n--- want\n%s", got, want)
			}
			again, err := FormatYAML(got, kind, "spec.yaml")
			if err != nil || !bytes.Equal(again, got) {
				t.Fatalf("formatting twice changed the output (%v):\n%s", err, again)
			}
			crlf := func(b []byte) []byte { return bytes.ReplaceAll(b, []byte("\n"), []byte("\r\n")) }
			bom := func(b []byte) []byte { return append([]byte("\xef\xbb\xbf"), b...) }
			for name, conv := range map[string]func([]byte) []byte{"crlf": crlf, "bom": bom} {
				got, err := FormatYAML(conv(in), kind, "spec.yaml")
				if err != nil {
					t.Fatalf("%s: %v", name, err)
				}
				if !bytes.Equal(got, conv(want)) {
					t.Fatalf("%s:\n--- got\n%q\n--- want\n%q", name, got, conv(want))
				}
			}
		})
	}
}

func writeFmtOut(t *testing.T, file string, a *txtar.Archive, got []byte) {
	t.Helper()
	found := false
	for i := range a.Files {
		if a.Files[i].Name == "out" {
			a.Files[i].Data, found = got, true
		}
	}
	if !found {
		a.Files = append(a.Files, txtar.File{Name: "out", Data: got})
	}
	if err := os.WriteFile(file, txtar.Format(a), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestFormatYAMLCorpus formats every YAML spec and conf in the repository: the companion's,
// the docs' examples and the test fixtures. Each must format, keep its values, and come out
// the same when formatted again.
func TestFormatYAMLCorpus(t *testing.T) {
	var paths []string
	for _, root := range []string{filepath.Join("..", "..", "cmd"), filepath.Join("..", "..", "docs"), "testdata"} {
		_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err == nil && !d.IsDir() && (strings.HasSuffix(path, ".yaml") || strings.HasSuffix(path, ".yml")) &&
				!strings.Contains(path, string(filepath.Separator)+"fmt"+string(filepath.Separator)) {
				paths = append(paths, path)
			}
			return nil
		})
	}
	if len(paths) < 20 {
		t.Fatalf("found only %d YAML files", len(paths))
	}
	formatted := 0
	for _, path := range paths {
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		out, err := FormatYAML(src, "", path)
		if err != nil {
			if strings.Contains(err.Error(), "can't tell whether") {
				continue // not a rotini document
			}
			t.Errorf("%s: %v", path, err)
			continue
		}
		formatted++
		again, err := FormatYAML(out, "", path)
		if err != nil || !bytes.Equal(again, out) {
			t.Errorf("%s: formatting twice changed it (%v)", path, err)
		}
	}
	if formatted < 20 {
		t.Fatalf("formatted only %d files", formatted)
	}
}

func TestFormatYAMLSeeds(t *testing.T) {
	for _, seed := range []string{templateSpec, templateConf} {
		src := strings.NewReplacer("{{.Version}}", "1.0.0", "{{.Package}}", "demo").Replace(seed)
		out, err := FormatYAML([]byte(src), "", "")
		if err != nil {
			t.Fatal(err)
		}
		if string(out) != src {
			t.Errorf("a seed isn't in canonical form; `rotini init` should write what `rotini fmt` keeps:\n%s", out)
		}
	}
}

// TestFormatYAMLCanonicalFiles keeps the companion's own spec and conf, and the docs' every-key
// examples, in the form `rotini fmt` writes.
func TestFormatYAMLCanonicalFiles(t *testing.T) {
	for _, path := range []string{
		filepath.Join("..", "..", "cmd", "rotini", ".rotini.spec.yaml"),
		filepath.Join("..", "..", "cmd", "rotini", ".rotini.conf.yaml"),
		filepath.Join("..", "..", "docs", "assets", "examples", "rotini.spec.yaml"),
		filepath.Join("..", "..", "docs", "assets", "examples", "rotini.conf.yaml"),
	} {
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		out, err := FormatYAML(src, "", path)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if !bytes.Equal(out, src) {
			t.Errorf("%s isn't formatted; run `rotini fmt` on it", path)
		}
	}
}

func TestFormatYAMLErrors(t *testing.T) {
	for name, tc := range map[string]struct{ src, kind, want string }{
		"multi-document": {"a: 1\n---\nb: 2\n", "", "more than one YAML document"},
		"sequence root":  {"- a\n", "", "isn't a block mapping"},
		"flow root":      {"{command: {name: x}}\n", "", "isn't a block mapping"},
		"unknown kind":   {"name: x\n", "bogus", "unknown document kind"},
		"ambiguous":      {"version: 1.0.0\n", "", "pass --kind"},
		"broken":         {"command: [\n", "", "parse yaml"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := FormatYAML([]byte(tc.src), tc.kind, "file.yaml")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want one containing %q", err, tc.want)
			}
		})
	}
	if out, err := FormatYAML([]byte("# only a comment\n"), "", ""); err != nil || string(out) != "# only a comment\n" {
		t.Errorf("comments only: %q %v", out, err)
	}
	if kind := detectFormatKind(mustRoot(t, "version: 1.0.0\n"), "app/.rotini.conf.yaml"); kind != FormatKindConf {
		t.Errorf("conf by name: %q", kind)
	}
	if kind := detectFormatKind(mustRoot(t, "version: 1.0.0\n"), "app/.rotini.spec.yml"); kind != FormatKindSpec {
		t.Errorf("spec by name: %q", kind)
	}
}

func TestFormatFiles(t *testing.T) {
	dir := t.TempDir()
	messy := filepath.Join(dir, ".rotini.spec.yaml")
	tidy := filepath.Join(dir, "tidy.yaml")
	src := "command:\n    summary: s\n    name: tool\nversion: 1.0.0\n"
	if err := os.WriteFile(messy, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tidy, []byte("version: 1.0.0\ncommand:\n  name: tool\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	changed, err := FormatFiles([]string{messy, tidy}, "", true)
	if err != nil || len(changed) != 1 || changed[0] != messy {
		t.Fatalf("check: %v %v", changed, err)
	}
	if got, _ := os.ReadFile(messy); string(got) != src {
		t.Fatal("check wrote the file")
	}

	changed, err = FormatFiles([]string{messy, tidy}, "", false)
	if err != nil || len(changed) != 1 {
		t.Fatalf("format: %v %v", changed, err)
	}
	got, _ := os.ReadFile(messy)
	if want := "version: 1.0.0\ncommand:\n  name: tool\n  summary: s\n"; string(got) != want {
		t.Fatalf("formatted:\n%s", got)
	}
	if info, _ := os.Stat(messy); info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want the file's own 0600 kept", info.Mode().Perm())
	}
	if changed, err := FormatFiles([]string{messy}, "", true); err != nil || len(changed) != 0 {
		t.Fatalf("after formatting: %v %v", changed, err)
	}

	bad := filepath.Join(dir, "x.json")
	_ = os.WriteFile(bad, []byte("{}"), 0o644)
	odd := filepath.Join(dir, "x.ini")
	_ = os.WriteFile(odd, []byte(""), 0o644)
	_, err = FormatFiles([]string{bad, odd, filepath.Join(dir, "missing.yaml"), tidy}, "", false)
	for _, want := range []string{"formatting JSON files isn't supported yet", `extension ".ini"`, "missing.yaml: read:"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("error %v lacks %q", err, want)
		}
	}
	var joined interface{ Unwrap() []error }
	if !errors.As(err, &joined) || len(joined.Unwrap()) != 3 {
		t.Errorf("want 3 joined errors, got %v", err)
	}
}

func mustRoot(t *testing.T, src string) *cfgedit.YAMLNode {
	t.Helper()
	doc, err := cfgedit.IndexYAML([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	return doc.Root
}
