package codegen

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// lintComposedTree runs the generator's composer over the whole $ref tree so validate catches
// what emerges only once refs are followed: collisions across composition boundaries and
// cyclic or missing refs. Reusing the composer keeps validate and generate in agreement. Each
// child spec's own checks run in Processor.validateComposedSpecs. It is best-effort: it skips
// a spec with no refs and an invalid root (lintRootCommand reports it). For a spec outside the
// current module, or with no module, it checks only that each local ref names a spec file.
func lintComposedTree(spec *Spec, specPath string) []error {
	if !specHasRefs(spec) {
		return nil
	}
	if spec.Command.Name == "" || spec.Command.Ref != "" {
		return nil
	}
	root, name, err := findModule()
	if err != nil {
		return missingLocalRefs(spec, specPath)
	}
	// The composer resolves refs and import paths relative to the current module, so run it
	// only when the spec lives inside that module.
	absSpec, err1 := filepath.Abs(specPath)
	absRoot, err2 := filepath.Abs(root)
	if err1 != nil || err2 != nil ||
		(absSpec != absRoot && !strings.HasPrefix(absSpec, absRoot+string(filepath.Separator))) {
		return missingLocalRefs(spec, specPath)
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

// missingLocalRefs reports each local $ref of spec that names no file, or one the composer can't
// read as a spec, as the composer would. It is the check that runs when the composer can't, for
// want of the module.
func missingLocalRefs(spec *Spec, specPath string) []error {
	var problems []error
	base := filepath.Dir(specPath)
	for _, ref := range localSpecRefs(&spec.Command) {
		locator, err := locateRef(base, ref)
		if err != nil {
			continue
		}
		if _, err := os.Stat(locator); errors.Is(err, os.ErrNotExist) {
			problems = append(problems, &problem{kind: "spec", ptr: rootPointer, loc: "command " + spec.Command.Name,
				msg: fmt.Sprintf("composition: compose %q: %s: %v", ref, locator, os.ErrNotExist)})
		} else if _, err := readSpec(locator); err != nil {
			// A directory, or a file that isn't a spec.
			problems = append(problems, &problem{kind: "spec", ptr: rootPointer, loc: "command " + spec.Command.Name,
				msg: fmt.Sprintf("composition: compose %q: %v", ref, err)})
		}
	}
	return problems
}
