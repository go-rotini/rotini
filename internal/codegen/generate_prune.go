package codegen

import (
	"fmt"
	"go/build/constraint"
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
	return pruneGoDir(gp.plan, lay.cmdDir, protected, onPrune)
}

// pruneEntrypoint removes orphaned .go files in the entrypoint directory, honoring its `keep`
// list; main.go itself is create-once and always protected. It is a no-op when no entrypoint
// is declared, or when it shares the cmd package directory, which pruneStubs already covers.
func pruneEntrypoint(pl *planner, lay layout, keepList []string, onPrune func(string)) error {
	if lay.entrypointDir == "" || lay.entrypointDir == lay.cmdDir {
		return nil
	}
	protected := map[string]bool{lay.entrypointFile: true}
	for _, k := range keepList {
		protected[filepath.ToSlash(k)] = true
	}
	return pruneGoDir(pl, lay.entrypointDir, protected, onPrune)
}

// pruneGoDir retires the orphaned generated stubs in dir: non-test .go files that are not
// protected and that stubLooksGenerated identifies as rotini-written. It takes two generates:
// the first disables an orphan (see disableStub), and the next deletes the stub it disabled.
// go generate lists every package's files before running any directive, so deleting a file
// another package's directive is about to be handed would fail the run; a build-ignored file is
// not on that list. Sub-directories, *_test.go files and hand-written files are never touched.
// Every step is reported through onPrune. A directory that doesn't exist yet, as in a dry run
// before anything is written, has nothing to prune.
func pruneGoDir(pl *planner, dir string, protected map[string]bool, onPrune func(string)) error {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
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
		body, _, _, err := pl.read(path)
		if err != nil {
			return err
		}
		if strings.HasPrefix(string(body), "//go:build ignore\n") && !stubDisabled(body) {
			continue // out of the build without rotini's note: the author's to keep
		}
		if stubDisabled(body) {
			if err := pl.remove(path); err != nil {
				return fmt.Errorf("prune %s: %w", name, err)
			}
			if onPrune != nil {
				onPrune(name)
			}
			continue
		}
		if err := pl.write(path, disableStub(body)); err != nil {
			return fmt.Errorf("prune %s: %w", name, err)
		}
		if onPrune != nil {
			onPrune(name + " (disabled; removed by the next generate)")
		}
	}
	return nil
}

// disabledHeader is what disableStub puts at the top of an orphaned stub. The next generate
// recognizes a stub it disabled by these lines, so the text is part of the format.
const disabledHeader = "//go:build ignore\n\n" + disabledNote + "\n// removed by the next generate. To keep the command, add it back to the spec and delete\n// these lines.\n\n"

// disabledNote is the line that marks a stub rotini disabled.
const disabledNote = "// rotini: this handler's command is no longer in the spec, so the file is disabled and is"

// disableStub returns an orphaned stub taken out of the build: rotini's header, with
// //go:build ignore, above the author's code. A build constraint the author added is
// replaced, since a file can carry only one.
func disableStub(body []byte) []byte {
	lines := strings.SplitAfter(string(body), "\n")
	var kept []string
	inHeader := true
	for _, l := range lines {
		trimmed := strings.TrimSpace(l)
		if strings.HasPrefix(trimmed, "package ") {
			inHeader = false
		}
		if inHeader && (strings.HasPrefix(trimmed, "//go:build") || strings.HasPrefix(trimmed, "// +build")) {
			continue
		}
		kept = append(kept, l)
	}
	return []byte(disabledHeader + strings.TrimLeft(strings.Join(kept, ""), "\n"))
}

// enableStub undoes disableStub: the author's code, back in the build. An author's own build
// constraint, which disabling replaced, is not restored.
func enableStub(body []byte) []byte {
	return []byte(strings.TrimPrefix(string(body), disabledHeader))
}

// stubDisabled reports whether body is a stub rotini disabled: build-ignored, with rotini's
// note directly below. An author who removed the note, or turned the build back on, has a
// live file again.
func stubDisabled(body []byte) bool {
	return strings.HasPrefix(string(body), "//go:build ignore\n\n"+disabledNote)
}

// buildIgnored reports whether src is kept out of every build by a //go:build constraint
// that needs the ignore tag, wherever it sits among the comments above the package clause.
func buildIgnored(src []byte) bool {
	for line := range strings.SplitSeq(string(src), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "package ") {
			return false
		}
		if !constraint.IsGoBuild(line) {
			continue
		}
		expr, err := constraint.Parse(line)
		if err != nil {
			return false
		}
		// Without the ignore tag it can't build, whatever else is set: no build includes it.
		allOn := expr.Eval(func(tag string) bool { return tag != "ignore" })
		allOff := expr.Eval(func(string) bool { return false })
		return !allOn && !allOff
	}
	return false
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
func pruneFeatureOutputs(pl *planner, lay layout, keepList []string, outputs []featureOutput) error {
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
			if err := pl.remove(filepath.Join(o.absEmbedDir, name)); err != nil {
				return fmt.Errorf("prune %s: %w", rel, err)
			}
		}
	}
	return nil
}
