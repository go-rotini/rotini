package codegen

// This file owns reading inputs: spec/conf file decoding (serialization chosen
// from the extension, via go-rotini/fs), raw-JSON conversion for schema
// validation, the spec/conf discovery fallbacks, and module resolution.
// Writing outputs lives in generate_writer.go.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-rotini/fs"
	"github.com/go-rotini/jsonc"
	"github.com/go-rotini/toml"
	"github.com/go-rotini/yaml"
)

type fileType string

const (
	fileTypeSpec fileType = "spec"
	fileTypeConf fileType = "conf"
)

// fileFormat is a supported spec/conf serialization. Its value doubles as the
// canonical file extension (and the seed transcode target — see convert).
type fileFormat string

const (
	formatUnknown fileFormat = ""
	formatYAML    fileFormat = "yaml"
	formatJSON    fileFormat = "json"
	formatJSONC   fileFormat = "jsonc"
	formatTOML    fileFormat = "toml"
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
// preamble of readFile and reconcileDoc.
func readRaw(path string) (fileFormat, []byte, error) {
	format := detectFileFormat(path)
	if format == formatUnknown {
		return formatUnknown, nil, fmt.Errorf("%w: %s", errUnsupportedFormat, path)
	}
	data, err := fs.ReadFile(path)
	if err != nil {
		// The fs error nests the os one, so its text names the path three times; a missing
		// file is common enough to deserve one plain sentence.
		if errors.Is(err, os.ErrNotExist) {
			return format, nil, fmt.Errorf("%s: %w", path, os.ErrNotExist)
		}
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
	return decodeData[T](format, data, path)
}

// decodeData decodes one read document into a value of type T. path labels a
// decode failure; the loaders use this (rather than readFile) so one read feeds
// both the decoded struct and the raw JSON instance (bytesToJSON) — validation
// then judges exactly the bytes generation consumes.
func decodeData[T any](format fileFormat, data []byte, path string) (*T, error) {
	out := new(T)
	var err error
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
		return nil, &decodeError{path: path, format: format, data: data, err: err}
	}
	if n, ok := any(out).(normalizer); ok {
		n.normalize()
	}
	return out, nil
}

// decodeError is a document that read fine but would not decode into rotini's Go types — almost
// always a value of the wrong type, such as `minimum: "five"` or `required: [name]` on an input.
//
// It carries the raw bytes so the Processor can hand the same document to the schema validator,
// which says what is wrong with a position and a JSON pointer. The decoder's own message says
// neither, and leaks Go types to someone who wrote YAML:
//
//	decode spec.yaml: yaml: unmarshal errors: line 17: cannot unmarshal !!seq into Go value of type bool
type decodeError struct {
	path   string
	format fileFormat
	data   []byte
	err    error
}

func (e *decodeError) Error() string { return fmt.Sprintf("decode %s: %v", e.path, e.err) }
func (e *decodeError) Unwrap() error { return e.err }

// bytesToJSON converts one read document to canonical JSON bytes, whatever its source
// serialization, for the jsonschema validator. The raw instance is returned rather than a
// decoded struct, so rules like additionalProperties:false still see unknown fields.
func bytesToJSON(format fileFormat, data []byte) ([]byte, error) {
	var (
		out []byte
		err error
	)
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
		return nil, errUnsupportedFormat
	}
	if err != nil {
		return nil, fmt.Errorf("convert %s to json: %w", format, err)
	}
	return out, nil
}

// readSpec reads and decodes the rotini spec file at path (serialization chosen from
// the extension). It does not validate against the schema — the Processor's reconcile +
// validate stages do that.
func readSpec(path string) (*Spec, error) {
	return readFile[Spec](path)
}

// readConf reads and decodes the rotini conf file at path (serialization chosen from
// the extension). It does not validate against the schema — the Processor's reconcile +
// validate stages do that.
func readConf(path string) (*Conf, error) {
	return readFile[Conf](path)
}

