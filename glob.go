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
// process's) as the word was. `[[]`, `[*]` and `[?]` stand for the character itself: a word
// with no other metacharacter is that literal text, unexpanded, so ArgvOf's escaped values read
// back exactly. "-" and a word naming an existing path are kept as written, and a pattern
// matching nothing comes back with its escapes removed, so the path check reports a missing
// match as it would on a POSIX shell.
func expandGlob(dir, word string) []string {
	if word == "-" || !strings.ContainsAny(word, "*?[") {
		return []string{word}
	}
	literal, isPattern := unescapeGlob(word)
	if !isPattern {
		return []string{literal}
	}
	if _, err := os.Lstat(joinDir(dir, word)); err == nil {
		return []string{word}
	}
	pattern := joinDir(dir, word)
	matches, err := globFold(pattern)
	if err != nil || len(matches) == 0 {
		return []string{literal}
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

// globFold is [filepath.Glob] matching names regardless of case, as Windows file systems and
// tools do: `*.TXT` matches a.txt.
func globFold(pattern string) ([]string, error) {
	if _, err := filepath.Match(pattern, ""); err != nil {
		return nil, err //nolint:wrapcheck // the caller keeps the word as written
	}
	if !strings.ContainsAny(pattern, "*?[") {
		if _, err := os.Lstat(pattern); err != nil {
			return nil, nil //nolint:nilerr // a word naming no file matches nothing, as filepath.Glob reads it
		}
		return []string{pattern}, nil
	}
	dir, file := filepath.Split(pattern)
	dir = cleanGlobDir(dir)
	if !strings.ContainsAny(dir[len(filepath.VolumeName(dir)):], "*?[") {
		return globDirFold(dir, file, nil), nil
	}
	if dir == pattern {
		return nil, filepath.ErrBadPattern
	}
	parents, err := globFold(dir)
	if err != nil {
		return nil, err
	}
	var matches []string
	for _, d := range parents {
		matches = globDirFold(d, file, matches)
	}
	return matches, nil
}

// cleanGlobDir is the directory part of a split pattern without its trailing separator, as
// [filepath.Glob] reads it: "" is ".", a root or volume root is kept, and a bare volume "C:"
// is "C:.".
func cleanGlobDir(dir string) string {
	vol := len(filepath.VolumeName(dir))
	switch {
	case dir == "":
		return "."
	case vol+1 == len(dir) && os.IsPathSeparator(dir[len(dir)-1]):
		return dir
	case vol == len(dir):
		return dir + "."
	}
	return dir[:len(dir)-1]
}

// globDirFold appends to matches the entries of dir whose names match pattern regardless of
// case, in name order.
func globDirFold(dir, pattern string, matches []string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return matches
	}
	folded := strings.ToLower(pattern)
	for _, e := range entries {
		if ok, err := filepath.Match(folded, strings.ToLower(e.Name())); err == nil && ok {
			matches = append(matches, filepath.Join(dir, e.Name()))
		}
	}
	return matches
}

// globEscaper writes each glob metacharacter as a one-character class, which matches it
// literally on every system (a backslash is a separator on Windows, not an escape).
var globEscaper = strings.NewReplacer("[", "[[]", "*", "[*]", "?", "[?]")

// unescapeGlob removes the escapes globEscaper writes, and reports whether word holds any
// unescaped `*`, `?` or `[`.
func unescapeGlob(word string) (literal string, isPattern bool) {
	var b strings.Builder
	b.Grow(len(word))
	for i := 0; i < len(word); i++ {
		c := word[i]
		if c == '[' && i+2 < len(word) && word[i+2] == ']' && strings.IndexByte("[*?", word[i+1]) >= 0 {
			b.WriteByte(word[i+1])
			i += 2
			continue
		}
		if c == '*' || c == '?' || c == '[' {
			isPattern = true
		}
		b.WriteByte(c)
	}
	return b.String(), isPattern
}
