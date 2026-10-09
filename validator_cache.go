package rotini

import (
	"fmt"
	"regexp"
	"sync"

	"github.com/go-rotini/recon"
)

// Compiled schemas and patterns, keyed by their text, so each compiles once per process. The
// text never changes for a given key, so the caches are safe across runs and goroutines.
var (
	schemaValidators sync.Map // schema text → *recon.JSONSchemaValidator
	patternRegexps   sync.Map // pattern → *regexp.Regexp
)

// schemaValidator returns the compiled validator for a JSON Schema.
func schemaValidator(schema string) (*recon.JSONSchemaValidator, error) {
	if cached, ok := schemaValidators.Load(schema); ok {
		if v, ok := cached.(*recon.JSONSchemaValidator); ok {
			return v, nil
		}
	}
	v, err := recon.NewJSONSchemaValidator([]byte(schema))
	if err != nil {
		return nil, fmt.Errorf("compile schema: %w", err)
	}
	schemaValidators.Store(schema, v)
	return v, nil
}

// compiledPattern returns the compiled regular expression for a declared pattern.
func compiledPattern(pattern string) (*regexp.Regexp, error) {
	if cached, ok := patternRegexps.Load(pattern); ok {
		if re, ok := cached.(*regexp.Regexp); ok {
			return re, nil
		}
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("compile pattern: %w", err)
	}
	patternRegexps.Store(pattern, re)
	return re, nil
}
