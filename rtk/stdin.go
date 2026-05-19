package rtk

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/go-rotini/jsonc"
	"github.com/go-rotini/toml"
	"github.com/go-rotini/yaml"
)

// readStdinBytes reads everything from r and returns the bytes. A nil reader
// yields nil bytes (no error). Read errors yield nil bytes (the parser
// treats stdin as best-effort; a downstream consumer that requires stdin
// should explicitly check).
//
// Read happens at most once per Parse call — the parser caches the result
// and passes it to anyone who needs it (`-` argv substitution + StdinSpec
// shaping).
func readStdinBytes(r io.Reader) []byte {
	if r == nil {
		return nil
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return nil
	}
	return data
}

// shapeStdin converts cached stdin bytes into the typed value
// [Result.Stdin] exposes per the active command's [StdinSpec.Format].
//
// Behavior by format:
//
//	"" or no spec → returns nil, nil. The handler sees Result.Stdin == nil.
//	"text"        → trim trailing CR/LF, return string.
//	"raw"         → return the bytes verbatim.
//	"json"        → json.Unmarshal into map[string]any.
//	"yaml"        → go-rotini/yaml.Unmarshal into map[string]any.
//	"toml"        → go-rotini/toml.Unmarshal into map[string]any.
//	"jsonc"       → go-rotini/jsonc.Unmarshal into map[string]any.
//
// Empty stdin (nil or len 0) is shape-skipped — Result.Stdin stays nil
// regardless of format. This matches the rotiniold behavior and avoids
// surprising "shape an empty payload into an empty map" semantics.
//
// Malformed structured input returns a wrapped error; the caller surfaces
// it as a parse failure.
func shapeStdin(raw []byte, spec *StdinSpec) (any, error) {
	if spec == nil || len(raw) == 0 {
		return nil, nil //nolint:nilnil // legitimate "no value" return; the parser checks the spec.
	}
	switch spec.Format {
	case "text":
		return strings.TrimRight(string(raw), "\r\n"), nil
	case "raw":
		return raw, nil
	case "json":
		var v map[string]any
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, fmt.Errorf("rtk: stdin (json): %w", err)
		}
		return v, nil
	case "yaml":
		var v map[string]any
		if err := yaml.Unmarshal(raw, &v); err != nil {
			return nil, fmt.Errorf("rtk: stdin (yaml): %w", err)
		}
		return v, nil
	case "toml":
		var v map[string]any
		if err := toml.Unmarshal(raw, &v); err != nil {
			return nil, fmt.Errorf("rtk: stdin (toml): %w", err)
		}
		return v, nil
	case "jsonc":
		var v map[string]any
		if err := jsonc.Unmarshal(raw, &v); err != nil {
			return nil, fmt.Errorf("rtk: stdin (jsonc): %w", err)
		}
		return v, nil
	default:
		return nil, fmt.Errorf("rtk: unknown stdin format %q (expected one of: text, raw, json, yaml, toml, jsonc)", spec.Format)
	}
}
