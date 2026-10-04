package codegen

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Orphan pruning keeps regeneration idempotent: generated files (stubs, feature pages) that no
// longer correspond to anything in the spec are removed. A file rotini did not write is never
// removed.

// pruneStubs removes handler stubs in the cmd package that no longer correspond to an own
// command. The generated file, keep-listed files, test files, a co-located main.go and a
// co-located models file are protected. Only files stubLooksGenerated accepts are candidates.
func pruneStubs(gp *program, lay layout, keepList []string, onPrune func(string)) error {
	protected := map[string]bool{lay.cmdFile: true}
	if lay.entrypointDir == lay.cmdDir && lay.entrypointFile != "" {
		protected[lay.entrypointFile] = true
	}
	if lay.splitModels && lay.modelsDir == lay.cmdDir {
		protected[lay.modelsFile] = true
	}
	// A stub still under its legacy dashed name is the command's handler (see
	// dashedStubFilename).
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

// pruneGoDir removes the orphaned generated stubs in dir: non-test .go files that are not
// protected and that stubLooksGenerated identifies as rotini-written. Sub-directories,
// *_test.go files and hand-written files are never touched. Every removal is reported through
// onPrune.
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
			continue
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

// stubMarker is the line every generated handler stub carries (templates/handler.go.tmpl). It
// identifies a file as rotini-written; deleting the line claims the file for the author.
const stubMarker = "var _ rotini.Handler = ("

// legacyStubMarker is stubMarker as written before v1.2.0, when the interface was Handlers.
// Recognizing it keeps those stubs prunable after an upgrade.
const legacyStubMarker = "var _ rotini.Handlers = ("

// stubLooksGenerated reports whether path is a handler stub rotini wrote. A missing file
// reports false.
func stubLooksGenerated(path string) (bool, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("read %s to decide whether it is a generated stub: %w", path, err)
	}
	return strings.Contains(string(body), stubMarker) || strings.Contains(string(body), legacyStubMarker), nil
}

// owns reports whether a file in the feature's embed_dir is one this feature writes, and so
// one pruning may remove.
//
// Most features own files with their prefix and extension (help_*.txt). The man feature owns
// <page-name>.<section> files for the root page or its sub-pages with a single-digit section
// (taskr.1, taskr-add.8), which also catches pages left by a section change, plus the
// pre-v1.2.0 man_<root>*.txt files.
func (o featureOutput) owns(name string) bool {
	if o.desc.manPages {
		if len(o.nodes) == 0 {
			return false
		}
		root := o.nodes[0].data.PageName
		lower := strings.ToLower(name)
		if strings.HasPrefix(lower, "man_"+root) && strings.HasSuffix(lower, ".txt") {
			return true
		}
		base, section, ok := strings.Cut(name, ".")
		if !ok || len(section) != 1 || section[0] < '1' || section[0] > '9' {
			return false
		}
		return base == root || strings.HasPrefix(base, root+"-")
	}
	if !strings.HasSuffix(name, o.desc.ext) || strings.HasSuffix(name, "_test"+o.desc.ext) {
		return false
	}
	return o.desc.filePrefix == "" || strings.HasPrefix(name, o.desc.filePrefix)
}

// pruneFeatureOutputs removes each enabled feature's orphaned pages. Only files the feature
// owns are candidates, so features sharing an embed_dir never prune each other's files.
// Keep-listed paths are preserved, and the editable template lives in template_dir, outside
// the scan.
func pruneFeatureOutputs(lay layout, keepList []string, outputs []featureOutput) error {
	keep := make(map[string]bool, len(keepList))
	for _, k := range keepList {
		keep[filepath.ToSlash(k)] = true
	}
	for _, o := range outputs {
		// Current pages are protected only in embed mode: an inline feature writes no
		// pages, so any on disk are stale.
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
			if e.IsDir() || !o.owns(name) {
				continue
			}
			if protected[name] {
				continue
			}
			// keep entries are relative to the cmd package.
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
