package internal

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// supportedExts is the set of extensions readSpec/writeSpec round-trip.
var supportedExts = []string{".yaml", ".yml", ".json", ".jsonc"}

func TestSpecRoundTrip(t *testing.T) {
	want := &Spec{
		Schema: "https://example.com/spec.json",
		Command: Command{
			Name:     "demo",
			Aliases:  []string{"d"},
			Timeout:  "10s",
			Commands: []Command{{Name: "sub", Aliases: []string{"s"}}},
		},
	}
	for _, ext := range supportedExts {
		t.Run(ext, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "spec"+ext)
			if err := writeSpec(path, want); err != nil {
				t.Fatalf("writeSpec: %v", err)
			}
			got, err := readSpec(path)
			if err != nil {
				t.Fatalf("readSpec: %v", err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("round-trip mismatch:\n got=%+v\nwant=%+v", got, want)
			}
		})
	}
}

func TestConfRoundTrip(t *testing.T) {
	want := &Conf{
		Schema: "https://example.com/conf.json",
		Generate: &GenerateConfig{
			Packages: &PackagesConfig{
				Cli: &PackageConfig{Package: "internal/handlers", File: "handlers.gen.go"},
			},
		},
	}
	for _, ext := range supportedExts {
		t.Run(ext, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "conf"+ext)
			if err := writeConf(path, want); err != nil {
				t.Fatalf("writeConf: %v", err)
			}
			got, err := readConf(path)
			if err != nil {
				t.Fatalf("readConf: %v", err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("round-trip mismatch:\n got=%+v\nwant=%+v", got, want)
			}
		})
	}
}

// TestReadSpec_formatsAndTags reads hand-authored documents in each format
// to confirm format detection and that the json struct tags ("$schema",
// "command") drive decoding across YAML/JSON/JSONC alike.
func TestReadSpec_formatsAndTags(t *testing.T) {
	docs := map[string]string{
		".yaml": "$schema: https://x/spec.json\ncommand:\n  name: demo\n  commands:\n    - name: sub\n",
		".json": `{"$schema":"https://x/spec.json","command":{"name":"demo","commands":[{"name":"sub"}]}}`,
		".jsonc": "{\n  // leading comment\n  \"$schema\": \"https://x/spec.json\",\n" +
			"  \"command\": { \"name\": \"demo\", \"commands\": [{\"name\": \"sub\"}] },\n}\n",
	}
	for ext, doc := range docs {
		t.Run(ext, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "spec"+ext)
			if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
				t.Fatalf("seed file: %v", err)
			}
			got, err := readSpec(path)
			if err != nil {
				t.Fatalf("readSpec: %v", err)
			}
			if got.Schema != "https://x/spec.json" || got.Command.Name != "demo" {
				t.Errorf("tag mapping wrong: %+v", got)
			}
			if len(got.Command.Commands) != 1 || got.Command.Commands[0].Name != "sub" {
				t.Errorf("commands wrong: %+v", got)
			}
		})
	}
}

func TestUnsupportedFormat(t *testing.T) {
	path := filepath.Join(t.TempDir(), "spec.xml")
	if _, err := readSpec(path); !errors.Is(err, errUnsupportedFormat) {
		t.Errorf("readSpec err = %v, want errUnsupportedFormat", err)
	}
	if err := writeSpec(path, &Spec{}); !errors.Is(err, errUnsupportedFormat) {
		t.Errorf("writeSpec err = %v, want errUnsupportedFormat", err)
	}
}
