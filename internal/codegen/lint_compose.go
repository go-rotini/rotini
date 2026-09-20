package codegen

import (
	"path/filepath"
	"strings"
)

// This file owns the deep composed-$ref lint check — relocated here from validate.go
// because it runs in the LINT stage (lintSpec appends it), not schema validation.

// lintComposedTree is the deep `$ref` descend: it runs the generator's own composer over the
// whole tree so `rotini validate` catches what only emerges once refs are followed — collisions
// across composition boundaries, cyclic or missing refs, and each composed spec's version.
// Reusing generate's compose logic is what keeps validate and generate from drifting. It is
// best-effort: a ref-less spec or a module-less context is skipped.
func lintComposedTree(spec *Spec, specPath string) []error {
	if !specHasRefs(spec) {
		return nil
	}
	// A root that is not itself valid cannot be composed from, and lintRootCommand has
	// already said so — in a positioned message. Running the composer anyway would restate
	// it unpositioned, so the reader sees one cause reported twice.
	if spec.Command.Name == "" || spec.Command.Ref != "" {
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
