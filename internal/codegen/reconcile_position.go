package codegen

// Mapping JSON pointers to line:column positions in the original source. Each format uses
// its codec's RFC 6901 pointer over a positioned AST; JSON uses the jsonc parser. Parsing is
// lazy and cached per document, since locators are consulted only when problems exist.

import (
	"sync"

	"github.com/go-rotini/jsonc"
	"github.com/go-rotini/toml"
	"github.com/go-rotini/yaml"
)

// sourceLocator resolves a JSON pointer ("/command/flags/0/schema") to a 1-based line and
// column in the original source. ok is false when the pointer does not resolve or the
// document does not parse.
type sourceLocator func(pointer string) (line, col int, ok bool)

// newSourceLocator builds the locator for one document, or nil for an unrecognized format.
func newSourceLocator(format fileFormat, data []byte) sourceLocator {
	switch format {
	case formatYAML:
		return yamlLocator(data)
	case formatJSON, formatJSONC:
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

// jsoncLocator resolves a pointer via jsonc.PathPointer for JSON and JSONC documents.
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

// tomlLocator resolves a pointer via toml.PathPointer.
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
