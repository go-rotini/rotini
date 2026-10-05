package codegen

import (
	"path/filepath"
	"strings"
)

// lintComposedTree runs the generator's composer over the whole $ref tree so validate catches
// what emerges only once refs are followed: collisions across composition boundaries and
// cyclic or missing refs. Reusing the composer keeps validate and generate in agreement. Each
// child spec's own checks run in Processor.validateComposedSpecs. It is best-effort: it skips
// a spec with no refs, an invalid root (lintRootCommand reports it), and a spec outside the
// current module.
func lintComposedTree(spec *Spec, specPath string) []error {
	if !specHasRefs(spec) {
		return nil
	}
	if spec.Command.Name == "" || spec.Command.Ref != "" {
		return nil
	}
	root, name, err := findModule()
	if err != nil {
		return nil
	}
	// The composer resolves refs and import paths relative to the current module, so run it
	// only when the spec lives inside that module.
	absSpec, err1 := filepath.Abs(specPath)
	absRoot, err2 := filepath.Abs(root)
	if err1 != nil || err2 != nil ||
		(absSpec != absRoot && !strings.HasPrefix(absSpec, absRoot+string(filepath.Separator))) {
		return nil
	}
	if _, err := resolveTree(spec, specPath, name); err != nil {
		// The composer reports on the tree as a whole, so the problem is placed on the root.
		return []error{&problem{kind: "spec", ptr: rootPointer, loc: "command " + spec.Command.Name, msg: "composition: " + err.Error()}}
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
