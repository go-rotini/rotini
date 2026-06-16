package internal

// This file maps JSON-pointer instance locations back to line:column positions
// in the ORIGINAL source bytes, so a validation problem can name the place the
// user actually typed — `.rotini.spec.yaml:12:7` — instead of only the pointer.
// Each format delegates to its codec's RFC-6901 locator: yaml, toml, and jsonc
// all expose PathPointer + Path.ReadPositions over a parsed AST whose nodes
// carry source positions. JSON is served by the jsonc parser (JSON ⊂ JSONC).
// Parsing is lazy and cached per document — locators are only consulted on the
// failure path.

import (
	"sync"

	"github.com/go-rotini/jsonc"
	"github.com/go-rotini/toml"
	"github.com/go-rotini/yaml"
)

// sourceLocator resolves a JSON-pointer instance location ("/command/inputs/
// flags/0/schema") to a 1-based line:column in the original source. ok is
// false when the pointer cannot be resolved (or the document fails to parse);
// callers then degrade to pointer-only reporting.
type sourceLocator func(pointer string) (line, col int, ok bool)

// newSourceLocator builds the locator for one read document. It is cheap to
// build — parsing happens lazily, at most once, on the first lookup — because
// locators are only consulted on the failure path. A nil return means the
// format is unrecognized.
func newSourceLocator(format fileFormat, data []byte) sourceLocator {
	switch format {
	case formatYAML:
		return yamlLocator(data)
	case formatJSON, formatJSONC:
		// JSON is a subset of JSONC, so one parser serves both.
		return jsoncLocator(data)
	case formatTOML:
		return tomlLocator(data)
	default:
		return nil
	}
}

// yamlLocator resolves a pointer via yaml.PathPointer over the parsed document.
func yamlLocator(data []byte) sourceLocator {
	parse := sync.OnceValues(func() (*yaml.File, error) { return yaml.Parse(data) })
	return func(pointer string) (int, int, bool) {
		f, err := parse()
		if err != nil || len(f.Docs) == 0 {
			return 0, 0, false
		}
		p, err := yaml.PathPointer(pointer)
		if err != nil {
			return 0, 0, false
		}
		positions, err := p.ReadPositions(f.Docs[0])
		if err != nil || len(positions) == 0 {
			return 0, 0, false
		}
		return positions[0].Line, positions[0].Column, true
	}
}

// jsoncLocator resolves a pointer via jsonc.PathPointer; it serves both JSON
// and JSONC documents.
func jsoncLocator(data []byte) sourceLocator {
	parse := sync.OnceValues(func() (*jsonc.File, error) { return jsonc.Parse(data) })
	return func(pointer string) (int, int, bool) {
		f, err := parse()
		if err != nil || f.Root == nil {
			return 0, 0, false
		}
		p, err := jsonc.PathPointer(pointer)
		if err != nil {
			return 0, 0, false
		}
		positions := p.ReadPositions(f.Root)
		if len(positions) == 0 {
			return 0, 0, false
		}
		return positions[0].Line, positions[0].Column, true
	}
}

// tomlLocator resolves a pointer via toml.PathPointer. (TOML positions are now
// supported — previously these problems degraded to pointer-only.)
func tomlLocator(data []byte) sourceLocator {
	parse := sync.OnceValues(func() (*toml.File, error) { return toml.Parse(data) })
	return func(pointer string) (int, int, bool) {
		f, err := parse()
		if err != nil || f.Root == nil {
			return 0, 0, false
		}
		p, err := toml.PathPointer(pointer)
		if err != nil {
			return 0, 0, false
		}
		positions, err := p.ReadPositions(f.Root)
		if err != nil || len(positions) == 0 {
			return 0, 0, false
		}
		return positions[0].Line, positions[0].Column, true
	}
}
