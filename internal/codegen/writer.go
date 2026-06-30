package codegen

// This file owns writing generated artifacts (Go files, doc pages, completion
// scripts, seeds) through go-rotini/fs — atomic (temp file then rename, so an
// interrupted pass never leaves a torn file) and parent-creating. Reading inputs
// lives in reader.go.

import (
	"fmt"

	"github.com/go-rotini/fs"
)

// writeGeneratedFile creates dir as needed and writes the generated file. An
// already-identical file is left untouched, keeping mtimes (and the watchers
// and build caches keyed on them) stable across no-op regenerations.
func writeGeneratedFile(path string, content []byte) error {
	if _, err := fs.WriteIfChanged(path, content, fs.WithMkdirAll(true), fs.WithAtomic(true)); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
