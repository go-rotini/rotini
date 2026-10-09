package rotini

import (
	"cmp"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// expandGlobs replaces each word of the leaf's glob arguments ([ArgDef.Glob]) with the paths it
// matches, when goos ("" for the running system) is Windows, whose shells pass patterns
// through. It runs once every word is in, so a variadic argument followed by fixed ones knows
// which words are its own.
func (p *parsedInputs) expandGlobs(args []ArgDef, idx int, goos string) {
	if cmp.Or(goos, runtime.GOOS) != "windows" || !hasGlobArg(args) {
		return
	}
	si := &p.scopes[idx]
	spans := argSpans(args, len(si.args))
	out := make([]string, 0, len(si.args))
	next := 0
	for i, s := range spans {
		if !args[i].Glob || s[0] >= s[1] {
			continue
		}
		out = append(out, si.args[next:s[0]]...)
		for _, word := range si.args[s[0]:s[1]] {
			out = append(out, expandGlob(p.dir, word)...)
		}
		next = s[1]
	}
	si.args = append(out, si.args[next:]...)
}

func hasGlobArg(args []ArgDef) bool {
	for _, a := range args {
		if a.Glob {
			return true
		}
	}
	return false
}

// expandGlob expands one word as a Windows user expects a shell to: a word with `*`, `?` or `[`
// becomes the sorted paths it matches, relative to dir (the run's directory, "" for the
// process's) as the word was. A word naming an existing path, "-", and a pattern matching
// nothing are kept as written, so the path check reports a missing match as it would on a
// POSIX shell.
func expandGlob(dir, word string) []string {
	if word == "-" || !strings.ContainsAny(word, "*?[") {
		return []string{word}
	}
	if _, err := os.Lstat(joinDir(dir, word)); err == nil {
		return []string{word}
	}
	pattern := joinDir(dir, word)
	matches, err := filepath.Glob(pattern)
	if err != nil || len(matches) == 0 {
		return []string{word}
	}
	if pattern != word {
		for i, m := range matches {
			if rel, err := filepath.Rel(dir, m); err == nil {
				matches[i] = rel
			}
		}
	}
	return matches
}
