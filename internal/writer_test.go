package internal

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// supportedExts is the set of extensions writeSpec/readSpec round-trip.
var supportedExts = []string{".yaml", ".yml", ".json", ".jsonc"}

func TestSpecRoundTrip(t *testing.T) {
	want := &Spec{
		Version: "0.0.0",
		Schema:  "https://example.com/spec.json",
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
				Cmd: &PackageConfig{File: "internal/handlers/handlers.gen.go"},
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

// TestConfRoundTrip_toml covers the TOML encode side separately: TOML cannot
// round-trip through reflect.DeepEqual on the full struct (nil-vs-empty
// semantics), so assert the decoded fields instead.
func TestConfRoundTrip_toml(t *testing.T) {
	want := &Conf{Schema: "https://example.com/conf.json"}
	path := filepath.Join(t.TempDir(), "conf.toml")
	if err := writeConf(path, want); err != nil {
		t.Fatalf("writeConf: %v", err)
	}
	got, err := readConf(path)
	if err != nil {
		t.Fatalf("readConf: %v", err)
	}
	if got.Schema != want.Schema {
		t.Errorf("toml round-trip schema = %q, want %q", got.Schema, want.Schema)
	}
}

func TestWriteFile_unsupportedFormat(t *testing.T) {
	path := filepath.Join(t.TempDir(), "spec.xml")
	if err := writeSpec(path, &Spec{}); !errors.Is(err, errUnsupportedFormat) {
		t.Errorf("writeSpec(.xml) err = %v, want errUnsupportedFormat", err)
	}
}

func TestWriteGeneratedFile(t *testing.T) {
	// Parent directories are created as needed.
	path := filepath.Join(t.TempDir(), "a", "b", "gen.go")
	if err := writeGeneratedFile(path, []byte("package x\n")); err != nil {
		t.Fatalf("writeGeneratedFile: %v", err)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != "package x\n" {
		t.Errorf("content = %q, err = %v", got, err)
	}

	// A parent path that is a regular file is an error.
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeGeneratedFile(filepath.Join(blocker, "gen.go"), []byte("x")); err == nil {
		t.Error("writeGeneratedFile(under a file) = nil, want an error")
	}
}

// TestWriteGeneratedFile_skipsIdentical confirms regenerating identical content
// leaves the file untouched (mtime-stable), so watch loops and build caches
// keyed on mtimes stay quiet across no-op passes.
func TestWriteGeneratedFile_skipsIdentical(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gen.go")
	if err := writeGeneratedFile(path, []byte("package x\n")); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeGeneratedFile(path, []byte("package x\n")); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Error("identical content rewrote the generated file (mtime changed)")
	}
	if err := writeGeneratedFile(path, []byte("package y\n")); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); string(got) != "package y\n" {
		t.Errorf("changed content not written: %q", got)
	}
}

// writeFileBytes writes atomically and creates the parent directory: a non-Go output goes
// to a nested missing dir with the exact content, and the atomic temp file is renamed away
// (the target dir holds only the final file, never a torn or leftover temp).
func TestWriteFileBytes_atomicAndMkdir(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rtg", "help", "app.txt") // none of these dirs exist yet

	if err := writeFileBytes(path, "the help page\n"); err != nil {
		t.Fatalf("writeFileBytes: %v", err)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != "the help page\n" {
		t.Fatalf("content = %q, err = %v; want the help page", got, err)
	}
	// Atomic temp-then-rename leaves no residue: only the final file in the target dir.
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatalf("read target dir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "app.txt" {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("target dir = %v, want only [app.txt] (no leftover temp file)", names)
	}

	// Overwriting (re-generate) replaces the content atomically, still no residue.
	if err := writeFileBytes(path, "updated\n"); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if got, _ := os.ReadFile(path); string(got) != "updated\n" {
		t.Errorf("rewrite content = %q, want updated", got)
	}
	if entries, _ := os.ReadDir(filepath.Dir(path)); len(entries) != 1 {
		t.Errorf("after rewrite, target dir has %d entries, want 1", len(entries))
	}

	// A parent path that is a regular file is an error.
	if err := writeFileBytes(filepath.Join(path, "under-a-file.txt"), "x"); err == nil {
		t.Error("writeFileBytes(under a file) = nil, want an error")
	}
}

// TestWriteIfChanged confirms the mtime-stability contract: identical content
// skips the write entirely, different content lands on disk.
func TestWriteIfChanged(t *testing.T) {
	path := filepath.Join(t.TempDir(), "page.txt")
	if err := writeIfChanged(path, "v1"); err != nil {
		t.Fatalf("initial write: %v", err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	// Identical content: the file is untouched (same mtime).
	if err := writeIfChanged(path, "v1"); err != nil {
		t.Fatalf("identical write: %v", err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Error("identical content rewrote the file (mtime changed)")
	}

	// Different content: the file is replaced.
	if err := writeIfChanged(path, "v2"); err != nil {
		t.Fatalf("changed write: %v", err)
	}
	if got, _ := os.ReadFile(path); string(got) != "v2" {
		t.Errorf("content = %q, want v2", got)
	}
}
