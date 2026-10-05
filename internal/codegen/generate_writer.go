package codegen

import (
	"fmt"

	"github.com/go-rotini/fs"
)

// writeGeneratedFile writes a generated file atomically, creating its directory as needed. An
// identical file is left untouched, so a no-op regeneration keeps mtimes stable.
func writeGeneratedFile(path string, content []byte) error {
	if _, err := fs.WriteIfChanged(path, content, fs.WithMkdirAll(true), fs.WithAtomic(true)); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
