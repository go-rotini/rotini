// Package testmod gives the modules that tests build from generated code the dependencies the
// rotini module itself builds with.
//
// Go reads replace directives only in the main module. A test module that requires rotini from
// the working tree therefore builds against the versions rotini's go.mod requires, not the
// local trees its replace directives point at. Copying those directives into the test module
// makes a working tree that replaces its dependencies exercise them end to end.
package testmod

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
)

// A Replace is one replace directive: the module Old, at OldVersion or at every version when
// that is empty, replaced by the module New at NewVersion, or by the local directory New when
// NewVersion is empty.
type Replace struct {
	Old, OldVersion, New, NewVersion string
}

// Replaces returns the replace directives of the go.mod in repoRoot, as the go command reads
// them, with each local directory made absolute. It returns none when the file can't be read.
func Replaces(repoRoot string) []Replace {
	if _, err := os.Stat(filepath.Join(repoRoot, "go.mod")); err != nil {
		return nil
	}
	cmd := exec.Command("go", "mod", "edit", "-json")
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=")
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	var mod struct {
		Replace []struct {
			Old struct {
				Path    string `json:"Path"`
				Version string `json:"Version"`
			} `json:"Old"`
			New struct {
				Path    string `json:"Path"`
				Version string `json:"Version"`
			} `json:"New"`
		} `json:"Replace"`
	}
	if err := json.Unmarshal(out, &mod); err != nil {
		return nil
	}
	var replaces []Replace
	for _, r := range mod.Replace {
		dir := r.New.Path
		if r.New.Version == "" {
			if !filepath.IsAbs(dir) {
				dir = filepath.Join(repoRoot, dir)
			}
			dir = filepath.ToSlash(dir)
		}
		replaces = append(replaces, Replace{Old: r.Old.Path, OldVersion: r.Old.Version, New: dir, NewVersion: r.New.Version})
	}
	return replaces
}

// Directives returns repoRoot's replace directives as go.mod lines, each ending in a newline,
// for a test module's go.mod.
func Directives(repoRoot string) string {
	var b strings.Builder
	for _, r := range Replaces(repoRoot) {
		fields := []string{"replace", Quote(r.Old)}
		if r.OldVersion != "" {
			fields = append(fields, r.OldVersion)
		}
		fields = append(fields, "=>", Quote(r.New))
		if r.NewVersion != "" {
			fields = append(fields, r.NewVersion)
		}
		b.WriteString(strings.Join(fields, " "))
		b.WriteByte('\n')
	}
	return b.String()
}

// EditFlags returns repoRoot's replace directives as `go mod edit` flags.
func EditFlags(repoRoot string) []string {
	replaces := Replaces(repoRoot)
	flags := make([]string, 0, len(replaces))
	for _, r := range replaces {
		old, repl := r.Old, r.New
		if r.OldVersion != "" {
			old += "@" + r.OldVersion
		}
		if r.NewVersion != "" {
			repl += "@" + r.NewVersion
		}
		flags = append(flags, "-replace="+old+"="+repl)
	}
	return flags
}

// Quote returns s as a go.mod token: quoted when it holds a space, a quote or another character
// go.mod would read as syntax, as it is otherwise.
func Quote(s string) string {
	if s == "" || strings.Contains(s, "//") || strings.Contains(s, "/*") {
		return strconv.Quote(s)
	}
	for _, r := range s {
		switch {
		case strings.ContainsRune(" \"'`", r), len(s) > 1 && strings.ContainsRune("()[]{},", r), !unicode.IsPrint(r):
			return strconv.Quote(s)
		}
	}
	return s
}
