package cfgedit

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rogpeppe/go-internal/txtar"
)

// FuzzEdit makes a random edit to a random document in each format. The edit may fail, but
// it must never panic, and a successful edit must hold: the edited file decodes with the key set
// (or gone), and making the same edit again changes nothing. A verification failure is an
// allowed outcome: it is the safety net refusing an edit whose result would read back wrong.
func FuzzEdit(f *testing.F) {
	formats := []Format{YAML, JSON, JSONC, Dotenv}
	if _, err := newTOMLEditor(); err == nil {
		formats = append(formats, TOML)
	}
	files, _ := filepath.Glob(filepath.Join("testdata", "edit", "*", "*.txtar"))
	for _, file := range files {
		a, err := txtar.ParseFile(file)
		if err != nil {
			continue
		}
		for i, format := range formats {
			if strings.Contains(file, string(filepath.Separator)+string(format)+string(filepath.Separator)) {
				for _, af := range a.Files {
					if af.Name == "in" {
						f.Add(uint8(i), string(af.Data), "deploy.timeout", "x y", false)
						f.Add(uint8(i), string(af.Data), "new.key", "5", false)
						f.Add(uint8(i), string(af.Data), "deploy.retries", "", true)
					}
				}
			}
		}
	}
	f.Fuzz(func(t *testing.T, which uint8, src, key, value string, remove bool) {
		format := formats[int(which)%len(formats)]
		path := strings.Split(key, ".")
		if format == Dotenv {
			path = []string{key}
		}
		if loneCR([]byte(src)) {
			return
		}
		var err error
		var out []byte
		if remove {
			out, err = Unset([]byte(src), format, path, KeepParents(int(which)%3))
		} else {
			out, err = Set([]byte(src), format, path, value)
		}
		if err != nil {
			return
		}
		got, err := decode(format, out)
		if err != nil {
			t.Fatalf("%s %q on %q: the edited file doesn't decode: %v", format, key, src, err)
		}
		v, found := lookup(got, path)
		if remove == found || (!remove && !equalValue(value, v)) {
			t.Fatalf("%s %q on %q: got %#v (found %v)", format, key, src, v, found)
		}
		var again []byte
		if remove {
			again, err = Unset(out, format, path, KeepParents(int(which)%3))
		} else {
			again, err = Set(out, format, path, value)
		}
		if err != nil || !bytes.Equal(again, out) {
			t.Fatalf("%s %q on %q: the same edit again changed the file (%v)\n%q\n%q", format, key, src, err, out, again)
		}
	})
}

func lookup(m map[string]any, path []string) (any, bool) {
	var cur any = m
	for _, seg := range path {
		mm, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		if cur, ok = mm[seg]; !ok {
			return nil, false
		}
	}
	return cur, true
}
