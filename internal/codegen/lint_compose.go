package codegen

import (
	"path/filepath"
	"strings"
)

// This file owns the deep composed-$ref lint check — relocated here from validate.go
// because it runs in the LINT stage (lintSpec appends it), not schema validation.

// lintComposedTree is the deep `$ref` descend (W8/D-W8.3): when the spec composes
// child specs, run the generator's own composer (resolveTree) over the WHOLE tree so
// `rotini validate` catches problems that only emerge once refs are followed — name/
// alias collisions across composition boundaries, cyclic or missing refs, and each
// composed spec's version. It reuses generate's exact compose logic (no separate walk),
// so validate and generate cannot drift. Best-effort: it needs a module (composed
// commands resolve to import paths) and only matters when refs are present, so a
// ref-less spec or a module-less context is skipped — leaving per-spec validation as-is.
func lintComposedTree(spec *Spec, specPath string) []error {
	if !specHasRefs(spec) {
		return nil
	}
	root, name, err := findModule()
	if err != nil {
		return nil // no module: a composed CLI can't generate here anyway; not validate's error to raise
	}
	// The composer resolves refs + import paths relative to the CWD module; only run it
	// when the spec actually lives inside that module (else CWD ≠ the spec's project and
	// the relative refs/imports would be meaningless — leave it to a validate run from
	// the right place).
	absSpec, err1 := filepath.Abs(specPath)
	absRoot, err2 := filepath.Abs(root)
	if err1 != nil || err2 != nil ||
		(absSpec != absRoot && !strings.HasPrefix(absSpec, absRoot+string(filepath.Separator))) {
		return nil
	}
	if _, err := resolveTree(spec, specPath, name); err != nil {
		return []error{&problem{kind: "spec", loc: "composition", msg: err.Error()}}
	}
	return nil
}

// specHasRefs reports whether any command in the tree composes a child spec via $ref.
func specHasRefs(spec *Spec) bool {
	found := false
	walkCommands(spec, func(c *Command, _ string) {
		if c.Ref != "" {
			found = true
		}
	})
	return found
}
