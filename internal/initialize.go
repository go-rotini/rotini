package internal

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"github.com/go-rotini/fs"
	"github.com/go-rotini/jsonc"
	"github.com/go-rotini/yaml"
)

// InitFormat identifies the on-disk encoding of the .rotini.spec /
// .rotini.conf files [Initialize] writes. The scaffold template is
// authored once in YAML (the most readable form); other formats are
// produced by round-tripping through the matching codec package.
type InitFormat string

const (
	// InitFormatYAML emits .rotini.spec.yaml + .rotini.conf.yaml.
	InitFormatYAML InitFormat = "yaml"

	// InitFormatJSON emits .rotini.spec.json + .rotini.conf.json
	// (indented via [encoding/json.MarshalIndent]).
	InitFormatJSON InitFormat = "json"

	// InitFormatJSONC emits .rotini.spec.jsonc + .rotini.conf.jsonc
	// via [github.com/go-rotini/jsonc].MarshalIndent — valid JSON
	// today, ready for the user to add comments after.
	InitFormatJSONC InitFormat = "jsonc"
)

// SupportedInitFormats lists every [InitFormat] [Initialize] knows
// how to write. Useful for `--format` flag enums.
var SupportedInitFormats = []InitFormat{InitFormatYAML, InitFormatJSON, InitFormatJSONC}

// ErrUnsupportedInitFormat is returned by [Initialize] when
// [InitOptions.Format] is not one of [SupportedInitFormats].
var ErrUnsupportedInitFormat = errors.New("internal: unsupported init format")

// InitOptions configures an [Initialize] invocation. Name is required;
// the rest have sensible defaults.
type InitOptions struct {
	// Name is the root command name embedded in the generated spec
	// file. Must match the spec schema's name pattern
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

	// Format selects the on-disk encoding for the spec + conf files.
	// Empty defaults to [InitFormatYAML]. main.go is always Go;
	// Format does not affect it.
	Format InitFormat

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

// Initialize lays out a new rotini project: writes the spec file,
// the conf file, and main.go into opts.Dir using the embedded init
// templates. Spec + conf encoding follows opts.Format; main.go is
// always Go.
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

	format := opts.Format
	if format == "" {
		format = InitFormatYAML
	}
	ext, err := extensionForFormat(format)
	if err != nil {
		return nil, err
	}

	specPath := filepath.Join(dir, ".rotini.spec."+ext)
	confPath := filepath.Join(dir, ".rotini.conf."+ext)
	mainPath := filepath.Join(dir, "main.go")

	if !opts.Force {
		for _, p := range []string{specPath, confPath, mainPath} {
			if fs.Exists(p) {
				return nil, fmt.Errorf("%w: %s (pass Force=true to overwrite)", ErrAlreadyExists, p)
			}
		}
	}

	// Render the YAML scaffold templates, then convert to the
	// requested format. JSON / JSONC outputs preserve the field
	// structure and values — comments in YAML are dropped during
	// round-trip; we accept that for now since the init scaffold
	// has no user-meaningful comments.
	specYAML, err := renderInitTemplate("rotini-spec.yaml.tmpl", map[string]any{
		"Name":          opts.Name,
		"RotiniVersion": version,
	})
	if err != nil {
		return nil, err
	}
	confYAML, err := renderInitTemplate("rotini-conf.yaml.tmpl", map[string]any{
		"RotiniVersion": version,
	})
	if err != nil {
		return nil, err
	}
	mainBytes, err := renderInitTemplate("rotini-main.go.tmpl", map[string]any{
		"ModulePath": modulePath,
	})
	if err != nil {
		return nil, err
	}

	specBytes, err := convertFormat(specYAML, format)
	if err != nil {
		return nil, fmt.Errorf("internal: convert spec to %s: %w", format, err)
	}
	confBytes, err := convertFormat(confYAML, format)
	if err != nil {
		return nil, fmt.Errorf("internal: convert conf to %s: %w", format, err)
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

// extensionForFormat returns the file extension (no dot) for an
// [InitFormat]. Returns [ErrUnsupportedInitFormat] for unknown values.
func extensionForFormat(f InitFormat) (string, error) {
	switch f {
	case InitFormatYAML:
		return "yaml", nil
	case InitFormatJSON:
		return "json", nil
	case InitFormatJSONC:
		return "jsonc", nil
	default:
		return "", fmt.Errorf("%w: %q (supported: %v)", ErrUnsupportedInitFormat, f, SupportedInitFormats)
	}
}

// convertFormat re-encodes yamlBytes into the target [InitFormat]:
//
//   - yaml  → returned unchanged.
//   - json  → yaml.Unmarshal → json.MarshalIndent.
//   - jsonc → yaml.Unmarshal → jsonc.MarshalIndent.
//
// JSON / JSONC outputs receive a trailing newline so editors that
// insert one don't show spurious diffs on first save.
func convertFormat(yamlBytes []byte, target InitFormat) ([]byte, error) {
	if target == InitFormatYAML {
		return yamlBytes, nil
	}
	var v any
	if err := yaml.Unmarshal(yamlBytes, &v); err != nil {
		return nil, fmt.Errorf("decode yaml: %w", err)
	}
	switch target {
	case InitFormatJSON:
		out, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			return nil, fmt.Errorf("encode json: %w", err)
		}
		return append(out, '\n'), nil
	case InitFormatJSONC:
		out, err := jsonc.MarshalIndent(v, "  ")
		if err != nil {
			return nil, fmt.Errorf("encode jsonc: %w", err)
		}
		return append(out, '\n'), nil
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedInitFormat, target)
	}
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
