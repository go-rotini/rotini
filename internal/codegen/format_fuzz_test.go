package codegen

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rogpeppe/go-internal/txtar"
)

// FuzzFormat formats arbitrary YAML as a spec. Formatting may refuse a document, but it must
// never panic, never produce a file whose values differ (the printer's own check reports that,
// and it is a bug), and formatting its output again must change nothing.
func FuzzFormat(f *testing.F) {
	files, _ := filepath.Glob(filepath.Join("testdata", "fmt", "*.txtar"))
	for _, file := range files {
		a, err := txtar.ParseFile(file)
		if err != nil {
			continue
		}
		for _, af := range a.Files {
			if af.Name == "in" {
				f.Add(string(af.Data))
			}
		}
	}
	f.Fuzz(func(t *testing.T, src string) {
		out, err := FormatYAML([]byte(src), FormatKindSpec, "")
		if err != nil {
			if strings.Contains(err.Error(), "please report it") || strings.TrimSpace(err.Error()) == "" {
				t.Fatalf("%q: %v", src, err)
			}
			return
		}
		again, err := FormatYAML(out, FormatKindSpec, "")
		if err != nil || !bytes.Equal(again, out) {
			t.Fatalf("%q: formatting twice changed it (%v)\n%q\n%q", src, err, out, again)
		}
	})
}
