package internal

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"github.com/go-rotini/fs"
)

// InitOptions configures an [Initialize] invocation. Name is required;
// the rest have sensible defaults.
type InitOptions struct {
	// Name is the root command name embedded in the generated
	// .rotini.spec.yaml. Must match the spec schema's name pattern
	// ([a-zA-Z][a-zA-Z0-9_-]*).
	Name string

	// Dir is the directory the three scaffold files land in. When
	// empty, the current working directory is used. The directory must
	// already exist.
	Dir string

	// ModulePath is the Go module path used in main.go's import
	// statements (e.g., "github.com/me/myapp"). When empty,
	// [Initialize] reads go.mod from Dir or its ancestors to derive
	// the path. If neither override nor go.mod is available, returns
	// [ErrModulePathUnknown].
	ModulePath string

	// RotiniVersion is the rotini version embedded in the schema URLs
	// (e.g., "1.2.3"). When empty, "0.0.0" is used as a placeholder so
	// the generated files are syntactically valid; users typically
	// supply this from a `runtime/debug.ReadBuildInfo()`-resolved
	// build version.
	RotiniVersion string

	// Force, when true, overwrites existing files. When false (default),
	// [Initialize] returns [ErrAlreadyExists] for the first file it
	// finds already present so users don't accidentally clobber
	// hand-edited scaffolds.
	Force bool
}

// InitResult reports the outcome of an [Initialize] invocation.
type InitResult struct {
	// FilesWritten lists the absolute paths Initialize created. Empty
	// when Force is false and any target was already present.
	FilesWritten []string
}

// Initialize lays out a new rotini project: writes
// .rotini.spec.yaml, .rotini.conf.yaml, and main.go into opts.Dir
// using the embedded init templates.
//
// On Force=false, the entire operation aborts before any write when
// any target already exists. On Force=true, each target is
// overwritten atomically via fs.WriteFile.
func Initialize(opts InitOptions) (*InitResult, error) {
	if err := validateInitOptions(&opts); err != nil {
		return nil, err
	}

	dir := opts.Dir
	if dir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("internal: get working directory: %w", err)
		}
		dir = cwd
	}

	modulePath, err := resolveModulePath(&opts, dir)
	if err != nil {
		return nil, err
	}

	version := opts.RotiniVersion
	if version == "" {
		version = "0.0.0"
	}

	specPath := filepath.Join(dir, ".rotini.spec.yaml")
	confPath := filepath.Join(dir, ".rotini.conf.yaml")
	mainPath := filepath.Join(dir, "main.go")

	if !opts.Force {
		for _, p := range []string{specPath, confPath, mainPath} {
			if fs.Exists(p) {
				return nil, fmt.Errorf("%w: %s (pass Force=true to overwrite)", ErrAlreadyExists, p)
			}
		}
	}

	specBytes, err := renderInitTemplate("init-spec.yaml.tmpl", map[string]any{
		"Name":          opts.Name,
		"RotiniVersion": version,
	})
	if err != nil {
		return nil, err
	}
	confBytes, err := renderInitTemplate("init-conf.yaml.tmpl", map[string]any{
		"RotiniVersion": version,
	})
	if err != nil {
		return nil, err
	}
	mainBytes, err := renderInitTemplate("init-main.go.tmpl", map[string]any{
		"ModulePath": modulePath,
	})
	if err != nil {
		return nil, err
	}

	writes := []struct {
		path string
		data []byte
	}{
		{specPath, specBytes},
		{confPath, confBytes},
		{mainPath, mainBytes},
	}
	result := &InitResult{}
	for _, w := range writes {
		if err := fs.WriteFile(w.path, w.data, fs.WithAtomic(true), fs.WithMkdirAll(true)); err != nil {
			return nil, fmt.Errorf("internal: write %s: %w", w.path, err)
		}
		result.FilesWritten = append(result.FilesWritten, w.path)
	}
	return result, nil
}

