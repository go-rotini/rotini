package internal

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// supportedExts is the set of extensions ReadSpec/WriteSpec round-trip.
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
			if err := WriteSpec(path, want); err != nil {
				t.Fatalf("WriteSpec: %v", err)
			}
			got, err := ReadSpec(path)
			if err != nil {
				t.Fatalf("ReadSpec: %v", err)
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
			Cmd: &GenerateCmdConfig{Package: "internal/handlers", GenFile: "handlers.gen.go"},
		},
	}
	for _, ext := range supportedExts {
		t.Run(ext, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "conf"+ext)
			if err := WriteConf(path, want); err != nil {
				t.Fatalf("WriteConf: %v", err)
			}
			got, err := ReadConf(path)
			if err != nil {
				t.Fatalf("ReadConf: %v", err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("round-trip mismatch:\n got=%+v\nwant=%+v", got, want)
			}
		})
	}
}

// TestReadSpec_formatsAndTags reads hand-authored documents in each format
// to confirm format detection and that the json struct tags ("$schema",
// "gen_file") drive decoding across YAML/JSON/JSONC alike.
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
			got, err := ReadSpec(path)
			if err != nil {
				t.Fatalf("ReadSpec: %v", err)
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
	path := filepath.Join(t.TempDir(), "spec.toml")
	if _, err := ReadSpec(path); !errors.Is(err, ErrUnsupportedFormat) {
		t.Errorf("ReadSpec err = %v, want ErrUnsupportedFormat", err)
	}
	if err := WriteSpec(path, &Spec{}); !errors.Is(err, ErrUnsupportedFormat) {
		t.Errorf("WriteSpec err = %v, want ErrUnsupportedFormat", err)
	}
}

func TestWatchSpec(t *testing.T) {
	path := filepath.Join(t.TempDir(), "spec.yaml")
	if err := WriteSpec(path, &Spec{Command: Command{Name: "v1"}}); err != nil {
		t.Fatalf("seed WriteSpec: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	updates := make(chan *Spec, 8)
	err := WatchSpec(ctx, path, func(s *Spec, err error) {
		if err == nil {
			updates <- s
		}
	})
	if err != nil {
		t.Fatalf("WatchSpec: %v", err)
	}

	// Give the watcher a moment to establish its baseline, then change the
	// file in place.
	time.Sleep(200 * time.Millisecond)
	if err := os.WriteFile(path, []byte("command:\n  name: v2\n"), 0o600); err != nil {
		t.Fatalf("modify: %v", err)
	}

	deadline := time.After(10 * time.Second)
	for {
		select {
		case s := <-updates:
			if s.Command.Name == "v2" {
				return
			}
		case <-deadline:
			t.Fatal("timed out waiting for watch event with updated content")
		}
	}
}
