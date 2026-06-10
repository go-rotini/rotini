package internal

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/go-rotini/fs"
	"github.com/go-rotini/jsonc"
	"github.com/go-rotini/jsonschema"
	"github.com/go-rotini/toml"
	"github.com/go-rotini/yaml"
)

type fileType string

const (
	fileTypeSpec fileType = "spec"
	fileTypeConf fileType = "conf"
)

var (
	//go:embed schema-spec.json
	schemaSpecFileBytes []byte
	//go:embed schema-conf.json
	schemaConfFileBytes []byte
)

// ─── serialization ───────────────────────────────────────────────────────────

type fileFormat int

const (
	formatUnknown fileFormat = iota
	formatYAML
	formatJSON
	formatJSONC
	formatTOML
)

var errUnsupportedFormat = errors.New("unsupported file format")

// detectFileFormat maps a path's extension to its serialization format.
func detectFileFormat(path string) fileFormat {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".yaml", ".yml":
		return formatYAML
	case ".json":
		return formatJSON
	case ".jsonc":
		return formatJSONC
	case ".toml":
		return formatTOML
	default:
		return formatUnknown
	}
}

// readRaw detects path's serialization format from its extension and reads the
// file's bytes, erroring on an unknown extension or a read failure. It is the shared
// preamble of readFile and toJSON.
func readRaw(path string) (fileFormat, []byte, error) {
	format := detectFileFormat(path)
	if format == formatUnknown {
		return formatUnknown, nil, fmt.Errorf("%w: %s", errUnsupportedFormat, path)
	}
	data, err := fs.ReadFile(path)
	if err != nil {
		return format, nil, fmt.Errorf("read %s: %w", path, err)
	}
	return format, data, nil
}

// readFile reads the file at path and decodes it into a value of type T, choosing the
// decoder from the file extension. YAML, JSON, JSONC, and TOML all honor the json
// struct tags carried by the generated Spec and Conf types.
func readFile[T any](path string) (*T, error) {
	format, data, err := readRaw(path)
	if err != nil {
		return nil, err
	}

	out := new(T)
	switch format {
	case formatYAML:
		err = yaml.Unmarshal(data, out)
	case formatJSON:
		err = json.Unmarshal(data, out)
	case formatJSONC:
		err = jsonc.Unmarshal(data, out)
	case formatTOML:
		err = toml.Unmarshal(data, out)
	default:
		return nil, fmt.Errorf("%w: %s", errUnsupportedFormat, path)
	}
	if err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}
	return out, nil
}

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

// toJSON reads the file at path and returns its contents as canonical JSON bytes,
// regardless of the source serialization. It feeds documents to the jsonschema
// validator, which operates on JSON instances. The raw instance is returned (not a
// decoded struct) so schema rules like additionalProperties:false still see unknown
// fields.
func toJSON(path string) ([]byte, error) {
	format, data, err := readRaw(path)
	if err != nil {
		return nil, err
	}
	var out []byte
	switch format {
	case formatJSON:
		return data, nil
	case formatJSONC:
		out, err = jsonc.ToJSON(data)
	case formatYAML:
		out, err = yaml.ToJSON(data)
	case formatTOML:
		out, err = toml.ToJSON(data)
	default:
		return nil, fmt.Errorf("%w: %s", errUnsupportedFormat, path)
	}
	if err != nil {
		return nil, fmt.Errorf("convert %s to json: %w", path, err)
	}
	return out, nil
}

// readSpec reads and decodes the rotini spec file at path (serialization chosen from
// the extension). It does not validate against the schema; build a specLoader for that.
func readSpec(path string) (*Spec, error) {
	return readFile[Spec](path)
}

// writeSpec encodes s and writes it to path, selecting the serialization from the
// file extension.
func writeSpec(path string, s *Spec) error {
	return writeFile(path, s)
}

// readConf reads and decodes the rotini conf file at path (serialization chosen from
// the extension). It does not validate against the schema; build a confLoader for that.
func readConf(path string) (*Conf, error) {
	return readFile[Conf](path)
}

// writeConf encodes c and writes it to path, selecting the serialization from the
// file extension.
func writeConf(path string, c *Conf) error {
	return writeFile(path, c)
}

// ─── embedded schema compilation (cached) ────────────────────────────────────

// The embedded rotini JSON Schemas are immutable, so each is compiled at most once
// per process and the result cached — the cache spares the recompile when a fresh
// specLoader/confLoader is built per pass (e.g. watch mode rebuilds one on every change).
var (
	specSchemaOnce sync.Once
	specSchema     *jsonschema.Schema
	specSchemaErr  error

	confSchemaOnce sync.Once
	confSchema     *jsonschema.Schema
	confSchemaErr  error
)

// loadSpecSchema compiles the embedded spec schema once and returns the cached result.
func loadSpecSchema() (*jsonschema.Schema, error) {
	specSchemaOnce.Do(func() { specSchema, specSchemaErr = jsonschema.Compile(schemaSpecFileBytes) })
	return specSchema, specSchemaErr
}

