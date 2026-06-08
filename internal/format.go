package internal

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/go-rotini/fs"
	"github.com/go-rotini/jsonc"
	"github.com/go-rotini/yaml"
)

// fileFormat identifies the on-disk serialization of a rotini spec or
// conf file.
type fileFormat int

const (
	formatUnknown fileFormat = iota
	formatYAML
	formatJSON
	formatJSONC
)

// errUnsupportedFormat is returned for a spec or conf path whose extension
// is not one of the supported serializations (.yaml, .yml, .json, .jsonc).
var errUnsupportedFormat = errors.New("unsupported file format")

// detectFormat maps a file path's extension to its serialization format,
// returning formatUnknown for unrecognized extensions.
func detectFormat(path string) fileFormat {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".yaml", ".yml":
		return formatYAML
	case ".json":
		return formatJSON
	case ".jsonc":
		return formatJSONC
	default:
		return formatUnknown
	}
}

// readFile reads the file at path and decodes it into a value of type T,
// choosing the decoder from the file extension. YAML, JSON, and JSONC all
// honor the json struct tags carried by the generated Spec and Conf types.
func readFile[T any](path string) (*T, error) {
	format := detectFormat(path)
	if format == formatUnknown {
		return nil, fmt.Errorf("%w: %s", errUnsupportedFormat, path)
	}
	data, err := fs.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	out := new(T)
	switch format {
	case formatYAML:
		err = yaml.Unmarshal(data, out)
	case formatJSON:
		err = json.Unmarshal(data, out)
	case formatJSONC:
		err = jsonc.Unmarshal(data, out)
	default:
		return nil, fmt.Errorf("%w: %s", errUnsupportedFormat, path)
	}
	if err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}
	return out, nil
}

// writeFile encodes v in the format selected from path's extension and
// writes it atomically, creating parent directories as needed. JSONC files
// are written as standard JSON, which is a valid JSONC document.
func writeFile[T any](path string, v *T) error {
	var (
		data []byte
		err  error
	)
	switch detectFormat(path) {
	case formatYAML:
		data, err = yaml.Marshal(v)
	case formatJSON, formatJSONC:
		data, err = json.MarshalIndent(v, "", "  ")
		data = append(data, '\n')
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

// toJSON reads the file at path and returns its contents as canonical JSON
// bytes, regardless of the source serialization. It feeds documents to the
// jsonschema validator, which operates on JSON instances. The raw instance
// is returned (not a decoded struct) so schema rules like
// additionalProperties:false still see unknown fields.
func toJSON(path string) ([]byte, error) {
	format := detectFormat(path)
	if format == formatUnknown {
		return nil, fmt.Errorf("%w: %s", errUnsupportedFormat, path)
	}
	data, err := fs.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	switch format {
	case formatJSON:
		return data, nil
	case formatJSONC:
		out, err := jsonc.ToJSON(data)
		if err != nil {
			return nil, fmt.Errorf("convert %s to json: %w", path, err)
		}
		return out, nil
	case formatYAML:
		out, err := yaml.ToJSON(data)
		if err != nil {
			return nil, fmt.Errorf("convert %s to json: %w", path, err)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("%w: %s", errUnsupportedFormat, path)
	}
}

// readSpec reads and decodes the rotini spec file at path. The serialization
// (YAML, JSON, or JSONC) is selected from the file extension. It does not
// validate the document against the spec schema; use [Validate] for that.
func readSpec(path string) (*Spec, error) {
	return readFile[Spec](path)
}

// writeSpec encodes s and writes it to path, selecting the serialization from
// the file extension.
func writeSpec(path string, s *Spec) error {
	return writeFile(path, s)
}

// readConf reads and decodes the rotini conf file at path. The serialization
// (YAML, JSON, or JSONC) is selected from the file extension. It does not
// validate the document against the conf schema; use [Validate] for that.
func readConf(path string) (*Conf, error) {
	return readFile[Conf](path)
}

// writeConf encodes c and writes it to path, selecting the serialization from
// the file extension.
func writeConf(path string, c *Conf) error {
	return writeFile(path, c)
}
