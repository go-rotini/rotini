package codegen

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Emit-time orphan pruning that keeps regeneration idempotent: drop generated files
// (stubs, feature pages) that no longer correspond to anything in the spec.

// pruneStubs removes handler .go files in the cmd package that no longer correspond to an own
// command, preserving the generated cli file, the keep list, and any test files. When the
// entrypoint shares the cmd package, its create-once main.go is protected too.
//
// Only files rotini itself could have WRITTEN are candidates — see stubLooksGenerated. A
// hand-written helper in the same package is never touched, whatever it is called.
func pruneStubs(gp *program, lay layout, keepList []string, onPrune func(string)) error {
	protected := map[string]bool{lay.cmdFile: true}
	if lay.entrypointDir == lay.cmdDir && lay.entrypointFile != "" {
		protected[lay.entrypointFile] = true
	}
	// A models file sharing this directory is generated, not an orphan — without
	// this it is written and then immediately pruned.
	if lay.splitModels && lay.modelsDir == lay.cmdDir {
		protected[lay.modelsFile] = true
	}
	// A stub still under its old dashed name is the command's handler too, never an orphan:
	// pruning it would delete the user's code (see dashedStubFilename).
	for _, c := range gp.ownCommands() {
		protected[c.filename] = true
		if c.dashedFilename != "" {
			protected[c.dashedFilename] = true
		}
	}
	for _, k := range keepList {
		protected[filepath.ToSlash(k)] = true
	}
	return pruneGoDir(lay.cmdDir, protected, onPrune)
}

// pruneEntrypoint removes orphaned .go files in the entrypoint directory, honoring its `keep`
// list; main.go itself is create-once and always protected. It is a no-op when no entrypoint
// is declared, or when it shares the cmd package directory, which pruneStubs already covers.
func pruneEntrypoint(lay layout, keepList []string, onPrune func(string)) error {
	if lay.entrypointDir == "" || lay.entrypointDir == lay.cmdDir {
		return nil
	}
	protected := map[string]bool{lay.entrypointFile: true}
	for _, k := range keepList {
		protected[filepath.ToSlash(k)] = true
	}
	return pruneGoDir(lay.entrypointDir, protected, onPrune)
}

// pruneGoDir removes the ORPHANED GENERATED STUBS in dir: non-test .go files that are not
// protected AND that rotini itself wrote. Sub-directories and *_test.go files are never
// touched, and neither is anything a human wrote.
//
// The "rotini wrote it" test is the point. Pruning used to remove every unprotected .go file,
// so a helper placed beside the handlers that use it — the obvious home for a shared
// registry key — was deleted by the next `go generate`, silently, with the build failure as
// the first sign anything had happened. `keep:` was the remedy, and its own description says
// it is "intended to stay empty".
//
// Every pruned file is reported through onPrune. Removing a file is not a silent operation.
func pruneGoDir(dir string, protected map[string]bool, onPrune func(string)) error {
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
		path := filepath.Join(dir, name)
		generated, err := stubLooksGenerated(path)
		if err != nil {
			return err
		}
		if !generated {
			continue // a hand-written file: not rotini's to remove
		}
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("prune %s: %w", name, err)
		}
		if onPrune != nil {
			onPrune(name)
		}
	}
	return nil
}

// stubMarker is the line every generated handler stub is written with (see
// templates/handler.go.tmpl). Its presence is what identifies a file as one rotini created,
// and an author who deletes the line has said the file is theirs.
const stubMarker = "var _ rotini.Handlers = ("

// stubLooksGenerated reports whether path is a handler stub rotini wrote. A file that cannot
// be read is treated as NOT generated: the safe answer when deleting is "leave it".
func stubLooksGenerated(path string) (bool, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("read %s to decide whether it is a generated stub: %w", path, err)
	}
	return strings.Contains(string(body), stubMarker), nil
}

// pruneFeatureOutputs removes each enabled feature's orphaned pages — those for commands no
// longer in the spec. Only files matching the feature's unique prefix and suffix are
// candidates, so features sharing one embed dir never prune each other's files. The editable
// template, test files, keep-listed paths and top-level cmd package files are preserved.
func pruneFeatureOutputs(lay layout, keepList []string, outputs []featureOutput) error {
	keep := make(map[string]bool, len(keepList))
	for _, k := range keepList {
		keep[filepath.ToSlash(k)] = true
	}
	for _, o := range outputs {
		// Pruning scans the embed_dir for stale output files. The editable template
		// lives in template_dir, so it is never a candidate. The current command set's
		// pages are protected only in embed mode: an inline feature writes none, so any
		// on disk are stale.
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
