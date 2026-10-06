package codegen

import (
	"path/filepath"
	"strings"
	"testing"
)

// exitAuditFixture generates a project whose `build` command documents codes 0, 1 and 4 and
// whose root documents none, and returns the cmd package directory and a re-generate func.
func exitAuditFixture(t *testing.T) (cmdDir string, gen func(*testing.T) []error) {
	t.Helper()
	dir := t.TempDir()
	writeTestFile(t, dir, "go.mod", "module example.com/demo\n\ngo 1.26\n")
	writeTestFile(t, dir, ".rotini.spec.yaml", `version: 0.0.0
command:
  name: demo
  commands:
    - name: build
      exit_status:
        - { code: 0, summary: built }
        - { code: 1, summary: failed }
        - { code: 4, summary: partly built }
`)
	writeTestFile(t, dir, ".rotini.conf.yaml", goldenConf)
	t.Chdir(dir)

	gen = func(t *testing.T) []error {
		t.Helper()
		var notices []error
		if err := NewProcessor("0.0.0").Generate(".rotini.spec.yaml", ".rotini.conf.yaml", false,
			func(string, error) {}, func(n []error) { notices = append(notices, n...) }); err != nil {
			t.Fatalf("Generate: %v", err)
		}
		return notices
	}
	gen(t)
	return filepath.Join(dir, "internal", "cmd", "demo"), gen
}

// TestExitAudit pins which exit codes the audit reports: undocumented literals and same-package
// constants on the method's *rotini.Context, whatever it is named, and nothing else.
func TestExitAudit(t *testing.T) {
	cmdDir, gen := exitAuditFixture(t)
	appendToStub(t, cmdDir, "demo_build.go", `
const exitConflict = 3
const exitPartial = 4

func (*demoBuildHandler) PostRun(ctx context.Context, c *rotini.Context) {
	c.HaltWithCode(7)
	c.HaltWithCode(exitConflict)
	c.HaltWithCode(exitPartial)
	c.HaltWithCode(1)
	c.HaltWithCode(0)
	n := 9
	c.HaltWithCode(n)
	c.Exit(n + 1)
}

type other struct{}

func (other) Exit(int) {}

func (*demoBuildHandler) helper(r *rotini.Context, o other) {
	r.Exit(5)
	o.Exit(6)
}
`)
	appendToStub(t, cmdDir, "demo.go", `
func (*demoHandler) PostRun(ctx context.Context, rtx *rotini.Context) {
	rtx.HaltWithCode(8)
}
`)

	var got []string
	for _, n := range gen(t) {
		if strings.Contains(n.Error(), "exit_status") {
			got = append(got, n.Error())
		}
	}
	want := []string{
		`internal/cmd/demo/demo_build.go:`, `"demo build" exits with 7, which its exit_status doesn't list`,
		`internal/cmd/demo/demo_build.go:`, `"demo build" exits with 3, which its exit_status doesn't list`,
		`internal/cmd/demo/demo_build.go:`, `"demo build" exits with 5, which its exit_status doesn't list`,
	}
	if len(got) != 3 {
		t.Fatalf("exit-code warnings = %q, want 3 (7, the constant 3, and 5 in a helper method)", got)
	}
	for i, w := range got {
		if !strings.HasPrefix(w, want[2*i]) || !strings.HasSuffix(w, want[2*i+1]) {
			t.Errorf("warning %d = %q, want %q … %q", i, w, want[2*i], want[2*i+1])
		}
	}
}

// TestExitAudit_aliasedImportAndLine pins that the audit follows an aliased rotini import and
// reports the exact line of the call.
func TestExitAudit_aliasedImportAndLine(t *testing.T) {
	cmdDir, gen := exitAuditFixture(t)
	writeTestFile(t, cmdDir, "extra.go", `package demo

import r "github.com/go-rotini/rotini"

func (*demoBuildHandler) bail(c *r.Context) {
	c.Exit(9)
}
`)
	var got []string
	for _, n := range gen(t) {
		if strings.Contains(n.Error(), "exit_status") {
			got = append(got, n.Error())
		}
	}
	want := `internal/cmd/demo/extra.go:6: "demo build" exits with 9, which its exit_status doesn't list`
	if len(got) != 1 || got[0] != want {
		t.Errorf("warnings = %q, want [%q]", got, want)
	}
}

// TestExitAudit_cascadingHookIsTheDeclarersCommand pins that a code set in a cascading hook is
// attributed to the command whose handler declares it, though the hook also runs for that
// command's descendants.
func TestExitAudit_cascadingHookIsTheDeclarersCommand(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "go.mod", "module example.com/demo\n\ngo 1.26\n")
	writeTestFile(t, dir, ".rotini.spec.yaml", `version: 0.0.0
command:
  name: demo
  exit_status:
    - { code: 0, summary: ok }
  commands:
    - name: build
      exit_status:
        - { code: 0, summary: built }
        - { code: 4, summary: partly built }
`)
	writeTestFile(t, dir, ".rotini.conf.yaml", goldenConf)
	t.Chdir(dir)
	gen := func() []error {
		var notices []error
		if err := NewProcessor("0.0.0").Generate(".rotini.spec.yaml", ".rotini.conf.yaml", false, nil, func(n []error) { notices = append(notices, n...) }); err != nil {
			t.Fatalf("Generate: %v", err)
		}
		return notices
	}
	gen()
	writeTestFile(t, filepath.Join(dir, "internal", "cmd", "demo"), "hooks.go", `package demo

import (
	"context"

	"github.com/go-rotini/rotini"
)

func (*demoHandler) CascadingPostRun(ctx context.Context, rtx *rotini.Context) {
	rtx.HaltWithCode(4)
}
`)
	var got []string
	for _, n := range gen() {
		if strings.Contains(n.Error(), "exit_status") {
			got = append(got, n.Error())
		}
	}
	if len(got) != 1 || !strings.Contains(got[0], `"demo" exits with 4`) {
		t.Errorf("warnings = %q, want one attributing 4 to \"demo\", which doesn't list it", got)
	}
}
