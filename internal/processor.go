package internal

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-rotini/fs"
	"github.com/go-rotini/jsonc"
	"github.com/go-rotini/jsonschema"
	"github.com/go-rotini/toml"
	"github.com/go-rotini/yaml"
)

type fileFormat int

const (
	formatUnknown fileFormat = iota
	formatYAML
	formatJSON
	formatJSONC
	formatTOML
)

var errUnsupportedFormat = errors.New("unsupported file format")

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

// readFile reads the file at path and decodes it into a value of type T,
// choosing the decoder from the file extension. YAML, JSON, and JSONC all
// honor the json struct tags carried by the generated Spec and Conf types.
func readFile[T any](path string) (*T, error) {
	format := detectFileFormat(path)
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

// writeFile encodes v in the format selected from path's extension and
// writes it atomically, creating parent directories as needed. JSONC files
// are written as standard JSON, which is a valid JSONC document.
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

// toJSON reads the file at path and returns its contents as canonical JSON
// bytes, regardless of the source serialization. It feeds documents to the
// jsonschema validator, which operates on JSON instances. The raw instance
// is returned (not a decoded struct) so schema rules like
// additionalProperties:false still see unknown fields.
func toJSON(path string) ([]byte, error) {
	format := detectFileFormat(path)
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
	case formatTOML:
		out, err := toml.ToJSON(data)
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
// ReadSpec is the exported read seam the codegen package's Loader uses to decode a
// spec across the package boundary; internal's own code uses the unexported
// readSpec. (Migration bridge: when the read path moves fully into codegen this
// goes away.)
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
// ReadConf is the exported read seam the codegen package's Loader uses to decode a
// conf across the package boundary; internal's own code uses the unexported
// readConf. (Migration bridge: when the read path moves fully into codegen this
// goes away.)
func readConf(path string) (*Conf, error) {
	return readFile[Conf](path)
}

// writeConf encodes c and writes it to path, selecting the serialization from
// the file extension.
func writeConf(path string, c *Conf) error {
	return writeFile(path, c)
}

type processor struct {
	version string

	schemaSpec *jsonschema.Schema
	spec       *Spec

	schemaConf *jsonschema.Schema
	conf       *Conf
}

//go:embed schema-spec.json
var schemaSpecBytes []byte

//go:embed schema-conf.json
var schemaConfBytes []byte

func NewProcessor(specFilePath string, confFilePath string, version string) (*processor, error) {
	schemaSpec, err := jsonschema.Compile(schemaSpecBytes)
	if err != nil {
		return nil, fmt.Errorf("compile spec schema: %w", err)
	}

	schemaConf, err := jsonschema.Compile(schemaConfBytes)
	if err != nil {
		return nil, fmt.Errorf("compile conf schema: %w", err)
	}

	return &processor{
		schemaSpec: schemaSpec,
		schemaConf: schemaConf,
	}, nil
}

func (*processor) compileSchema(schemaType string, schemaBytes []byte) (*jsonschema.Schema, error) {
	schema, err := jsonschema.Compile(schemaBytes)
	if err != nil {
		return nil, fmt.Errorf("compile %s schema: %w", schemaType, err)
	}
	return schema, nil
}

func getFallbackPaths(fileType string) ([]string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return nil, err
	}

	// fileType = spec or conf
	return []string{
		fmt.Sprintf("%s/.rotini.%s.yaml", dir, fileType),
		fmt.Sprintf("%s/.rotini.%s.toml", dir, fileType),
		fmt.Sprintf("%s/.rotini.%s.json", dir, fileType),
		fmt.Sprintf("%s/.rotini.%s.jsonc", dir, fileType),
	}, nil
}

/*
 * Processor functionality:
 * Loader
 * 1. compile schema spec
 * 2. compile schema conf
 * 3. get the end-users spec file bytes
 *   a. first by path passed in param
 *   b. next by looking at the fallback paths (if any of the fallback paths resolve to a spec, use it -- if none do, err)
 * 4. get the end-users conf file bytes -- first by path passed in param, then by looking at the fallback paths (if any of the fallback paths resolve to a conf, use it -- if none do, err)
 *   a. first by path passed in param
 *   b. next by looking at the fallback paths (if any of the fallback paths resolve to a conf, use it -- if none do, do not err, go to next)
 *   c. finally, fallback to a "default conf" shape
 * Validator
 * 1. ensures end-user spec file satisfies schema spec
 * 2. ensures end-user conf file satisfies schema conf
 * 3. ensures "rotini-specific rules" for end-user spec file all okay -- no dupe command names/aliases, etc -- whatever won't be "caught" by the jsonschema check
 * 4. ensures "rotini-specific rules" for end-user conf file all okay -- whatever won't be "caught" by the jsonschema check
 * Generator
 * 1. generator for help
 * 2. generator for man
 * 3. generator for markdown
 * 4. generator for completion
 * 5. generator for rotini codegen
 * Initializer
 * 1. scaffolds based on a "recipe" type
 *   a. "flat" = go.mod, go.sum, main.go, commands handler files, codegen files all in main package
 *   b. "cmd"
 */
