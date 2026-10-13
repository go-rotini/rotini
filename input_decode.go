package rotini

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/go-rotini/jsonschema"
	"github.com/go-rotini/recon"
)

// utf8BOM is the UTF-8 byte-order mark some editors write at the start of a file.
const utf8BOM = "\xef\xbb\xbf"

// stripBOM removes one leading UTF-8 byte-order mark.
func stripBOM(b []byte) []byte { return bytes.TrimPrefix(b, []byte(utf8BOM)) }

// decodeMessage says why a document did not decode, in the decoder's words without its
// "<format>: line N, column M:" prefix: the decoder's message for the bundled formats, the
// mapping rule for a top level that isn't one, else the innermost error's text.
func decodeMessage(err error) string { return decodeMessageWith(err, nil) }

// jsonDecodeMessage is [decodeMessage] for an error from encoding/json.
func jsonDecodeMessage(err error) string { return decodeMessageWith(err, jsonMessage) }

// decodeMessageWith is [decodeMessage], reading a decoder's own error types through more.
func decodeMessageWith(err error, more func(error) (string, bool)) string {
	if errors.Is(err, recon.ErrUnsupportedFormat) {
		return "the top level must be " + topLevelKind(err.Error())
	}
	if msg := parseMessage(err, more); msg != err.Error() {
		return msg
	}
	for next := errors.Unwrap(err); next != nil; next = errors.Unwrap(err) {
		err = next
	}
	return err.Error()
}

// topLevelKind is what a format's codec says its top level must be, in the format's own word
// ("an object" for JSON, "a table" for TOML), read from the codec's "<format> root must be
// <kind>, got <type>" message; "a mapping" when the message has no such phrase.
func topLevelKind(msg string) string {
	_, kind, ok := strings.Cut(msg, " root must be ")
	if !ok {
		return "a mapping"
	}
	kind, _, _ = strings.Cut(kind, ",")
	if kind = strings.TrimSpace(kind); kind == "" {
		return "a mapping"
	}
	return kind
}

// fileParseError reports a configuration file rotini decoded itself the way recon reports one
// it read.
func fileParseError(path string, err error) *recon.ParseError {
	pe := &recon.ParseError{Source: path, Path: path, Msg: parseMessage(err, nil), Cause: err}
	if line, col, ok := parsePosition(err); ok {
		pe.Position = recon.Position{Line: line, Column: col}
	}
	return pe
}

// decodeDocumentJSON decodes a stdin document of any shape — a top-level list included — with
// format's codec c and returns it as JSON, for a payload whose type is not an object. toml and
// dotenv documents are always tables, so they cannot hold one; nor can a format with no codec.
func decodeDocumentJSON(format string, c *codecEntry, data []byte) ([]byte, error) {
	if c == nil || c.decodeAny == nil {
		return nil, fmt.Errorf("%w: a %s document's root must be a table, got a list", recon.ErrUnsupportedFormat, format)
	}
	v, err := c.decodeAny(data)
	if err != nil {
		return nil, err
	}
	if raw, ok := v.(json.RawMessage); ok {
		return raw, nil
	}
	raw, err := json.Marshal(v)
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
	compiled, err := compileSchema(schema)
	if err != nil {
		return nil, internalBind(channelStdin, "", "invalid stdin schema", err)
	}
	return compiled, nil
}

// documentResult reports a failed stdin schema check as recon validation errors, so it reads
// like every other stdin field error.
func documentResult(schema string, result *jsonschema.Result) error {
	if result.Valid {
		return nil
	}
	multi := schemaFailures(result.Errors)
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
