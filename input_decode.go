package rotini

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"github.com/go-rotini/jsonc"
	"github.com/go-rotini/jsonschema"
	"github.com/go-rotini/recon"
	"github.com/go-rotini/yaml"
)

// utf8BOM is the UTF-8 byte-order mark some editors write at the start of a file.
const utf8BOM = "\xef\xbb\xbf"

// stripBOM removes one leading UTF-8 byte-order mark.
func stripBOM(b []byte) []byte { return bytes.TrimPrefix(b, []byte(utf8BOM)) }

// decodeMessage says why a document did not decode, in the decoder's words without its
// "<format>: line N, column M:" prefix: recon's message for the bundled formats, the mapping
// rule for a top level that isn't one, else the innermost error's text.
func decodeMessage(err error) string {
	if errors.Is(err, recon.ErrUnsupportedFormat) {
		return "the top level must be a mapping"
	}
	if msg := recon.ParseMessage(err); msg != err.Error() {
		return msg
	}
	for next := errors.Unwrap(err); next != nil; next = errors.Unwrap(err) {
		err = next
	}
	return err.Error()
}

// fileParseError reports a configuration file rotini decoded itself the way recon reports one
// it read: data is the file's content, for decoders that give a byte offset instead of a line.
func fileParseError(path string, data []byte, err error) *recon.ParseError {
	pe := &recon.ParseError{Source: path, Path: path, Msg: recon.ParseMessage(err), Cause: err}
	if line, col, ok := recon.ParsePosition(err, data); ok {
		pe.Position = recon.Position{Line: line, Column: col}
	}
	return pe
}

// fileCodec is the codec a configuration file decodes with: the declared format, else the one
// its extension names. ok is false when neither names a codec.
func fileCodec(format, path string) (recon.Codec, bool) {
	codecs := recon.DefaultCodecs()
	if format != "" {
		return codecs.ByName(format)
	}
	return codecs.ByExtension(strings.ToLower(filepath.Ext(path)))
}

// decodeDocumentJSON decodes a stdin document of any shape — a top-level list included — and
// returns it as JSON, for a payload whose type is not an object. toml and dotenv documents
// are always tables, so they cannot hold one.
func decodeDocumentJSON(format string, data []byte) ([]byte, error) {
	var v any
	switch format {
	case "json":
		if !json.Valid(data) {
			var probe any
			return nil, fmt.Errorf("decode json: %w", json.Unmarshal(data, &probe))
		}
		return bytes.TrimSpace(data), nil
	case "yaml":
		if err := yaml.Unmarshal(data, &v); err != nil {
			return nil, fmt.Errorf("decode yaml: %w", err)
		}
	case "jsonc":
		if err := jsonc.Unmarshal(data, &v); err != nil {
			return nil, fmt.Errorf("decode jsonc: %w", err)
		}
	default:
		return nil, fmt.Errorf("%w: a %s document is a table at the top level, not a list", recon.ErrUnsupportedFormat, format)
	}
	raw, err := json.Marshal(jsonCompatible(v))
	if err != nil {
		return nil, fmt.Errorf("encode the document as JSON: %w", err)
	}
	return raw, nil
}

// jsonCompatible converts decoded YAML (whose maps may have non-string keys) to values
// encoding/json can write.
func jsonCompatible(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, e := range t {
			t[k] = jsonCompatible(e)
		}
		return t
	case map[any]any:
		out := make(map[string]any, len(t))
		for k, e := range t {
			out[fmt.Sprint(k)] = jsonCompatible(e)
		}
		return out
	case []any:
		for i, e := range t {
			t[i] = jsonCompatible(e)
		}
		return t
	}
	return v
}

// documentSchemas caches each stdin document schema, compiled, by its text.
var documentSchemas sync.Map // string → *jsonschema.Schema

// validateDocumentJSON checks a JSON document of any shape against a stdin schema, reporting
// failures as recon validation errors so they read like every other stdin field error.
func validateDocumentJSON(schema string, raw []byte) error {
	compiled, err := documentSchema(schema)
	if err != nil {
		return err
	}
	result, err := compiled.Validate(raw)
	if err != nil {
		return usageBind(channelStdin, "", "could not check stdin against its schema", err)
	}
	return documentResult(schema, result)
}

// documentSchema returns a stdin schema, compiled once per process.
func documentSchema(schema string) (*jsonschema.Schema, error) {
	cached, ok := documentSchemas.Load(schema)
	if !ok {
		s, err := jsonschema.Compile([]byte(schema))
		if err != nil {
			return nil, internalBind(channelStdin, "", "invalid stdin schema", err)
		}
		cached, _ = documentSchemas.LoadOrStore(schema, s)
	}
	compiled, ok := cached.(*jsonschema.Schema)
	if !ok {
		return nil, internalBind(channelStdin, "", "invalid stdin schema", nil)
	}
	return compiled, nil
}

// documentResult reports a failed stdin schema check as recon validation errors, so it reads
// like every other stdin field error.
func documentResult(schema string, result *jsonschema.Result) error {
	if result.Valid {
		return nil
	}
	multi := &recon.MultiError{}
	var walk func(errs []jsonschema.ValidationError)
	walk = func(errs []jsonschema.ValidationError) {
		for _, e := range errs {
			if len(e.Causes) > 0 {
				walk(e.Causes)
				continue
			}
			multi.Append(&recon.ValidationError{Path: pointerPath(e.InstanceLocation), Rule: e.Keyword, Msg: e.Message})
		}
	}
	walk(result.Errors)
	applyPatternMessages(schema, multi)
	return reconBind(channelStdin, multi)
}

// pointerPath converts a JSON Pointer ("/0/name") to a recon path.
func pointerPath(pointer string) recon.Path {
	pointer = strings.TrimPrefix(pointer, "/")
	if pointer == "" {
		return recon.Path{}
	}
	segs := strings.Split(pointer, "/")
	for i, s := range segs {
		segs[i] = strings.ReplaceAll(strings.ReplaceAll(s, "~1", "/"), "~0", "~")
	}
	return recon.Path(segs)
}
