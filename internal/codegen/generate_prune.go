package codegen

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Emit-time orphan pruning that keeps regeneration idempotent: drop generated files
// (stubs, feature pages) that no longer correspond to anything in the spec.

// pruneStubs removes handler .go files in the cmd package that no longer correspond
// to an own command, preserving the generated cli file, the keep list, and any test
// files. The cmd package is flat, so keepList entries (package-relative) are just file
// names for its top-level stubs. When the entrypoint shares the cmd package, its
// create-once main.go is protected too (otherwise it would be pruned as an orphan).
func pruneStubs(gp *program, lay layout, keepList []string) error {
	protected := map[string]bool{
		gp.root.filename: true,
		lay.cmdFile:      true,
	}
	if lay.entrypointDir == lay.cmdDir && lay.entrypointFile != "" {
		protected[lay.entrypointFile] = true
	}
	for _, c := range gp.own {
		protected[c.filename] = true
	}
	for _, k := range keepList {
		protected[filepath.ToSlash(k)] = true
	}
	return pruneGoDir(lay.cmdDir, protected)
}

// pruneEntrypoint removes orphaned .go files in the entrypoint directory, so a
// `keep` list on the main package is honored (the main.go itself is create-once
// and always protected; test files are kept automatically). It is a no-op when no
// entrypoint is declared, or when the entrypoint shares the cmd package directory —
// pruneStubs already covers that dir (and is passed the merged keep list).
func pruneEntrypoint(lay layout, keepList []string) error {
	if lay.entrypointDir == "" || lay.entrypointDir == lay.cmdDir {
		return nil
	}
	protected := map[string]bool{lay.entrypointFile: true}
	for _, k := range keepList {
		protected[filepath.ToSlash(k)] = true
	}
	return pruneGoDir(lay.entrypointDir, protected)
}

// pruneGoDir removes every non-test .go file in dir whose base name is not in the
// protected set. Sub-directories and *_test.go files are never touched.
func pruneGoDir(dir string, protected map[string]bool) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("read dir %s: %w", dir, err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		if protected[name] {
			continue
		}
		if err := os.Remove(filepath.Join(dir, name)); err != nil {
			return fmt.Errorf("prune %s: %w", name, err)
		}
	}
	return nil
}

// pruneFeatureOutputs removes orphaned rotini-managed outputs in each enabled feature's
// dir (under the cmd package) — the per-command pages for commands no longer
// in the spec. Only files matching the feature's unique suffix AND prefix are
// candidates, so features sharing one embed dir never prune each other's files.
// The editable per-feature template, test files, and any keep-listed
// (package-relative) path are preserved. Top-level cmd package files (the gen file)
// are never auto-removed. keepList entries are package-relative to the cli
// package.
func pruneFeatureOutputs(lay layout, keepList []string, outputs []featureOutput) error {
	keep := make(map[string]bool, len(keepList))
	for _, k := range keepList {
		keep[filepath.ToSlash(k)] = true
	}
	for _, o := range outputs {
		// The editable template is always protected (it is the author's content,
		// never pruned even when template:false leaves it inert). The current
		// command set's output pages are protected only in embed mode — an inline
		// feature writes none, so any on-disk pages are stale and get pruned.
		// Pruning scans the embed_dir for stale OUTPUT files. The editable
		// template lives in template_dir (a different tree) and is rotini's only
		// managed file there, so it is never a prune candidate. The current
		// command set's output files are protected only in embed mode — inline
		// features write none, so any on-disk pages are stale and get pruned.
		protected := map[string]bool{}
		if o.embed {
			for _, n := range o.nodes {
				protected[n.file] = true
			}
		}

		entries, err := os.ReadDir(o.absEmbedDir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return fmt.Errorf("read %s embed_dir %s: %w", o.desc.name, o.absEmbedDir, err)
		}
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || !strings.HasSuffix(name, o.desc.ext) || strings.HasSuffix(name, "_test"+o.desc.ext) {
				continue
			}
			if o.desc.filePrefix != "" && !strings.HasPrefix(name, o.desc.filePrefix) {
				continue
			}
			if protected[name] {
				continue
			}
			// keep entries are package-relative (to the cmd package).
			rel := name
			if r, err := filepath.Rel(lay.cmdDir, filepath.Join(o.absEmbedDir, name)); err == nil {
				rel = filepath.ToSlash(r)
			}
			if keep[rel] {
				continue
			}
			if err := os.Remove(filepath.Join(o.absEmbedDir, name)); err != nil {
				return fmt.Errorf("prune %s: %w", rel, err)
			}
		}
	}
	return nil
}
