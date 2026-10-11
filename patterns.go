package rotini

import (
	"fmt"
	"regexp"
	"sync"
)

// Compiled patterns, keyed by their text, so each compiles once per process. The text never
// changes for a given key, so the cache is safe across runs and goroutines.
var patternRegexps sync.Map // pattern → *regexp.Regexp

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
