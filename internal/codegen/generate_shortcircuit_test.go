package codegen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// scConf is goldenConf with the help feature on, which the short-circuit seed shape needs.
const scConf = goldenConf + `  features:
    - type: help
      enabled: true
`

// scSpec builds a spec whose root --help is short_circuit (and cascading when cascading is
// set), with a command relying on the root's --help and one declaring its own.
func scSpec(cascading bool) string {
	casc := ""
	if cascading {
		casc = "\n      cascading: true"
	}
	return `version: 0.0.0
command:
  name: demo
  flags:
    - name: help
      identifiers: [-h, --help]` + casc + `
      short_circuit: true
      schema: { type: bool }
    - name: version
      identifiers: [-v, --version]
      short_circuit: true
      schema: { type: bool }
  commands:
    - name: build
    - name: own
      flags:
        - name: help
          identifiers: [-h, --help]
          schema: { type: bool }
      commands:
        - name: sub
          arguments:
            - name: what
              schema: { type: string, required: true }
`
}

// TestStubs_shortCircuitShape pins the stubs the short-circuit shape seeds: one root
// CascadingPreRun answering --help and --version, no help code in a stub that relies on the
// root's flag, and the per-stub answer kept where a command declares its own --help.
func TestStubs_shortCircuitShape(t *testing.T) {
	files := emitInModule(t, scSpec(true), scConf)

	root := files["internal/cmd/demo/demo.go"]
	for _, want := range []string{"func (*demoHandler) CascadingPreRun(", "Help(path...)", "inputs.Demo.Flags.Version"} {
		if !strings.Contains(root, want) {
			t.Errorf("root stub is missing %q:\n%s", want, root)
		}
	}
	if strings.Contains(root, "NoCascadingPreRun") || strings.Contains(root, "ArgvInputs") {
		t.Errorf("root stub keeps the NoCascadingPreRun embed or a per-stub help check:\n%s", root)
	}
	if build := files["internal/cmd/demo/demo_build.go"]; strings.Contains(build, "ArgvInputs") {
		t.Errorf("a command relying on the root's --help still checks it itself:\n%s", build)
	}
	if own := files["internal/cmd/demo/demo_own.go"]; !strings.Contains(own, "ArgvInputs") {
		t.Errorf("a command declaring its own --help lost its per-stub answer:\n%s", own)
	}
	// own sub resolves --help to own's flag, which isn't short_circuit, so the root's hook
	// never answers it: the stub must.
	if sub := files["internal/cmd/demo/demo_own_sub.go"]; !strings.Contains(sub, "ArgvInputs") {
		t.Errorf("a command under one declaring its own --help lost its per-stub answer:\n%s", sub)
	}
}

// TestStubs_withoutCascadingKeepPerStubHelp pins the safety rule for projects not in the full
// shape: the root hook still answers the root's flags, but every other stub keeps its own
// --help check, since nothing guarantees the root's hook covers it.
func TestStubs_withoutCascadingKeepPerStubHelp(t *testing.T) {
	files := emitInModule(t, scSpec(false), scConf)
	if root := files["internal/cmd/demo/demo.go"]; !strings.Contains(root, "CascadingPreRun(") {
		t.Errorf("root stub has no CascadingPreRun for its short-circuit flags:\n%s", root)
	}
	if build := files["internal/cmd/demo/demo_build.go"]; !strings.Contains(build, "ArgvInputs") {
		t.Errorf("without a cascading short-circuit --help, a stub must keep its own check:\n%s", build)
	}
}

// TestHookAudit_warnsOnAMissingRootHook pins the warning when the root's --help is in the
// short-circuit shape but the root handler has no CascadingPreRun, and silence once it does.
func TestHookAudit_warnsOnAMissingRootHook(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "go.mod", "module example.com/demo\n\ngo 1.27\n")
	writeTestFile(t, dir, ".rotini.spec.yaml", scSpec(true))
	writeTestFile(t, dir, ".rotini.conf.yaml", scConf)
	t.Chdir(dir)
	gen := func() []error {
		var notices []error
		if err := NewProcessor("0.0.0").Generate(".rotini.spec.yaml", ".rotini.conf.yaml", false,
			func(string, error) {}, func(n []error) { notices = append(notices, n...) }); err != nil {
			t.Fatalf("Generate: %v", err)
		}
		return notices
	}
	if notices := gen(); len(notices) != 0 {
		t.Fatalf("notices with the hook in place = %v, want none", notices)
	}

	path := filepath.Join(dir, "internal", "cmd", "demo", "demo.go")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	renamed := strings.Replace(string(body), "CascadingPreRun(ctx", "answerFlags(ctx", 1)
	if err := os.WriteFile(path, []byte(renamed), 0o600); err != nil {
		t.Fatal(err)
	}
	notices := gen()
	if len(notices) != 1 {
		t.Fatalf("notices = %v, want exactly one", notices)
	}
	got := notices[0].Error()
	for _, want := range []string{"internal/cmd/demo/demo.go:", "demoHandler has none", "short_circuit and cascading"} {
		if !strings.Contains(got, want) {
			t.Errorf("notice is missing %q:\n%s", want, got)
		}
	}
}
