package codegen

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-rotini/fs"
)

// planner is the one way generate and init touch the filesystem. Each real change becomes an
// operation in an [fs.Plan]; a generated file whose content already matches, a create-once
// file that exists, or a removal of a file that is gone plans nothing.
//
// A real run applies each operation as soon as it is planned, so later reads see the change.
// A dry run applies nothing and answers later reads from the plan instead: a file it would
// have created exists, and one it would have removed does not.
type planner struct {
	dry  bool
	plan *fs.Plan

	// pending and removed are what a dry run would have done, by absolute path.
	pending map[string][]byte
	removed map[string]bool

	// created lists, in order, each create-once file the run created: files the author owns
	// from then on.
	created []string
}

// newPlanner returns a planner that applies each operation (dry false) or only records it.
func newPlanner(dry bool) *planner {
	return &planner{dry: dry, plan: fs.NewPlan(), pending: map[string][]byte{}, removed: map[string]bool{}}
}

// generatedPerm is the mode of a file rotini creates.
const generatedPerm = 0o644

// write plans a generated file: created when missing, updated when its content differs, left
// alone when identical. An update keeps the file's mode.
func (pl *planner) write(path string, content []byte) error {
	current, mode, exists, err := pl.read(path)
	if err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	switch {
	case !exists:
		return pl.apply(fs.PlanOp{Action: fs.PlanActionCreate, Path: path, Data: content, Perm: generatedPerm})
	case !bytes.Equal(current, content):
		return pl.apply(fs.PlanOp{Action: fs.PlanActionUpdate, Path: path, Data: content, Perm: mode})
	}
	return nil
}

// createOnce plans a file the author owns from then on: created when missing, never compared
// or touched once it exists.
func (pl *planner) createOnce(path string, content []byte) error {
	exists, err := pl.exists(path)
	if err != nil || exists {
		return err
	}
	if err := pl.apply(fs.PlanOp{Action: fs.PlanActionCreate, Path: path, Data: content, Perm: generatedPerm}); err != nil {
		return err
	}
	pl.created = append(pl.created, path)
	return nil
}

// createdLines renders each created file as a report line, `created: <path>`, with the path
// relative to the working directory.
func (pl *planner) createdLines() string {
	var b strings.Builder
	for _, path := range pl.created {
		b.WriteString("created: ")
		b.WriteString(relToWorkdir(path))
		b.WriteString("\n")
	}
	return b.String()
}

// remove plans deleting path when it exists.
func (pl *planner) remove(path string) error {
	exists, err := pl.exists(path)
	if err != nil || !exists {
		return err
	}
	return pl.apply(fs.PlanOp{Action: fs.PlanActionDelete, Path: path})
}

// exists reports whether path exists, as the plan so far leaves it.
func (pl *planner) exists(path string) (bool, error) {
	_, _, exists, err := pl.read(path)
	return exists, err
}

// read returns path's content and mode as the plan so far leaves it.
func (pl *planner) read(path string) (content []byte, mode os.FileMode, exists bool, err error) {
	if pl.removed[path] {
		return nil, 0, false, nil
	}
	if data, ok := pl.pending[path]; ok {
		return data, generatedPerm, true, nil
	}
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, 0, false, nil
	}
	if err != nil {
		return nil, 0, false, fmt.Errorf("stat %s: %w", path, err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, false, fmt.Errorf("read %s: %w", path, err)
	}
	return data, info.Mode().Perm(), true, nil
}

// apply records op and, in a real run, carries it out at once. Parent directories are
// created as needed, and writes are atomic.
func (pl *planner) apply(op fs.PlanOp) error {
	pl.plan.Ops = append(pl.plan.Ops, op)
	if pl.dry {
		switch op.Action {
		case fs.PlanActionDelete:
			delete(pl.pending, op.Path)
			pl.removed[op.Path] = true
		default:
			pl.pending[op.Path] = op.Data
			delete(pl.removed, op.Path)
		}
		return nil
	}
	if err := fs.ApplyTransient(&fs.Plan{Ops: []fs.PlanOp{op}}); err != nil {
		return fmt.Errorf("%s %s: %w", op.Action, op.Path, err)
	}
	return nil
}

// Changes lists the planned operations one per line, in [fs.Plan.Diff]'s format, with each
// path shown relative to the working directory, climbing out of it with ../ when needed (as
// under go generate, which runs in the entrypoint's directory).
func (pl *planner) Changes() []string {
	if len(pl.plan.Ops) == 0 {
		return nil
	}
	shown := &fs.Plan{Ops: make([]fs.PlanOp, len(pl.plan.Ops))}
	for i, op := range pl.plan.Ops {
		op.Path = relToWorkdir(op.Path)
		if op.Source != "" {
			op.Source = relToWorkdir(op.Source)
		}
		shown.Ops[i] = op
	}
	return strings.Split(strings.TrimSuffix(shown.Diff(), "\n"), "\n")
}

// relToWorkdir is path relative to the working directory, or as given when it can't be.
func relToWorkdir(path string) string {
	wd, err := os.Getwd()
	if err != nil {
		return filepath.ToSlash(path)
	}
	rel, err := filepath.Rel(wd, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(rel)
}
