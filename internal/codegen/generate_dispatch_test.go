package codegen

import (
	"regexp"
	"strings"
	"testing"
)

// programHandlersMethod matches one method of the generated ProgramHandlers interface.
var programHandlersMethod = regexp.MustCompile(`(?m)^\t(\w+)\(\) rotini\.Handler$`)

// assertSwitchDispatch checks that the generated NewProgram builds its program through
// rotini.NewProgramFunc, with one switch case per ProgramHandlers method calling through the
// interface, and never through the reflective rotini.NewProgram.
func assertSwitchDispatch(t *testing.T, src string, want ...string) {
	t.Helper()
	if !strings.Contains(src, "rotini.NewProgramFunc(definition, func(name string) (rotini.Handler, bool) {") {
		t.Errorf("generated NewProgram does not use rotini.NewProgramFunc:\n%s", src)
	}
	if strings.Contains(src, "rotini.NewProgram(") {
		t.Error("generated code still calls the reflective rotini.NewProgram")
	}
	iface, _, _ := strings.Cut(src, "\n}\n")
	methods := programHandlersMethod.FindAllStringSubmatch(iface, -1)
	if len(methods) == 0 {
		t.Fatalf("no ProgramHandlers methods found:\n%s", src)
	}
	seen := map[string]bool{}
	for _, m := range methods {
		seen[m[1]] = true
		if c := "case \"" + m[1] + "\":\n\t\t\treturn handlers." + m[1] + "(), true\n"; !strings.Contains(src, c) {
			t.Errorf("no switch case for ProgramHandlers method %s", m[1])
		}
	}
	for _, w := range want {
		if !seen[w] {
			t.Errorf("ProgramHandlers has no %s method; got %v", w, methods)
		}
	}
}

// TestDispatch_composedCommandsJoinTheSwitch: a composed child and grandchild are dispatched
// by the root's switch, under the root's method names.
func TestDispatch_composedCommandsJoinTheSwitch(t *testing.T) {
	emitted := composeModuleStaged(t, transitiveTree())
	root, ok := emitted["internal/cmd/root/zz_root.go"]
	if !ok {
		t.Fatalf("root package was not generated; got %v", keysOf(emitted))
	}
	assertSwitchDispatch(t, root, "Root", "RootChild", "RootChildGrand")
}

// TestDispatch_passthroughHandlerJoinsTheSwitch: a command whose handler lives in another
// package is dispatched by the same switch.
func TestDispatch_passthroughHandlerJoinsTheSwitch(t *testing.T) {
	dir := modelsModule(t, `version: 0.0.0
generate:
  packages:
    - type: cmd
      file: internal/cmd/cyc/zz_cyc.go
      package: cyc
`, "example.com/cyc/internal/cmd/cyc", "cyc")
	src := readEmitted(t, dir, "internal/cmd/cyc/zz_cyc.go")
	assertSwitchDispatch(t, src, "Cyc", "CycDeploy")
	if !strings.Contains(src, "return deployh.Deploy()") {
		t.Errorf("the passthrough method no longer delegates to its package:\n%s", src)
	}
}
