// Package testmod gives the modules that tests build from generated code the dependencies the
// rotini module itself builds with.
//
// Go reads replace directives only in the main module. A test module that requires rotini from
// the working tree therefore builds against the versions rotini's go.mod requires, not the
// local trees its replace directives point at. Copying those directives into the test module
// makes a working tree that replaces its dependencies exercise them end to end.
package testmod

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// A Replace is one replace directive: Old, and optionally its version, replaced by New, a
// module path and version or a local directory.
type Replace struct {
	Old, New string
}

// Replaces returns the replace directives of the go.mod in repoRoot, with each local directory
// made absolute. It returns none when the file can't be read.
func Replaces(repoRoot string) []Replace {
	data, err := os.ReadFile(filepath.Join(repoRoot, "go.mod"))
	if err != nil {
		return nil
	}
	var out []Replace
	inBlock := false
	for line := range strings.SplitSeq(string(data), "\n") {
		if i := strings.Index(line, "//"); i >= 0 {
			line = line[:i]
		}
		line = strings.TrimSpace(line)
		switch {
		case inBlock && line == ")":
			inBlock = false
			continue
		case line == "replace (":
			inBlock = true
			continue
		case !inBlock:
			rest, ok := strings.CutPrefix(line, "replace ")
			if !ok {
				continue
			}
			line = rest
		}
		old, repl, ok := strings.Cut(line, "=>")
		if !ok {
			continue
		}
		old, repl = strings.Join(strings.Fields(old), " "), strings.TrimSpace(repl)
		if fields := strings.Fields(repl); len(fields) == 1 && isLocal(fields[0]) {
			repl = fields[0]
			if !filepath.IsAbs(repl) {
				repl = filepath.Join(repoRoot, repl)
			}
			repl = filepath.ToSlash(repl)
		} else {
			repl = strings.Join(fields, " ")
		}
		out = append(out, Replace{Old: old, New: repl})
	}
	return out
}

// isLocal reports whether a replacement is a directory rather than a module path.
func isLocal(p string) bool {
	return filepath.IsAbs(p) || strings.HasPrefix(p, "./") || strings.HasPrefix(p, "../") ||
		strings.HasPrefix(p, `.\`) || strings.HasPrefix(p, `..\`) || p == "." || p == ".."
}

// Directives returns repoRoot's replace directives as go.mod lines, each ending in a newline,
// for a test module's go.mod.
func Directives(repoRoot string) string {
	var b strings.Builder
	for _, r := range Replaces(repoRoot) {
		fmt.Fprintf(&b, "replace %s => %s\n", r.Old, r.New)
	}
	return b.String()
}

// EditFlags returns repoRoot's replace directives as `go mod edit` flags.
func EditFlags(repoRoot string) []string {
	replaces := Replaces(repoRoot)
	flags := make([]string, 0, len(replaces))
	for _, r := range replaces {
		flags = append(flags, "-replace="+strings.ReplaceAll(r.Old, " ", "@")+"="+strings.ReplaceAll(r.New, " ", "@"))
	}
	return flags
}
