package codegen

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"

	"github.com/go-rotini/fs"
)

// TestPlanner_onlyRealChanges pins which effect becomes which operation, and that an effect
// that changes nothing plans nothing.
func TestPlanner_onlyRealChanges(t *testing.T) {
	dir := t.TempDir()
	same := filepath.Join(dir, "same.go")
	changed := filepath.Join(dir, "changed.go")
	owned := filepath.Join(dir, "owned.go")
	gone := filepath.Join(dir, "gone.go")
	for path, body := range map[string]string{same: "same", changed: "old", owned: "mine", gone: "x"} {
		writeTestFile(t, dir, filepath.Base(path), body)
	}

	pl := newPlanner(true)
	steps := []error{
		pl.write(same, []byte("same")),
		pl.write(changed, []byte("new")),
		pl.write(filepath.Join(dir, "sub", "new.go"), []byte("new")),
		pl.createOnce(owned, []byte("seed")),
		pl.createOnce(filepath.Join(dir, "stub.go"), []byte("seed")),
		pl.remove(gone),
		pl.remove(filepath.Join(dir, "never.go")),
	}
	for _, err := range steps {
		if err != nil {
			t.Fatal(err)
		}
	}
	var got []string
	for _, op := range pl.plan.Ops {
		got = append(got, op.Action.String()+" "+filepath.Base(op.Path))
	}
	want := []string{"update changed.go", "create new.go", "create stub.go", "delete gone.go"}
	if !slices.Equal(got, want) {
		t.Fatalf("ops = %v, want %v", got, want)
	}
}

// TestPlanner_dryRunTouchesNothing pins that a dry run leaves the disk as it was, while later
// questions see the plan.
func TestPlanner_dryRunTouchesNothing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.go")
	writeTestFile(t, dir, "a.go", "old")

	pl := newPlanner(true)
	if err := pl.write(path, []byte("new")); err != nil {
		t.Fatal(err)
	}
	if err := pl.createOnce(filepath.Join(dir, "b.go"), []byte("b")); err != nil {
		t.Fatal(err)
	}
	if body, _ := os.ReadFile(path); string(body) != "old" {
		t.Errorf("a.go = %q, want it untouched", body)
	}
	if fs.Exists(filepath.Join(dir, "b.go")) {
		t.Error("a dry run created b.go")
	}
	if exists, _ := pl.exists(filepath.Join(dir, "b.go")); !exists {
		t.Error("the plan doesn't see the b.go it would create")
	}
	if err := pl.remove(path); err != nil {
		t.Fatal(err)
	}
	if exists, _ := pl.exists(path); exists || !fs.Exists(path) {
		t.Error("a planned removal should hide a.go from the plan and leave it on disk")
	}
}

// TestPlanner_updateKeepsTheMode pins that rewriting a generated file keeps its mode.
func TestPlanner_updateKeepsTheMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file modes are not Unix modes on Windows")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "a.go")
	writeTestFile(t, dir, "a.go", "old")
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	pl := newPlanner(false)
	if err := pl.write(path, []byte("new")); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600 kept", info.Mode().Perm())
	}
	if body, _ := os.ReadFile(path); string(body) != "new" {
		t.Errorf("a.go = %q, want new", body)
	}
}
