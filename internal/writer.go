package internal

// This file owns writing outputs: encoded spec/conf documents and every
// generated artifact (Go files, doc pages, completion scripts, seeds). All
// writes go through go-rotini/fs — atomic (temp file then rename, so an
// interrupted pass never leaves a torn, half-written file) and parent-creating.
// Reading inputs lives in reader.go.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/go-rotini/fs"
	"github.com/go-rotini/toml"
	"github.com/go-rotini/yaml"
)

// writeFile encodes v in the format selected from path's extension and writes it
// atomically, creating parent directories as needed. JSONC files are written as
// standard JSON, which is a valid JSONC document.
func writeFile[T any](path string, v *T) error {
	var (
		data []byte
		err  error
	)
	switch detectFileFormat(path) {
	case formatYAML:
		data, err = yaml.Marshal(v)
	case formatJSON, formatJSONC:
		data, err = json.MarshalIndent(v, "", "  ")
		data = append(data, '\n')
	case formatTOML:
		data, err = toml.Marshal(v)
	default:
		return fmt.Errorf("%w: %s", errUnsupportedFormat, path)
	}
	if err != nil {
		return fmt.Errorf("encode %s: %w", path, err)
	}

	if err := fs.WriteFile(path, data, fs.WithMkdirAll(true), fs.WithAtomic(true)); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// writeSpec encodes s and writes it to path, selecting the serialization from the
// file extension.
func writeSpec(path string, s *Spec) error {
	return writeFile(path, s)
}

// writeConf encodes c and writes it to path, selecting the serialization from the
// file extension.
func writeConf(path string, c *Conf) error {
	return writeFile(path, c)
}

// writeGeneratedFile creates dir as needed and writes the generated file.
func writeGeneratedFile(path string, content []byte) error {
	if err := fs.WriteFile(path, content, fs.WithMkdirAll(true), fs.WithAtomic(true)); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// writeFileBytes writes content to path atomically and creates the parent
// directory if needed. It backs every non-Go output: help/man pages, completion
// scripts, and handler stubs.
func writeFileBytes(path, content string) error {
	if err := fs.WriteFile(path, []byte(content), fs.WithMkdirAll(true), fs.WithAtomic(true)); err != nil {
		return fmt.Errorf("write %s: %w", filepath.Base(path), err)
	}
	return nil
}

// writeIfChanged writes content only when it differs from the file on disk,
// keeping mtimes (and watch loops) stable.
func writeIfChanged(path, content string) error {
	if existing, err := os.ReadFile(path); err == nil && string(existing) == content {
		return nil
	}
	return writeFileBytes(path, content)
}
