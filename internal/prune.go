package internal

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/go-rotini/fs"
	"github.com/go-rotini/rotini/rtk"
)

// PruneOptions configures a [Prune] invocation.
type PruneOptions struct {
	// SpecPath is the path to the .rotini.spec.{yaml,json,toml,jsonc}
	// file that defines the "current" command set. Required.
	SpecPath string

	// HandlersDir is the directory containing the user's per-command
	// handler files. Per the scaffold convention, this is
	// `internal/handlers` relative to the module root. The path is
	// resolved as-is (absolute or working-dir-relative) — caller
	// responsibility to resolve module roots.
	HandlersDir string

	// Keep is an additional safelist of filenames (basename only, not
	// full paths) that should never be deleted, regardless of whether
	// they correspond to a command in the spec. Useful for
	// hand-written helpers the user wants to retain.
	Keep []string

	// DryRun, when true, reports which files would be deleted without
	// actually removing them. The returned [PruneResult.FilesDeleted]
	// still lists the planned-deletion paths.
	DryRun bool
}

// PruneResult reports the outcome of a [Prune] invocation.
type PruneResult struct {
	// FilesDeleted lists every absolute path Prune removed (or would
	// remove, when DryRun is true). Deterministic order.
	FilesDeleted []string

	// FilesKept lists every file in HandlersDir that matches the
	// rotini-handler-filename convention but was retained because it
	// corresponds to a command currently declared in the spec, or
	// because its basename appears in [PruneOptions.Keep].
	// Deterministic order.
	FilesKept []string
}

// Prune deletes handler files in opts.HandlersDir that no longer
// correspond to any command in opts.SpecPath. Files outside the
// rotini-handler-filename convention (any `*.go` file at the top
// level of the directory) are left untouched. Files whose basename
// appears in opts.Keep are also retained.
//
// Pruning is idempotent: a missing handler file is silently treated
// as "nothing to delete." A missing handlers directory returns an
// empty [PruneResult] without error, since "there are no files to
// prune" is a valid steady state for a fresh project.
func Prune(opts PruneOptions) (*PruneResult, error) {
	if opts.SpecPath == "" {
		return nil, ErrMissingSpecPath
	}

	spec, err := LoadSpec(opts.SpecPath)
	if err != nil {
		return nil, fmt.Errorf("internal: load spec: %w", err)
	}
	if err := Validate(spec); err != nil {
		return nil, fmt.Errorf("internal: validate spec: %w", err)
	}

	// Resolve the "expected" set of handler filenames from the spec.
	expected := expectedHandlerFilenames(ToProgramSpec(spec))
	keep := make(map[string]bool, len(opts.Keep)+len(expected))
	for _, f := range opts.Keep {
		keep[f] = true
	}
	for f := range expected {
		keep[f] = true
	}

	// Scan the directory. Missing directory → empty result.
	dir := opts.HandlersDir
	if dir == "" {
		return nil, ErrMissingHandlersDir
	}
	if !fs.Exists(dir) {
		return &PruneResult{}, nil
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("internal: read handlers dir %s: %w", dir, err)
	}

	result := &PruneResult{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".go") {
			continue
		}
		if !looksLikeHandlerFile(name) {
			// Not a rotini handler file — leave alone.
			continue
		}
		fullPath := filepath.Join(dir, name)
		if keep[name] {
			result.FilesKept = append(result.FilesKept, fullPath)
			continue
		}
		if opts.DryRun {
			result.FilesDeleted = append(result.FilesDeleted, fullPath)
			continue
		}
		if err := fs.Remove(fullPath); err != nil {
			return nil, fmt.Errorf("internal: remove %s: %w", fullPath, err)
		}
		result.FilesDeleted = append(result.FilesDeleted, fullPath)
	}

	sort.Strings(result.FilesDeleted)
	sort.Strings(result.FilesKept)
	return result, nil
}

// ErrMissingHandlersDir is returned by [Prune] when
// [PruneOptions.HandlersDir] is empty.
var ErrMissingHandlersDir = errors.New("internal: HandlersDir is required")

