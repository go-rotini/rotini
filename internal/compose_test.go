package internal

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	childSpecYAML = `$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-spec.json
command:
  name: child
  commands:
    - name: greet
      inputs:
        arguments:
          - name: who
            schema: { type: string }
        flags:
          - name: loud
            identifiers: [--loud]
            schema: { type: bool }
`
	childConfYAML = `$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-conf.json
generate:
  rth:
    package: cmd/child/rth
    file: handlers.go
  rtg:
    package: cmd/child/rtg
    file: rotini.go
`
	parentSpecYAML = `$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-spec.json
command:
  name: parent
  commands:
    - $ref: ../child/.rotini.spec.yaml
`
	parentConfYAML = `$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-conf.json
generate:
  rth:
    package: cmd/parent/rth
    file: handlers.go
  rtg:
    package: cmd/parent/rtg
    file: rotini.go
`
)

func TestGenerate_staticComposition(t *testing.T) {
	tmp := initTestModule(t) // module example.com/myclis, chdir'd
	writeTestFile(t, filepath.Join(tmp, "cmd/child/.rotini.spec.yaml"), childSpecYAML)
	writeTestFile(t, filepath.Join(tmp, "cmd/child/.rotini.conf.yaml"), childConfYAML)
	writeTestFile(t, filepath.Join(tmp, "cmd/parent/.rotini.spec.yaml"), parentSpecYAML)
	writeTestFile(t, filepath.Join(tmp, "cmd/parent/.rotini.conf.yaml"), parentConfYAML)

	// Children must be generated before parents (the parent imports the child rth).
	if err := Generate("cmd/child/.rotini.spec.yaml", "cmd/child/.rotini.conf.yaml", false, nil); err != nil {
		t.Fatalf("generate child: %v", err)
	}
	if err := Generate("cmd/parent/.rotini.spec.yaml", "cmd/parent/.rotini.conf.yaml", false, nil); err != nil {
		t.Fatalf("generate parent: %v", err)
	}

	// Parent rtg: composed commands appear in the interface and the Definition,
	// but their input structs are NOT redeclared (they live in the child's rtg).
	rtg := filepath.Join(tmp, "cmd/parent/rtg/rotini.go")
	mustContain(t, rtg,
		"ParentChild() rotini.CommandHandlers",
		"ParentChildGreet() rotini.CommandHandlers",
		`Name: "child"`, `Handler: "ParentChild"`,
		`Name: "greet"`, `Handler: "ParentChildGreet"`,
		`{Name: "who", Type: "string"}`,
	)
	mustNotContain(t, rtg, "ParentChildGreetInputs", "ParentChildGreetFlags")

	// Parent rollup: imports the child rth (aliased) and delegates composed
	// commands to it; own root returns a local stub.
	rollup := filepath.Join(tmp, "cmd/parent/rth/handlers.go")
	mustContain(t, rollup,
		`childrth "example.com/myclis/cmd/child/rth"`,
		"return childrth.Handlers().Child()",
		"return childrth.Handlers().ChildGreet()",
		"return &parentHandlers{}",
	)

	// No stub is created in the parent for a composed command.
	if _, err := os.Stat(filepath.Join(tmp, "cmd/parent/rth/parent_child_greet.go")); !os.IsNotExist(err) {
		t.Errorf("composed command should not get a parent stub: %v", err)
	}
	// The child remains a standalone CLI with its own typed inputs + Handlers().
	mustContain(t, filepath.Join(tmp, "cmd/child/rtg/rotini.go"), "type ChildGreetInputs struct")
	mustContain(t, filepath.Join(tmp, "cmd/child/rth/handlers.go"), "func Handlers() rtg.ProgramHandlers")
}

func TestGenerate_cyclicRefErrors(t *testing.T) {
	tmp := initTestModule(t)
	// A spec that composes itself — the simplest cycle.
	selfRef := `$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-spec.json
command:
  name: a
  commands:
    - $ref: ../a/.rotini.spec.yaml
`
	conf := "$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-conf.json\n" +
		"generate:\n  rth:\n    package: cmd/a/rth\n  rtg:\n    package: cmd/a/rtg\n"
	writeTestFile(t, filepath.Join(tmp, "cmd/a/.rotini.spec.yaml"), selfRef)
	writeTestFile(t, filepath.Join(tmp, "cmd/a/.rotini.conf.yaml"), conf)

	err := Generate("cmd/a/.rotini.spec.yaml", "cmd/a/.rotini.conf.yaml", false, nil)
	if err == nil || !strings.Contains(err.Error(), "cyclic") {
		t.Fatalf("expected cyclic $ref error, got %v", err)
	}
}

func mustNotContain(t *testing.T, path string, subs ...string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	for _, s := range subs {
		if strings.Contains(string(data), s) {
			t.Errorf("%s unexpectedly contains %q", path, s)
		}
	}
}
