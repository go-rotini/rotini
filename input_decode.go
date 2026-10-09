package rotini

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/go-rotini/dotenv"
	"github.com/go-rotini/jsonc"
	"github.com/go-rotini/jsonschema"
	"github.com/go-rotini/recon"
	"github.com/go-rotini/toml"
	"github.com/go-rotini/yaml"
)

// utf8BOM is the UTF-8 byte-order mark some editors write at the start of a file.
const utf8BOM = "\xef\xbb\xbf"

// stripBOM removes one leading UTF-8 byte-order mark.
func stripBOM(b []byte) []byte { return bytes.TrimPrefix(b, []byte(utf8BOM)) }

// decodeFailure is a document that did not decode: where the decoder stopped, when it says,
// and why, in its own words without its "<format>: line N, column M:" prefix.
type decodeFailure struct {
	line, col int
	msg       string
}

// at renders the position as "L:C", or "" when the decoder gave none.
func (f decodeFailure) at() string {
	if f.line <= 0 {
		return ""
	}
	return strconv.Itoa(f.line) + ":" + strconv.Itoa(f.col)
}

// describeDecodeError reads a decode error from one of the bundled formats. data is the
// document that failed, for decoders that report a byte offset instead of a line.
func describeDecodeError(err error, data []byte) decodeFailure {
	if e, ok := errors.AsType[*yaml.SyntaxError](err); ok {
		return decodeFailure{e.Pos.Line, e.Pos.Column, e.Message}
	}
	if e, ok := errors.AsType[*toml.SyntaxError](err); ok {
		return decodeFailure{e.Pos.Line, e.Pos.Column, e.Message}
	}
	if e, ok := errors.AsType[*jsonc.SyntaxError](err); ok {
		return decodeFailure{e.Pos.Line, e.Pos.Column, e.Message}
	}
	if e, ok := errors.AsType[*dotenv.ParseError](err); ok {
		return decodeFailure{e.Pos.Line, e.Pos.Column, e.Message}
	}
	if e, ok := errors.AsType[*json.SyntaxError](err); ok {
		line, col := offsetPosition(data, e.Offset)
		return decodeFailure{line, col, e.Error()}
	}
	if e, ok := errors.AsType[*json.UnmarshalTypeError](err); ok {
		line, col := offsetPosition(data, e.Offset)
		return decodeFailure{line, col, e.Error()}
	}
	if errors.Is(err, recon.ErrUnsupportedFormat) {
		return decodeFailure{msg: "the top level must be a mapping"}
	}
	for {
		next := errors.Unwrap(err)
		if next == nil {
			return decodeFailure{msg: err.Error()}
		}
		err = next
	}
}

// offsetPosition converts a byte offset into data to a 1-based line and column. Only "\n"
// ends a line, so CRLF input counts the same.
func offsetPosition(data []byte, offset int64) (line, col int) {
	if offset > int64(len(data)) {
		offset = int64(len(data))
	}
	if offset < 0 {
		offset = 0
	}
	head := data[:offset]
	line = bytes.Count(head, []byte("\n")) + 1
	col = int(offset) - (bytes.LastIndexByte(head, '\n') + 1) + 1
	return line, col
}

// documentCodec decodes a configuration file the way recon's codec does, after dropping one
// leading byte-order mark, and reports a failure with its position (see [docError]).
type documentCodec struct{ recon.Codec }

func (c documentCodec) Decode(data []byte) (map[string]any, error) {
	data = stripBOM(data)
	m, err := c.Codec.Decode(data)
	if err != nil {
		return nil, &docError{failure: describeDecodeError(err, data), cause: err}
	}
	return m, nil
}

// docError is a decode failure with its position, from [documentCodec].
type docError struct {
	failure decodeFailure
	cause   error
}

func (e *docError) Error() string { return e.failure.msg }
func (e *docError) Unwrap() error { return e.cause }

// fileCodec is the codec a configuration file decodes with: the declared format, else the one
// its extension names, wrapped in [documentCodec]. ok is false when neither names a codec.
func fileCodec(format, path string) (recon.Codec, bool) {
	codecs := recon.DefaultCodecs()
	var c recon.Codec
	var ok bool
	if format != "" {
		c, ok = codecs.ByName(format)
	} else {
		c, ok = codecs.ByExtension(strings.ToLower(filepath.Ext(path)))
	}
	if !ok {
		return nil, false
	}
	return documentCodec{c}, true
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
