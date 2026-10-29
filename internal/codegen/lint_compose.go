package codegen

import (
	"cmp"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
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

// writtenCommand is a command entry as a spec file writes it: inline, or a $ref.
type writtenCommand struct {
	cmd *Command
	ptr string // its JSON pointer in src
	src *reconciledSpec
}

// composedNode is one command of the tree a spec composes through local $refs: the command as
// its own spec writes it, the name it is invoked by (a mounted root's is the name its $ref entry
// gives), where it is written, and, for a mounted root, the commands written beside its $ref.
type composedNode struct {
	writtenCommand

	name    string
	locator string // the spec file a mounted root comes from; "" for an inline command
	beside  []writtenCommand
}

// children lists the commands below n: its own, then those written beside its $ref.
func (n composedNode) children() []writtenCommand {
	out := make([]writtenCommand, 0, len(n.cmd.Commands)+len(n.beside))
	for i := range n.cmd.Commands {
		out = append(out, writtenCommand{cmd: &n.cmd.Commands[i], ptr: fmt.Sprintf("%s/commands/%d", n.ptr, i), src: n.src})
	}
	return append(out, n.beside...)
}

// lintComposedShadowing is lintShadowedIdentifiers across $ref boundaries: a flag of a mounted
// spec that reuses an identifier of a cascading flag declared in another file above it. Each
// spec's own lint covers the flags of one file, so only pairs from two files are reported, with
// the message an inline child gets, placed at the flag in the file declaring it. mod:// and
// remote refs, and specs that don't read, are skipped, as validateComposedSpecs skips them.
func lintComposedShadowing(rs *reconciledSpec) []error {
	if rs == nil || rs.spec == nil || !specHasRefs(rs.spec) {
		return nil
	}
	var problems []error
	loaded := map[string]*reconciledSpec{}
	var walk func(chain []composedNode, paths []string)
	walk = func(chain []composedNode, paths []string) {
		problems = append(problems, crossFileShadows(chain, paths)...)
		for _, w := range chain[len(chain)-1].children() {
			n, ok := resolveWritten(w, loaded)
			if !ok || slices.ContainsFunc(chain, func(a composedNode) bool { return n.locator != "" && a.locator == n.locator }) {
				continue
			}
			walk(append(slices.Clone(chain), n), append(slices.Clone(paths), paths[len(paths)-1]+"/"+n.name))
		}
	}
	root := composedNode{cmd: &rs.spec.Command, ptr: rootPointer, src: rs, name: cmp.Or(rs.spec.Command.Name, "(root)")}
	if abs, err := filepath.Abs(rs.path); err == nil {
		root.locator = filepath.Clean(abs)
	}
	walk([]composedNode{root}, []string{root.name})
	return problems
}

// resolveWritten turns a command entry into its node: itself, or the root of the local spec its
// $ref mounts. ok is false for a ref that isn't local or doesn't read as a spec.
func resolveWritten(w writtenCommand, loaded map[string]*reconciledSpec) (composedNode, bool) {
	c := w.cmd
	if c.Ref == "" {
		return composedNode{writtenCommand: w, name: c.Name}, true
	}
	if strings.HasPrefix(c.Ref, modScheme) || isExternalLocator(c.Ref) {
		return composedNode{}, false
	}
	locator, err := locateRef(filepath.Dir(w.src.path), c.Ref)
	if err != nil {
		return composedNode{}, false
	}
	child, seen := loaded[locator]
	if !seen {
		if child, err = reconcileSpec(displayPath(locator)); err != nil {
			child = nil // validateComposedSpecs reports it
		}
		loaded[locator] = child
	}
	if child == nil || child.spec == nil {
		return composedNode{}, false
	}
	n := composedNode{cmd: &child.spec.Command, ptr: rootPointer, src: child, name: cmp.Or(c.Name, child.spec.Command.Name), locator: locator}
	for i := range c.Commands {
		n.beside = append(n.beside, writtenCommand{cmd: &c.Commands[i], ptr: fmt.Sprintf("%s/commands/%d", w.ptr, i), src: w.src})
	}
	return n, true
}

// crossFileShadows is lintShadowedIdentifiers for the leaf of one composed chain, keeping the
// shadowings whose ancestor flag is written in another file than the leaf.
func crossFileShadows(chain []composedNode, paths []string) []error {
	if len(chain) < 2 {
		return nil
	}
	cmds := make([]*Command, len(chain))
	for i, n := range chain {
		cmds[i] = n.cmd
	}
	owners := ancestorOwners(cmds, paths)
	leaf := chain[len(chain)-1]
	named := *leaf.cmd // the leaf as it is invoked, for the message
	named.Name = leaf.name
	hits, taken := shadowHits(&named, owners)
	var problems []error
	for _, h := range hits {
		if chain[h.owner.at].src == leaf.src {
			continue
		}
		p := shadowProblem(h, &named, taken[h.owner], paths[len(paths)-1], leaf.ptr)
		locateProblems([]error{p}, leaf.src.path, leaf.src.locate)
		problems = append(problems, p)
	}
	return problems
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
