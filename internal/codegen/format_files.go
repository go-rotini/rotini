package codegen

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// FormatFn is the signature of [FormatFiles], for the companion CLI's test doubles.
type FormatFn func(paths []string, kind string, check bool) (changed []string, err error)

// FormatFiles rewrites each YAML spec, conf or composed command file in canonical form (see
// [FormatYAML]). With check it writes nothing. It returns the files that changed, or would
// have, as they were given. A file that can't be formatted is an error naming it; the others
// are still formatted, and all the errors are joined.
func FormatFiles(paths []string, kind string, check bool) ([]string, error) {
	pl := newPlanner(check)
	var changed []string
	var errs []error
	for _, path := range paths {
		did, err := formatFile(pl, path, kind)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", path, err))
			continue
		}
		if did {
			changed = append(changed, path)
		}
	}
	return changed, errors.Join(errs...)
}

func formatFile(pl *planner, path, kind string) (bool, error) {
	switch ext := strings.ToLower(filepath.Ext(path)); ext {
	case ".yaml", ".yml":
	case ".json", ".jsonc", ".toml":
		return false, fmt.Errorf("formatting %s files isn't supported yet", strings.ToUpper(ext[1:]))
	default:
		return false, fmt.Errorf("can't tell the format from the extension %q; rotini fmt formats .yaml and .yml files", ext)
	}
	src, err := os.ReadFile(path)
	if err != nil {
		return false, fmt.Errorf("read: %w", err)
	}
	out, err := FormatYAML(src, kind, path)
	if err != nil {
		return false, err
	}
	if bytes.Equal(out, src) {
		return false, nil
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return false, fmt.Errorf("resolve: %w", err)
	}
	return true, pl.write(abs, out)
}