// expectedHandlerFilenames returns the set of handler filenames the
// spec currently expects: one per command (root included), named per
// the rotini-handler-filename convention.
//
// Convention: every handler file is named
// "<program>_<segments>.go", with the program name prefixed and
// every command-path segment joined by underscores. The root command's
// file is just "<program>.go".
//
// Examples for a program named "rotini":
//
//   - root command           → "rotini.go"
//   - "generate"             → "rotini_generate.go"
//   - "foo bar baz"          → "rotini_foo_bar_baz.go"
//
// This is the rotiniold convention, kept because it (a) prefixes the
// program name into every filename so handlers are self-identifying,
// and (b) makes sibling commands with the same leaf name (e.g.,
// `cmd1 sub` vs `cmd2 sub`) end up in distinct files
// (`prog_cmd1_sub.go` vs `prog_cmd2_sub.go`).
//
// Hyphens in either the program name or any path segment are
// translated to underscores so the result is a valid Go file name
// idiomatically.
func expectedHandlerFilenames(ps rtk.ProgramSpec) map[string]bool {
	out := map[string]bool{
		HandlerFilename(ps.Name, ""): true,
	}
	var walk func(cmds []rtk.CommandSpec)
	walk = func(cmds []rtk.CommandSpec) {
		for i := range cmds {
			c := &cmds[i]
			out[HandlerFilename(ps.Name, c.Path)] = true
			walk(c.Commands)
		}
	}
	walk(ps.Commands)
	return out
}

// HandlerFilename derives the handler file basename for a command
// path. Exported so codegen (the handler-skel template) and tools
// surrounding the scaffold can compute expected names without
// re-implementing the convention.
//
// Examples (program "rotini"):
//
//   - HandlerFilename("rotini", "")             → "rotini.go"
//   - HandlerFilename("rotini", "generate")     → "rotini_generate.go"
//   - HandlerFilename("rotini", "foo-bar-baz")  → "rotini_foo_bar_baz.go"
//   - HandlerFilename("my-cli", "add")          → "my_cli_add.go"
//
// programName must be non-empty; an empty name falls back to "root"
// so legacy callers (and tests) keep producing a syntactically valid
// filename, but production callers always supply a name from
// [rtk.ProgramSpec.Name].
func HandlerFilename(programName, commandPath string) string {
	prog := strings.ReplaceAll(programName, "-", "_")
	if prog == "" {
		prog = "root"
	}
	if commandPath == "" {
		return prog + ".go"
	}
	return prog + "_" + strings.ReplaceAll(commandPath, "-", "_") + ".go"
}

// looksLikeHandlerFile reports whether a basename matches the rotini
// handler-filename convention closely enough to be a prune candidate.
// The check is loose on purpose: any `<identifier>.go` file under
// HandlersDir is potentially a prune target. Files that don't fit the
// pattern (e.g., helpers like `internal_helpers.go.bak`, hidden files
// like `.gitkeep`) are left untouched.
//
// "Looks like" means: starts with a letter, only contains
// [a-zA-Z0-9_], ends in ".go". Hyphens are NOT permitted — handler
// filenames always use underscores per [HandlerFilename]; if a user
// hand-authors a `foo-bar.go`, prune leaves it alone (it isn't
// mistakable for a generated handler).
func looksLikeHandlerFile(basename string) bool {
	if !strings.HasSuffix(basename, ".go") {
		return false
	}
	stem := strings.TrimSuffix(basename, ".go")
	if stem == "" {
		return false
	}
	if !isIdentRune(rune(stem[0]), true) {
		return false
	}
	for _, r := range stem[1:] {
		if !isIdentRune(r, false) {
			return false
		}
	}
	return true
}

// isIdentRune reports whether r is permissible in a Go identifier
// (the first rune must be a letter or underscore; subsequent runes
// may also be digits).
func isIdentRune(r rune, first bool) bool {
	switch {
	case r >= 'a' && r <= 'z':
		return true
	case r >= 'A' && r <= 'Z':
		return true
	case r == '_':
		return true
	case !first && r >= '0' && r <= '9':
		return true
	}
	return false
}
