package internal

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-rotini/jsonc"
	"github.com/go-rotini/toml"
	"github.com/go-rotini/yaml"
)

// ErrUnsupportedExtension is returned by [LoadSpec] / [LoadConf] when the
// supplied path's extension isn't one of the supported spec/conf formats.
var ErrUnsupportedExtension = errors.New("unsupported file extension")

// Spec DSL types mirror schema-spec.json exactly: field names match the
// JSON keys, and the loader strictly enforces "no unknown fields" so
// typos surface immediately rather than silently no-op-ing.
//
// The DSL is broader than [rtk.ProgramSpec] — it carries declarations
// (schemas, events, metadata, remote_commands) that codegen consumes but
// the runtime parser doesn't see. M2.4's toProgramSpec translates the
// runtime-facing subset into the rtk shape; the rest is held here for
// codegen to read.

// Spec is the in-memory representation of a .rotini.spec.{yaml,json,toml,jsonc}
// file. The on-disk format has the root command's fields promoted to the
// top level — Inputs / Commands / etc. apply to the root.
type Spec struct {
	// SchemaURL is the value of the `$schema` key — the URL identifying
	// the rotini spec schema version the file targets.
	SchemaURL string `json:"$schema,omitempty"`

	// Name is the root command name (used in routing and binary name).
	Name string `json:"name"`

	// Aliases lists alternative names for the root command.
	Aliases []string `json:"aliases,omitempty"`

	// Timeout is the root command execution timeout (Go duration string).
	Timeout string `json:"timeout,omitempty"`

	// Inputs are the root command's typed inputs.
	Inputs *Inputs `json:"inputs,omitempty"`

	// Commands is the nested sub-command tree.
	Commands []Command `json:"commands,omitempty"`

	// RemoteCommands declares co-located remote binaries dispatched as
	// first-class sub-commands of the root. Carried through verbatim;
	// codegen consumes it.
	RemoteCommands []RemoteCommandSpec `json:"remote_commands,omitempty"`

	// Files declares config files to load at startup. Each entry becomes
	// a recon-backed config source the framework auto-binds.
	Files []ConfigSpec `json:"files,omitempty"`

	// Events declares the named events the program can emit.
	Events []EventSpec `json:"events,omitempty"`

	// Metadata declares ldflag-injected build-time variables.
	Metadata []MetadataEntry `json:"metadata,omitempty"`

	// Schemas is the top-level reusable schema map. Referenced elsewhere
	// via `$ref: #/schemas/<Name>`.
	Schemas map[string]*FieldSchema `json:"schemas,omitempty"`
}

// Command defines a node in the CLI command tree.
type Command struct {
	Name           string              `json:"name"`
	Aliases        []string            `json:"aliases,omitempty"`
	Timeout        string              `json:"timeout,omitempty"`
	Inputs         *Inputs             `json:"inputs,omitempty"`
	Commands       []Command           `json:"commands,omitempty"`
	RemoteCommands []RemoteCommandSpec `json:"remote_commands,omitempty"`
}

// Inputs holds the typed sub-arrays of command inputs.
type Inputs struct {
	Flags     []Parameter `json:"flags,omitempty"`
	Arguments []Parameter `json:"arguments,omitempty"`
	Files     []Parameter `json:"files,omitempty"`
	Variables []Parameter `json:"variables,omitempty"`
	Stdin     *StdinSpec  `json:"stdin,omitempty"`
}

// Parameter is the unified input definition used across flags, arguments,
// env vars, and config inputs. Metadata fields (required, default, etc.)
// live inside the [FieldSchema] attached as Schema.
type Parameter struct {
	// Name is the logical name (used as the Go field name after
	// PascalCase conversion).
	Name string `json:"name"`

	// Schema describes the parameter's type and validation constraints
	// plus input-level metadata (required, default, variable, file, key).
	Schema *FieldSchema `json:"schema,omitempty"`

	// Identifiers are the CLI flag identifiers (e.g., "--force", "-f").
	// Flag-specific; ignored for non-flag inputs. When absent on a flag,
	// the generator auto-derives "--<name>".
	Identifiers []string `json:"identifiers,omitempty"`
}

// StdinSpec declares the expected stdin shape for a command's inputs.
//
// Format is inferred from Schema.Type: "string" → text, structured →
// json. Whether stdin is required is declared via Schema.Required.
type StdinSpec struct {
	Schema *FieldSchema `json:"schema,omitempty"`
}

