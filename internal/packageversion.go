package internal

// Package arm of composition version-compatibility (W9/D-W9.4): the rotini TOOL that
// generates must be the same major as the rotini LIBRARY the consuming module builds
// against (its go.mod require), so the emitted code and the runtime it calls agree on the
// contract. Go's MVS already resolves one library version per build; this catches the one
// thing MVS can't — a `go install`ed tool from a different major than the project's library.
// The check is best-effort and conservative: it fires only on a clear cross-major mismatch,
// and skips whenever it can't prove one (dev/unknown tool version, a local `replace`, no
// rotini require, an unparseable version).

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	rotini "github.com/go-rotini/rotini/internal/runtime"
)

// rotiniModulePath is the import path of the rotini module — the require/replace the
// package-arm check looks for in the consuming module's go.mod.
const rotiniModulePath = "github.com/go-rotini/rotini"

// checkPackageVersion compares the generating tool's version against the rotini library
// the module at moduleRoot requires, erroring (a package-arm [rotini.CompositionVersionError])
// only on a definite cross-major mismatch. It returns nil whenever compatibility can't be
// disproven — see the skip conditions in moduleRotiniVersion and below.
func checkPackageVersion(moduleRoot, toolVersion string) error {
	want := strings.TrimPrefix(toolVersion, "v")
	if want == "" {
		return nil // tool version unknown (dev build) — nothing to compare against
	}
	libVersion, replaced := moduleRotiniVersion(moduleRoot)
	if replaced || libVersion == "" {
		return nil // a local replace (dev) or no rotini require — no meaningful library version
	}
	got := strings.TrimPrefix(libVersion, "v")
	if majorVersion(got) == "" || sameMajorVersion(want, got) {
		return nil
	}
	msg := fmt.Sprintf("rotini tool is %s but this module builds against rotini library %s — the generated code targets a different major than the runtime it calls; align the `rotini` tool and the go.mod require (same major)", want, got)
	return &rotini.CompositionVersionError{
		Arm: rotini.CompositionPackageArm, Subject: rotiniModulePath, Want: want, Got: got, Msg: msg,
	}
}

// moduleRotiniVersion reads moduleRoot/go.mod and reports the version the rotini module is
// required at and whether it is locally replaced. It hand-scans (no modfile dependency,
// matching findModule's lightweight read), handling both single-line and block require/
// replace forms; a missing rotini require (or an unreadable go.mod — best-effort, since
// findModule already read it) yields ("", false), which the caller treats as "skip".
func moduleRotiniVersion(moduleRoot string) (version string, replaced bool) {
	f, err := os.Open(filepath.Join(moduleRoot, "go.mod"))
	if err != nil {
		return "", false
	}
	defer func() { _ = f.Close() }()

	var inRequire, inReplace bool
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if i := strings.Index(line, "//"); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		switch {
		case line == "":
			continue
		case strings.HasPrefix(line, "require ("):
			inRequire = true
			continue
		case strings.HasPrefix(line, "replace ("):
			inReplace = true
			continue
		case line == ")":
			inRequire, inReplace = false, false
			continue
		}
		if inReplace || strings.HasPrefix(line, "replace ") {
			if left, _, ok := strings.Cut(strings.TrimPrefix(line, "replace "), "=>"); ok && isRotiniModuleRef(left) {
				replaced = true
			}
			continue
		}
		if inRequire || strings.HasPrefix(line, "require ") {
			if fields := strings.Fields(strings.TrimPrefix(line, "require ")); len(fields) >= 2 && isRotiniPath(fields[0]) {
				version = fields[1]
			}
		}
	}
	return version, replaced
}

// isRotiniModuleRef reports whether the left side of a `replace` (a module path, optionally
// with a version) names the rotini module.
func isRotiniModuleRef(left string) bool {
	fields := strings.Fields(strings.TrimSpace(left))
	return len(fields) >= 1 && isRotiniPath(fields[0])
}

// isRotiniPath matches the rotini module path, including a future /vN major suffix.
func isRotiniPath(path string) bool {
	return path == rotiniModulePath || strings.HasPrefix(path, rotiniModulePath+"/v")
}

// majorVersion returns the leading numeric major of an "X.Y.Z" (or "vX.Y.Z") version, or
// "" if it has no recognizable major segment.
func majorVersion(version string) string {
	major, _, _ := strings.Cut(strings.TrimPrefix(version, "v"), ".")
	return major
}

// sameMajorVersion reports whether two versions share a major segment (the same-major
// policy the package and binary arms enforce); two empty majors are not "same".
func sameMajorVersion(a, b string) bool {
	ma, mb := majorVersion(a), majorVersion(b)
	return ma != "" && ma == mb
}
