package codegen

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestGenerateCompose_localRef covers composition mode 3: a parent command tree that
// pulls in a sibling spec with a local "$ref". The child's tree merges in and codegen
// auto-delegates the composed command to the child's OWN generated package.
//
// The build step is the load-bearing half. Delegation across packages only type-checks
// because both generated packages import the SAME github.com/go-rotini/rotini — so
// childcli.Handlers().Child() satisfies the parent's rotini.Handlers. When the runtime
// was emitted per-project, each package got its own incompatible rotini.Context and this
// could not compile.
func TestGenerateCompose_localRef(t *testing.T) {
	skipUnlessCompiling(t)
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	writeTestFile(t, dir, "go.mod", "module example.com/comp\n\ngo 1.26\n\nrequire github.com/go-rotini/rotini v0.0.0\n\nreplace github.com/go-rotini/rotini => "+filepath.ToSlash(repoRoot)+"\n")

	writeTestFile(t, dir, "cmd/child/.rotini.spec.yaml", `version: 0.0.0
command:
  name: child
  summary: the composed child
  flags:
    - name: force
      summary: force it
      identifiers: [--force]
      schema: {type: bool}
`)
	writeTestFile(t, dir, "cmd/child/.rotini.conf.yaml", `version: 0.0.0
generate:
  packages:
    - type: cmd
      file: internal/cmd/child/zz_child.go
      package: child
`)
	writeTestFile(t, dir, "cmd/parent/.rotini.spec.yaml", `version: 0.0.0
command:
  name: parent
  summary: the composing parent
  commands:
    - $ref: ../child/.rotini.spec.yaml
`)
	writeTestFile(t, dir, "cmd/parent/.rotini.conf.yaml", `version: 0.0.0
generate:
  packages:
    - type: main
      file: cmd/parent/main.go
      package: main
    - type: cmd
      file: internal/cmd/parent/zz_parent.go
      package: parent
`)
	t.Chdir(dir)

	// The child is generated first: the parent's rollup imports the child's cmd package,
	// whose location the parent reads from the CHILD's conf (childCmdImport).
	for _, cli := range []string{"child", "parent"} {
		spec := "cmd/" + cli + "/.rotini.spec.yaml"
		conf := "cmd/" + cli + "/.rotini.conf.yaml"
		if err := NewProcessor("0.0.0").Generate(spec, conf, false, func(string, error) {}, func([]error) {}); err != nil {
			t.Fatalf("Generate %s: %v", cli, err)
		}
	}

	rollup := readEmitted(t, dir, "internal/cmd/parent/zz_parent.go")
	for _, want := range []string{
		`childcli "example.com/comp/internal/cmd/child"`, // aliased child import (identAlias)
		"ParentChild() rotini.Handlers",                  // the composed command joins ProgramHandlers
		"childcli.Handlers().Child()",                    // auto-delegation, no hand wiring
	} {
		if !strings.Contains(rollup, want) {
			t.Errorf("parent rollup missing %q", want)
		}
	}
	// The child contributes the tree, not a stub in the parent's package: the parent
	// must NOT get its own handler file for the composed command.
	if _, err := readEmittedIfExists(dir, "internal/cmd/parent/parent_child.go"); err == nil {
		t.Error("parent got a handler stub for the composed command; it must delegate to the child")
	}

	for _, args := range [][]string{{"mod", "tidy"}, {"build", "./..."}} {
		cmd := exec.Command("go", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("go %s in the composed module: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
}