// FieldSchema is the unified schema type used for input types (flags,
// arguments, files, variables, stdin) and for the top-level reusable
// `schemas:` map. The `required` field is encoded as [json.RawMessage]
// because the schema uses it two ways:
//
//   - on input-level uses (parameters, stdin), it is a bool;
//   - on object-level uses (top-level schemas), it is a []string of
//     required property names.
//
// Use [FieldSchema.RequiredBool] / [FieldSchema.RequiredFields] to read
// it as the right shape.
type FieldSchema struct {
	// Ref is the optional JSON Pointer-style reference to a named schema
	// in the top-level `schemas:` map (e.g., "#/schemas/MyType").
	Ref string `json:"$ref,omitempty"`

	// Type is the Go-flavored type name ("string", "int", "bool", "[]string",
	// "map[string]int", "duration", etc.) or a JSON-Schema-flavored name
	// ("boolean", "integer", "number", "array", "object") — both are accepted.
	Type string `json:"type,omitempty"`

	// Nullable, when true, generates the Go field as *T instead of T.
	Nullable bool `json:"nullable,omitempty"`

	// Description is the long-form documentation surfaced in help output.
	Description string `json:"description,omitempty"`

	// Enum, when non-empty, restricts allowed values.
	Enum []string `json:"enum,omitempty"`

	// Pattern is the regex string values must match (string types only).
	Pattern string `json:"pattern,omitempty"`

	// Minimum / Maximum constrain numeric values inclusively.
	Minimum *float64 `json:"minimum,omitempty"`
	Maximum *float64 `json:"maximum,omitempty"`

	// MinLength / MaxLength constrain string length inclusively.
	MinLength *int `json:"minLength,omitempty"`
	MaxLength *int `json:"maxLength,omitempty"`

	// MinItems / MaxItems constrain slice/map cardinality inclusively.
	MinItems *int `json:"minItems,omitempty"`
	MaxItems *int `json:"maxItems,omitempty"`

	// Required has dual semantics — see godoc on the parent type.
	Required json.RawMessage `json:"required,omitempty"`

	// Properties is the nested-schema map for object types.
	Properties map[string]*FieldSchema `json:"properties,omitempty"`

	// Items is the element schema for array types.
	Items *FieldSchema `json:"items,omitempty"`

	// Input-level metadata (used when this schema is attached to a
	// parameter or stdin spec).

	// Default is the value used when no source supplies the input.
	// Encoded as a raw JSON value because YAML/JSON authors may write
	// strings, numbers, booleans, or even objects.
	Default json.RawMessage `json:"default,omitempty"`

	// Variable is the env-var name (environment inputs only).
	Variable string `json:"variable,omitempty"`

	// File is the config-file logical name (config_values inputs only).
	File string `json:"file,omitempty"`

	// Key is the config-file key path (config_values inputs only,
	// e.g., "server.port").
	Key string `json:"key,omitempty"`
}

// RequiredBool returns Required as a bool. Returns true only when
// Required is literal JSON `true`.
func (s *FieldSchema) RequiredBool() bool {
	if s == nil || len(s.Required) == 0 {
		return false
	}
	var b bool
	if err := json.Unmarshal(s.Required, &b); err != nil {
		return false
	}
	return b
}

// RequiredFields returns Required as a []string of property names.
// Returns nil when Required is not a JSON array.
func (s *FieldSchema) RequiredFields() []string {
	if s == nil || len(s.Required) == 0 {
		return nil
	}
	var fields []string
	if err := json.Unmarshal(s.Required, &fields); err != nil {
		return nil
	}
	return fields
}

// RemoteCommandSpec declares a co-located remote binary dispatched as a
// first-class command. The binary is invoked as `<program>-<name>`.
type RemoteCommandSpec struct {
	Name    string   `json:"name"`
	Aliases []string `json:"aliases,omitempty"`
	Timeout string   `json:"timeout,omitempty"`
}

// ConfigSpec declares a config file to load at startup.
type ConfigSpec struct {
	Name   string       `json:"name"`
	Path   string       `json:"path"`
	Format string       `json:"format,omitempty"` // "json" (default), "yaml", "toml"
	Schema *FieldSchema `json:"schema,omitempty"`
}

// EventSpec declares a single named event a command can emit.
type EventSpec struct {
	Name   string       `json:"name"`
	Schema *FieldSchema `json:"schema,omitempty"`
}

// MetadataEntry declares a build-time variable injected via go ldflags.
type MetadataEntry struct {
	Var     string `json:"var"`
	Default string `json:"default,omitempty"`
}

// Conf DSL types describe the codegen configuration that lives outside
// the spec — under .rotini.conf.{yaml,json,toml,jsonc}.

// Conf is the in-memory representation of a .rotini.conf.{yaml,json,...}
// file. It holds the codegen configuration that lives outside the spec.
type Conf struct {
	// SchemaURL is the value of the `$schema` key.
	SchemaURL string `json:"$schema,omitempty"`

	// Generate carries the codegen configuration.
	Generate GenerateConfig `json:"generate"`
}

// GenerateConfig groups codegen options for the bridge ("cmd") package
// and the framework package.
type GenerateConfig struct {
	Cmd       GenerateCmdConfig       `json:"cmd"`
	Framework GenerateFrameworkConfig `json:"framework"`
}

// GenerateCmdConfig holds codegen options for the user-facing command
// handler package (the one that holds the user's handler implementations
// plus the auto-generated bridge file).
type GenerateCmdConfig struct {
	Package string                 `json:"package,omitempty"`
	GenFile string                 `json:"gen_file,omitempty"`
	Prune   GenerateCmdPruneConfig `json:"prune"`
}

// GenerateCmdPruneConfig holds the pruning configuration for stale
// handler files (see [Prune] in M2.8).
type GenerateCmdPruneConfig struct {
	Enabled bool     `json:"enabled,omitempty"`
	Keep    []string `json:"keep,omitempty"`
}