// ─── discovery ───────────────────────────────────────────────────────────────.

// errSpecPathRequired is reported when no spec-file path is supplied and none of the
// fallback locations resolve to a spec.
var errSpecPathRequired = errors.New("no .rotini.spec.* file in the working directory — pass the spec's path")

// fallbackExtensions is the spec/conf discovery precedence: the first
// .rotini.<type>.<ext> that exists wins.
var fallbackExtensions = []string{"yml", "yaml", "toml", "json", "jsonc"}

// discoverFile returns the first existing .rotini.<fileType>.<ext> in dir, in
// fallbackExtensions precedence, or ("", false) when none exist. The
// extension-fallback search is delegated to go-rotini/fs.
func discoverFile(dir string, ft fileType) (string, bool) {
	return fs.FindWithExtensions(dir, ".rotini."+string(ft), fallbackExtensions)
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
		return "", fmt.Errorf("get working directory: %w", err)
	}
	found, _ := discoverFile(cwd, fileTypeSpec)
	return found, nil
}

// ResolvePaths resolves the spec and conf files a generate or validate of specPath and
// confPath reads, by the same rules the pipeline uses: an empty specPath is the first
// .rotini.spec.* in the working directory, and an empty confPath is the first .rotini.conf.*
// beside the spec. conf is "" when there is none, and the conf defaults apply. A path the
// caller gave is returned as given; whether it exists is the pipeline's to report. A
// discovered path is made relative to the working directory, for display.
func ResolvePaths(specPath, confPath string) (spec, conf string, err error) {
	spec, err = resolveSpecPath(specPath)
	if err != nil {
		return "", "", err
	}
	if spec == "" {
		return "", "", errSpecPathRequired
	}
	conf = resolveConfBesideSpec(spec, confPath)
	if specPath == "" {
		spec = displayPath(spec) // discovered from the working directory, so absolute
	}
	if confPath == "" && conf != "" {
		conf = displayPath(conf)
	}
	return spec, conf, nil
}

// resolveConfBesideSpec resolves the conf file path: the given path when set,
// otherwise the first .rotini.conf.* beside the spec, or "" when none is found
// (callers fall back to a default Conf).
func resolveConfBesideSpec(specPath, confPath string) string {
	if confPath != "" {
		return confPath
	}
	found, _ := discoverFile(filepath.Dir(specPath), fileTypeConf)
	return found
}

// discoverConf returns the first .rotini.conf.* file that exists in dir, or an
// error when none is found. Unlike resolveConfBesideSpec it errors on a miss —
// its callers expect a conf to be present (a module-root conf, a composed
// child's conf).
func discoverConf(dir string) (string, error) {
	if found, ok := discoverFile(dir, fileTypeConf); ok {
		return found, nil
	}
	return "", fmt.Errorf("no .rotini.%s.* file found in %s", fileTypeConf, dir)
}

// findModule walks up from the working directory to the nearest go.mod and
// returns the module root directory and the module path declared in it.
func findModule() (root, name string, err error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", "", fmt.Errorf("get working directory: %w", err)
	}
	// fs.FindUp walks up to the nearest go.mod; the high ancestor bound keeps
	// the original unbounded-to-root reach (fs defaults to 32).
	goMod, ok, err := fs.FindUp("go.mod", dir, fs.WithMaxAncestors(256))
	if err != nil {
		return "", "", fmt.Errorf("find go.mod: %w", err)
	}
	if !ok {
		return "", "", errors.New("go.mod not found in any parent of working directory")
	}
	data, err := os.ReadFile(goMod)
	if err != nil {
		return "", "", fmt.Errorf("read %s: %w", goMod, err)
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		line = strings.TrimSpace(line)
		if after, ok := strings.CutPrefix(line, "module "); ok {
			return filepath.Dir(goMod), strings.TrimSpace(after), nil
		}
	}
	return "", "", fmt.Errorf("no module path in %s", goMod)
}