// loadConfSchema compiles the embedded conf schema once and returns the cached result.
func loadConfSchema() (*jsonschema.Schema, error) {
	confSchemaOnce.Do(func() { confSchema, confSchemaErr = jsonschema.Compile(schemaConfFileBytes) })
	return confSchema, confSchemaErr
}

// ─── resolution ──────────────────────────────────────────────────────────────

// errSpecPathRequired is reported when no spec-file path is supplied and none of the
// fallback locations resolve to a spec.
var errSpecPathRequired = errors.New("spec file path is required")

// getFallbackPaths returns the default discovery locations for a spec or conf file
// within dir, in extension-precedence order.
func getFallbackPaths(dir string, fileType fileType) []string {
	fileExtensions := []string{"yml", "yaml", "toml", "json", "jsonc"}
	paths := make([]string, len(fileExtensions))
	for i, fileExtension := range fileExtensions {
		paths[i] = filepath.Join(dir, fmt.Sprintf(".rotini.%s.%s", fileType, fileExtension))
	}
	return paths
}

// firstExisting returns the first path in paths that exists on disk, or "" if none do.
func firstExisting(paths []string) string {
	for _, path := range paths {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return ""
}

// resolveSpecPath resolves the spec file path: the given path when set, otherwise the
// first .rotini.spec.* in the working directory, or "" when none is found (callers
// treat that as the required-spec error).
func resolveSpecPath(path string) (string, error) {
	if path != "" {
		return path, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return firstExisting(getFallbackPaths(cwd, fileTypeSpec)), nil
}

// resolveConfBesideSpec resolves the conf file path: the given path when set,
// otherwise the first .rotini.conf.* beside the spec, or "" when none is found
// (callers fall back to a default Conf).
func resolveConfBesideSpec(specPath, confPath string) string {
	if confPath != "" {
		return confPath
	}
	return firstExisting(getFallbackPaths(filepath.Dir(specPath), fileTypeConf))
}

// discoverFile returns the first .rotini.<fileType>.* file that exists in dir, or an
// error when none is found. Unlike resolveSpecPath/resolveConfBesideSpec it errors on
// a miss — the initializer uses it where a file is expected to be present (a module
// conf, a parent CLI's spec/conf).
func discoverFile(dir string, fileType fileType) (string, error) {
	if path := firstExisting(getFallbackPaths(dir, fileType)); path != "" {
		return path, nil
	}
	return "", fmt.Errorf("no .rotini.%s.* file found in %s", fileType, dir)
}

// ─── specLoader / confLoader ─────────────────────────────────────────────────────

// specLoader holds the compiled spec schema together with the resolved path and decoded
// content of the end-user's spec file — the correct schema and the user's content in
// one value, able to validate itself.
type specLoader struct {
	version string             // running binary version, for the $schema guard
	schema  *jsonschema.Schema // compiled spec JSON Schema
	path    string             // resolved spec path
	spec    *Spec              // decoded spec content
}

// newSpecLoader resolves the spec path (the given path, else the first .rotini.spec.* in
// the working directory) and reads + decodes the spec, holding it alongside the
// (cached) compiled spec schema. The spec is required: when no path is given and none
// is discovered it returns errSpecPathRequired. version is carried for the $schema guard.
func newSpecLoader(path, version string) (*specLoader, error) {
	schema, err := loadSpecSchema()
	if err != nil {
		return nil, err
	}

	resolved, err := resolveSpecPath(path)
	if err != nil {
		return nil, err
	}
	if resolved == "" {
		return nil, errSpecPathRequired
	}
	spec, err := readSpec(resolved)
	if err != nil {
		return nil, err
	}

	return &specLoader{version: version, schema: schema, path: resolved, spec: spec}, nil
}

// The specLoader/confLoader validate() methods live in validator.go (the validate op).

// confLoader holds the compiled conf schema together with the resolved path and decoded
// content of the end-user's conf file. The conf is optional: when none is found, path
// is "" and conf is a default &Conf{}.
type confLoader struct {
	version string             // running binary version, for the $schema guard
	schema  *jsonschema.Schema // compiled conf JSON Schema
	path    string             // resolved conf path ("" when none — defaults used)
	conf    *Conf              // decoded conf content (or default)
}

// newConfLoader resolves the conf path (the given path, else the first .rotini.conf.*
// beside the spec) and reads + decodes the conf, holding it alongside the (cached)
// compiled conf schema. The conf is optional: no path given and none discovered — or a
// resolved path that does not exist — yields a default Conf with an empty path.
// version is carried for the $schema guard.
func newConfLoader(specPath, confPath, version string) (*confLoader, error) {
	schema, err := loadConfSchema()
	if err != nil {
		return nil, err
	}

	f := &confLoader{version: version, schema: schema, conf: &Conf{}}

	resolved := resolveConfBesideSpec(specPath, confPath)
	if resolved == "" {
		return f, nil
	}
	if _, statErr := os.Stat(resolved); statErr != nil {
		if os.IsNotExist(statErr) {
			return f, nil // optional → default when the resolved path doesn't exist
		}
		return nil, fmt.Errorf("stat conf %s: %w", resolved, statErr)
	}

	conf, err := readConf(resolved)
	if err != nil {
		return nil, err
	}
	f.path, f.conf = resolved, conf
	return f, nil
}
