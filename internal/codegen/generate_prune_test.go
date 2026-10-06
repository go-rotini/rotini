package codegen

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const orphanStub = "package demo\n\nvar _ rotini.Handler = (*demoShipHandler)(nil)\n\ntype demoShipHandler struct{}\n"

// TestPruneGoDir_twoSteps pins the cycle: a live orphan is disabled, a disabled one is deleted,
// and each step is reported.
func TestPruneGoDir_twoSteps(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "demo_ship.go", orphanStub)
	var notes []string
	prune := func() {
		t.Helper()
		if err := pruneGoDir(newPlanner(false), dir, map[string]bool{}, func(n string) { notes = append(notes, n) }); err != nil {
			t.Fatal(err)
		}
	}

	prune()
	body, err := os.ReadFile(filepath.Join(dir, "demo_ship.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(body), disabledHeader) || !strings.HasSuffix(string(body), orphanStub) {
		t.Errorf("disabled stub =\n%s\nwant the header above the original code", body)
	}
	if _, err := parser.ParseFile(token.NewFileSet(), "x.go", body, parser.ParseComments); err != nil {
		t.Errorf("the disabled stub doesn't parse: %v", err)
	}

	prune()
	if _, err := os.Stat(filepath.Join(dir, "demo_ship.go")); !os.IsNotExist(err) {
		t.Error("the disabled stub survived the second prune")
	}
	if want := []string{"demo_ship.go (disabled; removed by the next generate)", "demo_ship.go"}; !slices.Equal(notes, want) {
		t.Errorf("notes = %q, want %q", notes, want)
	}
}

// TestDisableStub_replacesABuildConstraint pins that an author's constraint is replaced, never
// stacked, since a file may carry only one.
func TestDisableStub_replacesABuildConstraint(t *testing.T) {
	src := "//go:build linux\n// +build linux\n\n// Package comment.\npackage demo\n\nvar _ rotini.Handler = (*h)(nil)\n"
	got := string(disableStub([]byte(src)))
	if strings.Count(got, "//go:build") != 1 || strings.Contains(got, "+build") {
		t.Errorf("disabled =\n%s\nwant exactly one build constraint, ignore", got)
	}
	if !strings.Contains(got, "// Package comment.\npackage demo") {
		t.Errorf("disabled =\n%s\nwant the author's comments kept", got)
	}
	if _, err := parser.ParseFile(token.NewFileSet(), "x.go", got, parser.ParseComments); err != nil {
		t.Errorf("doesn't parse: %v", err)
	}
}

// TestPruneGoDir_leavesClaimedFilesAlone pins the files pruning never touches.
func TestPruneGoDir_leavesClaimedFilesAlone(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		// The author removed rotini's note from a disabled stub: theirs now.
		"noteless.go": "//go:build ignore\n\n" + orphanStub,
		// The marker is gone: hand-written.
		"mine.go": "package demo\n\nfunc helper() {}\n",
		// Kept by the conf.
		"kept.go": orphanStub,
		// A current stub.
		"current.go": orphanStub,
	}
	for name, body := range files {
		writeTestFile(t, dir, name, body)
	}
	if err := pruneGoDir(newPlanner(false), dir, map[string]bool{"kept.go": true, "current.go": true}, nil); err != nil {
		t.Fatal(err)
	}
	for name, want := range files {
		if got, err := os.ReadFile(filepath.Join(dir, name)); err != nil || string(got) != want {
			t.Errorf("%s was touched: %v", name, err)
		}
	}
}

// TestHookAudit_skipsDisabledStubs pins that a disabled stub, which may reference an inputs
// type that no longer exists, produces no audit warnings.
func TestHookAudit_skipsDisabledStubs(t *testing.T) {
	cmdDir, gen := hookAuditFixture(t)
	// Build-ignored without rotini's note, so pruning leaves it for the audit to see.
	writeTestFile(t, cmdDir, "demo_gone.go", "//go:build ignore\n\n"+`package demo

import (
	"context"

	"github.com/go-rotini/rotini"
)

var _ rotini.Handler = (*demoBuildHandler)(nil)

func (*demoBuildHandler) Prerun(ctx context.Context, rtx *rotini.Context) {
	_, _ = rtx.Inputs[DemoInputs]()
}
`)
	for _, n := range gen(t) {
		if strings.Contains(n.Error(), "demo_gone.go") {
			t.Errorf("audit warned about a disabled stub: %v", n)
		}
	}
}

// TestBuildIgnored pins which files count as out of the build: a constraint needing the
// ignore tag, wherever it sits above the package clause and whatever the line endings.
func TestBuildIgnored(t *testing.T) {
	for src, want := range map[string]bool{
		"//go:build ignore\n\npackage demo\n":                          true,
		"// Copyright 2026 me.\n\n//go:build ignore\n\npackage demo\n": true,
		"//go:build ignore\r\n\r\npackage demo\r\n":                    true,
		"//go:build ignore && linux\n\npackage demo\n":                 true,
		"//go:build linux\n\npackage demo\n":                           false,
		"//go:build !ignore\n\npackage demo\n":                         false,
		"package demo\n\n//go:build ignore\n":                          false,
	} {
		if got := buildIgnored([]byte(src)); got != want {
			t.Errorf("buildIgnored(%q) = %v, want %v", src, got, want)
		}
	}
}

// TestStubDisabled_noteMustFollowTheConstraint pins that rotini's note counts only where
// rotini put it, so a quoted copy elsewhere doesn't mark a file for deletion.
func TestStubDisabled_noteMustFollowTheConstraint(t *testing.T) {
	if !stubDisabled(disableStub([]byte(orphanStub))) {
		t.Error("a stub rotini disabled is not recognized")
	}
	moved := "//go:build ignore\n\n// mine\n" + disabledNote + "\n" + orphanStub
	if stubDisabled([]byte(moved)) {
		t.Error("a note away from the constraint was recognized")
	}
}

func TestBuildIgnored_negatedPlatformStillBuilds(t *testing.T) {
	if buildIgnored([]byte("//go:build !linux\n\npackage demo\n")) {
		t.Error("a !linux file was treated as out of every build")
	}
}
