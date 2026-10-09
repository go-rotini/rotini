package codegen

// Reading inputs: spec and conf decoding (format chosen by extension), conversion to JSON
// for schema validation, spec and conf discovery, and module resolution.

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

// fileFormat is a supported spec/conf serialization. Its value is also the canonical file
// extension.
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

// readRaw detects path's format from its extension and reads the file, erroring on an
// unknown extension or a read failure.
func readRaw(path string) (fileFormat, []byte, error) {
	format := detectFileFormat(path)
	if format == formatUnknown {
		return formatUnknown, nil, fmt.Errorf("%w: %s", errUnsupportedFormat, path)
	}
	data, err := fs.ReadFile(path)
	if err != nil {
		// The fs error repeats the path; report a missing file plainly.
		if errors.Is(err, os.ErrNotExist) {
			return format, nil, fmt.Errorf("%s: %w", path, os.ErrNotExist)
		}
		return format, nil, fmt.Errorf("read %s: %w", path, err)
	}
	return format, data, nil
}

// readFile reads and decodes the file at path into a T, choosing the decoder by extension.
// Every decoder honors the json struct tags on the generated Spec and Conf types.
func readFile[T any](path string) (*T, error) {
	format, data, err := readRaw(path)
	if err != nil {
		return nil, err
	}
	return decodeData[T](format, data, path)
}

// decodeData decodes one read document into a T and normalizes it. path labels a failure.
// Loaders call it directly so one read feeds both the decoded value and bytesToJSON.
func decodeData[T any](format fileFormat, data []byte, path string) (*T, error) {
	out := new(T)
	if err := checkDuplicateKeys(docKind(out), format, data, path); err != nil {
		return nil, err
	}
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
		if dup := tomlDuplicateKey(docKind(out), err, path); dup != nil {
			return nil, dup
		}
		return nil, &decodeError{path: path, format: format, data: data, err: err}
	}
	if n, ok := any(out).(normalizer); ok {
		n.normalize()
	}
	return out, nil
}

// decodeError reports a document that read but did not decode into rotini's Go types,
// usually a value of the wrong type. It carries the raw bytes so the Processor can run the
// schema validator instead, whose positioned message is clearer than the decoder's.
type decodeError struct {
	path   string
	format fileFormat
	data   []byte
	err    error
}

func (e *decodeError) Error() string { return fmt.Sprintf("decode %s: %v", e.path, e.err) }
func (e *decodeError) Unwrap() error { return e.err }

// bytesToJSON converts a document in any supported format to JSON for the schema validator.
// Converting the raw document, not the decoded struct, keeps unknown fields visible.
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

// readSpec reads and decodes the spec at path without schema validation.
func readSpec(path string) (*Spec, error) {
	return readFile[Spec](path)
}

// readConf reads and decodes the conf at path without schema validation.
func readConf(path string) (*Conf, error) {
	return readFile[Conf](path)
}

// errSpecPathRequired is returned when no spec path is given and none is discovered.
var errSpecPathRequired = errors.New("no .rotini.spec.* file in the working directory; pass the spec's path")

// fallbackExtensions is the discovery precedence: the first .rotini.<type>.<ext> that exists
// wins.
var fallbackExtensions = []string{"yml", "yaml", "toml", "json", "jsonc"}

// discoverFile returns the first existing .rotini.<ft>.<ext> in dir in fallbackExtensions
// order, or ("", false).
func discoverFile(dir string, ft fileType) (string, bool) {
	return fs.FindWithExtensions(dir, ".rotini."+string(ft), fallbackExtensions)
}

// resolveSpecPath returns path when set, otherwise the first .rotini.spec.* in the working
// directory, or "" when none is found.
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

// ResolvePaths resolves the spec and conf paths the pipeline would read. An empty specPath
// discovers the first .rotini.spec.* in the working directory; an empty confPath discovers
// the first .rotini.conf.* beside the spec, and conf is "" when there is none. Given paths
// are returned unchanged and unchecked; discovered paths are made relative for display.
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
		spec = displayPath(spec)
	}
	if confPath == "" && conf != "" {
		conf = displayPath(conf)
	}
	return spec, conf, nil
}

// resolveConfBesideSpec returns confPath when set, otherwise the first .rotini.conf.* beside
// the spec, or "" when none is found.
func resolveConfBesideSpec(specPath, confPath string) string {
	if confPath != "" {
		return confPath
	}
	found, _ := discoverFile(filepath.Dir(specPath), fileTypeConf)
	return found
}

// discoverConf returns the first .rotini.conf.* in dir, or an error when none exists.
func discoverConf(dir string) (string, error) {
	if found, ok := discoverFile(dir, fileTypeConf); ok {
		return found, nil
	}
	return "", fmt.Errorf("no .rotini.%s.* file found in %s", fileTypeConf, dir)
}

// findModule walks up from the working directory to the nearest go.mod and returns the
// module root directory and module path.
func findModule() (root, name string, err error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", "", fmt.Errorf("get working directory: %w", err)
	}
	// Raise fs's default ancestor bound (32) to effectively reach the filesystem root.
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
