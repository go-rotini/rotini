package codegen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Composition is rotini's biggest architectural bet and was the least-covered part of the
// codebase: composeNestedRef — the transitive parent → child → grandchild $ref — sat at 0.0%.
// The e2e tier proves the composed BINARY dispatches to every level; this proves the composer
// itself, in-process, where it is cheap to assert the shape of what it emitted.

// transitiveTree is a three-level tree: root composes child, child composes grand.
func transitiveTree() map[string]string {
	return map[string]string{
		"cmd/grand/.rotini.spec.yaml": `version: 0.0.0
command:
  name: grand
  summary: the grandchild, as it describes itself
  flags:
    - name: loud
      identifiers:
        - --loud
      schema:
        type: bool
`,
		"cmd/grand/.rotini.conf.yaml": `version: 0.0.0
generate:
  packages:
    - type: cmd
      file: internal/cmd/grand/zz_grand.go
      package: grand
`,
		"cmd/child/.rotini.spec.yaml": `version: 0.0.0
command:
  name: child
  summary: the child
  commands:
    - $ref: ../grand/.rotini.spec.yaml
`,
		"cmd/child/.rotini.conf.yaml": `version: 0.0.0
generate:
  packages:
    - type: cmd
      file: internal/cmd/child/zz_child.go
      package: child
`,
		"cmd/root/.rotini.spec.yaml": `version: 0.0.0
command:
  name: root
  summary: the root
  commands:
    - $ref: ../child/.rotini.spec.yaml
      summary: as the parent describes it
`,
		"cmd/root/.rotini.conf.yaml": `version: 0.0.0
generate:
  packages:
    - type: cmd
      file: internal/cmd/root/zz_root.go
      package: root
`,
	}
}

// TestCompose_transitiveRef covers composeNestedRef: the grandchild is grafted into the root's
// tree, and every composed node delegates back to the DIRECT child's package rather than
// having its types or stubs re-emitted at the root.
func TestCompose_transitiveRef(t *testing.T) {
	emitted := composeModuleStaged(t, transitiveTree())

	root, ok := emitted["internal/cmd/root/zz_root.go"]
	if !ok {
		t.Fatalf("root package was not generated; got %v", keysOf(emitted))
	}

	// 1. The whole tree is present in the root's Definition, two levels deep.
	for _, want := range []string{`"child"`, `"grand"`} {
		if !strings.Contains(root, want) {
			t.Errorf("root Definition does not contain %s:\n%s", want, root)
		}
	}

	// 2. The parent's overlay summary won over the child's own. This is the overlay model:
	//    a parent tailors a composed child for its tree without forking it.
	if !strings.Contains(root, "as the parent describes it") {
		t.Error("the parent's overlay summary did not win over the child's own")
	}

	// 3. The grandchild delegates to the DIRECT child's package. A transitive ref must not
	//    make the root import the grandchild: the child already composed it and exposes a
	//    handler method for it, so the root goes through the child.
	if !strings.Contains(root, "internal/cmd/child") {
		t.Error("root does not import the direct child's package")
	}
	if strings.Contains(root, "internal/cmd/grand") {
		t.Error("root imports the grandchild directly — a transitive ref should delegate through the child")
	}

	// 4. No stub and no input types are emitted at the root for a composed node: they live
	//    with the command they belong to.
	if _, ok := emitted["internal/cmd/root/root_child.go"]; ok {
		t.Error("a stub was seeded at the root for a composed command")
	}
	if strings.Contains(root, "type GrandInputs") || strings.Contains(root, "type ChildInputs") {
		t.Error("composed commands' input types were re-emitted at the root")
	}

	// 5. The root's own stub IS seeded — it is an own command, not a composed one.
	if _, ok := emitted["internal/cmd/root/root.go"]; !ok {
		t.Errorf("the root's own handler stub was not seeded; got %v", keysOf(emitted))
	}
}

// TestCompose_cycleIsRejected: a ref that reaches back to a spec already on the stack is a
// cycle, caught at generate time rather than as an infinite recursion.
func TestCompose_cycleIsRejected(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "go.mod", "module example.com/compose\n\ngo 1.26\n")
	writeTestFile(t, dir, "a/.rotini.spec.yaml", `version: 0.0.0
command:
  name: a
  commands:
    - $ref: ../b/.rotini.spec.yaml
`)
	writeTestFile(t, dir, "b/.rotini.spec.yaml", `version: 0.0.0
command:
  name: b
  commands:
    - $ref: ../a/.rotini.spec.yaml
`)
	writeTestFile(t, dir, "a/.rotini.conf.yaml", `version: 0.0.0
generate:
  packages:
    - type: cmd
      file: internal/cmd/a/zz_a.go
      package: a
`)
	t.Chdir(dir)

	err := NewProcessor("0.0.0").Generate("a/.rotini.spec.yaml", "a/.rotini.conf.yaml", false, func(string, error) {}, func([]error) {})
	if err == nil {
		t.Fatal("a cyclic $ref generated successfully, want an error")
	}
	if !strings.Contains(err.Error(), "cyclic") {
		t.Errorf("error %q does not say the refs are cyclic", err)
	}
}

// composeModuleStaged generates each composed spec in dependency order (grandchild, child,
// root), which is the order a real project's `go generate ./...` produces, then returns
// everything emitted.
func composeModuleStaged(t *testing.T, files map[string]string) map[string]string {
	t.Helper()
	dir := t.TempDir()
	writeTestFile(t, dir, "go.mod", "module example.com/compose\n\ngo 1.26\n")
	for path, body := range files {
		full := filepath.Join(dir, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(dir)

	for _, name := range []string{"grand", "child", "root"} {
		spec := "cmd/" + name + "/.rotini.spec.yaml"
		conf := "cmd/" + name + "/.rotini.conf.yaml"
		if err := NewProcessor("0.0.0").Generate(spec, conf, false, func(string, error) {}, func([]error) {}); err != nil {
			t.Fatalf("generate %s: %v", name, err)
		}
	}
	return collectGoFiles(t, dir)
}

func keysOf(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