// GenerateFrameworkConfig holds codegen options for the framework
// package (the one holding the auto-generated *.gen.go files plus any
// additional imports the user wants in the generated code).
type GenerateFrameworkConfig struct {
	Package           string   `json:"package,omitempty"`
	GenFile           string   `json:"gen_file,omitempty"`
	AdditionalImports []string `json:"additional_imports,omitempty"`
}

// ApplyConfDefaults populates zero-valued GenerateConfig fields with the
// rotini-default values. Call after [LoadConf] so downstream codegen
// always sees a fully-populated configuration.
func ApplyConfDefaults(c *Conf) {
	if c == nil {
		return
	}
	if c.Generate.Cmd.Package == "" {
		c.Generate.Cmd.Package = "internal/cli/cmd"
	}
	if c.Generate.Cmd.GenFile == "" {
		c.Generate.Cmd.GenFile = "handlers.gen.go"
	}
	if c.Generate.Framework.Package == "" {
		c.Generate.Framework.Package = "internal/cli/rotini"
	}
	if c.Generate.Framework.GenFile == "" {
		c.Generate.Framework.GenFile = "rotini.gen.go"
	}
}

// LoadSpec reads, parses, and decodes a .rotini.spec.{yaml,json,toml,jsonc}
// file. Format is detected by the path's extension. Unknown fields fail
// decoding so spec typos surface immediately.
func LoadSpec(path string) (*Spec, error) {
	jsonBytes, err := readAsJSON(path)
	if err != nil {
		return nil, fmt.Errorf("internal: load spec %s: %w", path, err)
	}
	var s Spec
	dec := json.NewDecoder(bytes.NewReader(jsonBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&s); err != nil {
		return nil, fmt.Errorf("internal: decode spec %s: %w", path, err)
	}
	return &s, nil
}

// LoadConf reads, parses, and decodes a .rotini.conf.{yaml,json,toml,jsonc}
// file. Same format-detection + strict-decoding rules as [LoadSpec].
func LoadConf(path string) (*Conf, error) {
	jsonBytes, err := readAsJSON(path)
	if err != nil {
		return nil, fmt.Errorf("internal: load conf %s: %w", path, err)
	}
	var c Conf
	dec := json.NewDecoder(bytes.NewReader(jsonBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("internal: decode conf %s: %w", path, err)
	}
	return &c, nil
}

// readAsJSON reads the file at path, parses it according to its extension,
// and re-emits the parsed value as JSON bytes. This intermediate-JSON
// strategy lets downstream code use [json.Decoder.DisallowUnknownFields]
// uniformly across formats — without it, YAML/TOML decoders silently
// accept unknown fields.
//
// Supported extensions:
//
//	.yaml, .yml   → go-rotini/yaml
//	.json         → encoding/json
//	.jsonc        → go-rotini/jsonc (tolerates comments / trailing commas)
//	.toml         → go-rotini/toml
//
// For .json files the loader content-sniffs: if the file contains "//"
// or "/*" outside string literals, it falls back to the jsonc parser.
// This is forgiving for hand-written spec files where authors sometimes
// add explanatory comments.
func readAsJSON(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}

	ext := strings.ToLower(filepath.Ext(path))
	var raw any
	switch ext {
	case ".yaml", ".yml":
		if err := yaml.Unmarshal(data, &raw); err != nil {
			return nil, fmt.Errorf("parse yaml: %w", err)
		}
	case ".toml":
		if err := toml.Unmarshal(data, &raw); err != nil {
			return nil, fmt.Errorf("parse toml: %w", err)
		}
	case ".jsonc":
		if err := jsonc.Unmarshal(data, &raw); err != nil {
			return nil, fmt.Errorf("parse jsonc: %w", err)
		}
	case ".json":
		if looksLikeJSONC(data) {
			if err := jsonc.Unmarshal(data, &raw); err != nil {
				return nil, fmt.Errorf("parse json (jsonc fallback): %w", err)
			}
		} else if err := json.Unmarshal(data, &raw); err != nil {
			return nil, fmt.Errorf("parse json: %w", err)
		}
	default:
		return nil, fmt.Errorf("%w: %q (want .yaml, .yml, .json, .jsonc, or .toml)", ErrUnsupportedExtension, ext)
	}

	jsonBytes, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("normalize to JSON: %w", err)
	}
	return jsonBytes, nil
}

// looksLikeJSONC reports whether data contains JSONC-only constructs
// (line comments, block comments) outside of string literals. It is a
// best-effort sniffer used only when the file extension is `.json`; a
// false negative just falls through to the stricter stdlib JSON parser.
func looksLikeJSONC(data []byte) bool {
	inString := false
	escaped := false
	for i := range data {
		c := data[i]
		if escaped {
			escaped = false
			continue
		}
		if inString {
			switch c {
			case '\\':
				escaped = true
			case '"':
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '/':
			if i+1 < len(data) && (data[i+1] == '/' || data[i+1] == '*') {
				return true
			}
		}
	}
	return false
}