// ErrAlreadyExists is returned by [Initialize] when a target file
// already exists and Force is false.
var ErrAlreadyExists = errors.New("internal: file already exists")

// ErrModulePathUnknown is returned by [Initialize] when no ModulePath
// override is supplied and no go.mod is reachable from the target
// directory.
var ErrModulePathUnknown = errors.New("internal: module path unknown (pass InitOptions.ModulePath or run from a Go module)")

// reInitName mirrors the spec schema's root-command-name pattern. We
// validate up-front so users get a clear error before any template
// renders.
var reInitName = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_-]*$`)

// validateInitOptions enforces InitOptions invariants and mutates opts
// (currently a no-op; reserved for future defaulting).
func validateInitOptions(opts *InitOptions) error {
	if opts.Name == "" {
		return ErrMissingInitName
	}
	if !reInitName.MatchString(opts.Name) {
		return fmt.Errorf("%w: %q must match %s", ErrInvalidInitName, opts.Name, reInitName.String())
	}
	return nil
}

// ErrMissingInitName is returned by [Initialize] when opts.Name is
// empty.
var ErrMissingInitName = errors.New("internal: InitOptions.Name is required")

// ErrInvalidInitName is returned by [Initialize] when opts.Name does
// not match the spec schema's name pattern.
var ErrInvalidInitName = errors.New("internal: InitOptions.Name has invalid format")

// resolveModulePath returns the module path to embed in main.go.
// Explicit override wins; otherwise [findModulePath] walks dir's
// ancestors looking for go.mod.
func resolveModulePath(opts *InitOptions, dir string) (string, error) {
	if opts.ModulePath != "" {
		return opts.ModulePath, nil
	}
	mod, err := findModulePath(dir)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrModulePathUnknown, err)
	}
	return mod, nil
}

// errNoGoMod is the internal sentinel returned by [findModulePath]
// when no go.mod is reachable from the starting directory. Callers
// wrap it with [ErrModulePathUnknown] before surfacing.
var errNoGoMod = errors.New("no go.mod found in any parent directory")

// errNoModuleLine is the internal sentinel returned by
// [readModuleName] when go.mod is present but missing its `module`
// declaration.
var errNoModuleLine = errors.New("no module declaration in go.mod")

// findModulePath walks dir's ancestors looking for go.mod and returns
// the module declaration's value. Returns [errNoGoMod] when no go.mod
// is found before reaching the filesystem root.
func findModulePath(dir string) (string, error) {
	for {
		goModPath := filepath.Join(dir, "go.mod")
		if fs.Exists(goModPath) {
			return readModuleName(goModPath)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errNoGoMod
		}
		dir = parent
	}
}

// readModuleName reads go.mod at path and returns the `module <name>`
// declaration. Returns [errNoModuleLine] when no module line is
// present.
func readModuleName(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read go.mod: %w", err)
	}
	for line := range bytes.SplitSeq(data, []byte("\n")) {
		trimmed := bytes.TrimSpace(line)
		if bytes.HasPrefix(trimmed, []byte("module ")) {
			return string(bytes.TrimSpace(trimmed[len("module "):])), nil
		}
	}
	return "", errNoModuleLine
}

// renderInitTemplate executes the named init template (text/template,
// no gofmt) against data and returns the rendered bytes. Init
// templates emit non-Go files (YAML) plus one Go file (main.go) that
// is small enough not to need gofmt — its content is hand-formatted
// in the template.
func renderInitTemplate(name string, data map[string]any) ([]byte, error) {
	tmpl, err := loadTemplates()
	if err != nil {
		return nil, fmt.Errorf("internal: load templates: %w", err)
	}
	t := tmpl.Lookup(name)
	if t == nil {
		return nil, fmt.Errorf("%w: %q", ErrUnknownTemplate, name)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("internal: execute %s: %w", name, err)
	}
	return buf.Bytes(), nil
}
