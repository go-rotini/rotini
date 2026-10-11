package codegen

import (
	"path/filepath"
	"strings"
	"testing"
)

const namedExitSpec = `version: 0.0.0
command:
  name: acme
  exit_status:
    - { code: 0, summary: ok }
    - { code: 1, summary: failed }
    - { code: 4, name: busy, summary: the store is locked, retryable: true }
  commands:
    - name: get
      exit_status:
        - { code: 3, name: not_found, summary: no such item }
        - { code: 5, name: api_error, summary: the API failed }
    - name: add
      exit_status:
        - { code: 6, name: conflict, summary: it exists already }
`

const modelsConf = `version: 0.0.0
generate:
  packages:
    - type: main
      file: cmd/acme/main.go
      package: main
    - type: cmd
      file: internal/cmd/acme/zz_acme.go
      package: acme
    - type: models
      file: internal/models/zz_models.go
      package: models
`

// TestExitConstants pins one constant per named exit code in the cmd file, named after the
// command, and a module that builds with them.
func TestExitConstants(t *testing.T) {
	dir, _ := emitModule(t, namedExitSpec, outputConf)
	cmd := squash(readEmitted(t, dir, "internal/cmd/acme/zz_acme.go"))
	for _, want := range []string{
		"AcmeExitBusy = 4",
		"AcmeGetExitNotFound = 3",
		"AcmeGetExitAPIError = 5",
		"AcmeAddExitConflict = 6",
	} {
		if !strings.Contains(cmd, want) {
			t.Errorf("the cmd file lacks %q", want)
		}
	}
	skipUnlessCompiling(t)
	if out, err := goBuild(t, dir); err != nil {
		t.Fatalf("the generated module does not build:\n%s", out)
	}
}

// TestExitConstants_models pins that with a models package the constants live there, and the
// cmd file re-exports them, so handler code reads the same either way.
func TestExitConstants_models(t *testing.T) {
	dir, _ := emitModule(t, namedExitSpec, modelsConf)
	models := squash(readEmitted(t, dir, "internal/models/zz_models.go"))
	cmd := squash(readEmitted(t, dir, "internal/cmd/acme/zz_acme.go"))
	if !strings.Contains(models, "AcmeGetExitNotFound = 3") {
		t.Errorf("the models file lacks the constant:\n%s", models)
	}
	if !strings.Contains(cmd, "AcmeGetExitNotFound = models.AcmeGetExitNotFound") {
		t.Errorf("the cmd file does not re-export the constant:\n%s", cmd)
	}
	skipUnlessCompiling(t)
	if out, err := goBuild(t, dir); err != nil {
		t.Fatalf("the generated module does not build:\n%s", out)
	}
}

// TestExitConstants_none pins that a spec naming no code renders no const block.
func TestExitConstants_none(t *testing.T) {
	files := emitInModule(t, `version: 0.0.0
command:
  name: demo
  exit_status:
    - { code: 0, summary: ok }
`, goldenConf)
	if cmd := files["internal/cmd/demo/zz_demo.go"]; strings.Contains(cmd, "Named exit codes") {
		t.Errorf("a const block was rendered with no named code:\n%s", cmd)
	}
}

// TestDuplicateDecls pins that a generated name made twice fails generate with a message
// naming the spec entry, instead of writing a file that does not compile.
func TestDuplicateDecls(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "go.mod", "module example.com/demo\n\ngo 1.27\n")
	// `demo get`'s exit name `inputs` makes DemoGetExitInputs, which is also the inputs type of
	// the command `demo get exit`.
	writeTestFile(t, dir, ".rotini.spec.yaml", `version: 0.0.0
command:
  name: demo
  commands:
    - name: get
      exit_status:
        - { code: 3, name: inputs, summary: bad inputs }
      commands:
        - name: exit
          summary: a sub-command named exit
`)
	writeTestFile(t, dir, ".rotini.conf.yaml", goldenConf)
	t.Chdir(dir)
	err := NewProcessor("0.0.0").Generate(".rotini.spec.yaml", ".rotini.conf.yaml", false, nil, nil)
	if err == nil || !strings.Contains(err.Error(), `DemoGetExitInputs is declared twice: the exit_status name "inputs" of "demo get"`) {
		t.Fatalf("Generate = %v, want the collision named", err)
	}
}

// TestDuplicateDecls_report pins the message for names no spec entry is known for.
func TestDuplicateDecls_report(t *testing.T) {
	src := []byte("package p\n\ntype A struct{}\n\nvar A = 1\n\nfunc (A) M() {}\n\nfunc M() {}\n")
	err := duplicateDecls("x.go", nil, src)
	if err == nil || !strings.Contains(err.Error(), "x.go: the generated name A is declared twice; two spec entries") {
		t.Errorf("duplicateDecls = %v", err)
	}
	if err := duplicateDecls("x.go", nil, []byte("package p\n\nfunc (T) M() {}\n\nfunc M() {}\n")); err != nil {
		t.Errorf("a method and a function of one name are not a collision: %v", err)
	}
}

// namedExitFixture generates namedExitSpec under conf and returns the cmd package directory
// and a re-generate func returning the exit-code warnings; dry runs the pass when asked.
func namedExitFixture(t *testing.T, conf string) (cmdDir string, gen func(dry bool) []string) {
	t.Helper()
	dir := t.TempDir()
	writeTestFile(t, dir, "go.mod", "module example.com/acme\n\ngo 1.27\n")
	writeTestFile(t, dir, ".rotini.spec.yaml", namedExitSpec)
	writeTestFile(t, dir, ".rotini.conf.yaml", conf)
	t.Chdir(dir)
	gen = func(dry bool) []string {
		var notices []error
		collect := func(n []error) { notices = append(notices, n...) }
		var err error
		if dry {
			_, err = NewProcessor("0.0.0").GenerateDryRun(".rotini.spec.yaml", ".rotini.conf.yaml", collect)
		} else {
			err = NewProcessor("0.0.0").Generate(".rotini.spec.yaml", ".rotini.conf.yaml", false, nil, collect)
		}
		if err != nil {
			t.Fatalf("Generate: %v", err)
		}
		var got []string
		for _, n := range notices {
			if strings.Contains(n.Error(), "exits with") {
				got = append(got, n.Error())
			}
		}
		return got
	}
	gen(false)
	return filepath.Join(dir, "internal", "cmd", "acme"), gen
}

// TestExitAudit_namedConstants pins how the audit reads generated constants: its own command's
// is listed, another command's is reported even when the code is listed here too, and an
// undeclared value is still caught.
func TestExitAudit_namedConstants(t *testing.T) {
	cmdDir, gen := namedExitFixture(t, outputConf)
	appendToStub(t, cmdDir, "acme_get.go", `
func (*acmeGetHandler) PostRun(ctx context.Context, rtx *rotini.Context) {
	rtx.HaltWithCode(AcmeGetExitNotFound)
	rtx.HaltWithCode(AcmeAddExitConflict)
	rtx.Exit(AcmeGetExitAPIError)
}
`)
	appendToStub(t, cmdDir, "acme.go", `
func (*acmeHandler) CascadingPostRun(ctx context.Context, rtx *rotini.Context) {
	rtx.HaltWithCode(AcmeExitBusy)
}
`)
	for _, dry := range []bool{false, true} {
		got := gen(dry)
		if len(got) != 1 || !strings.HasSuffix(got[0], `"acme get" exits with AcmeAddExitConflict, which belongs to "acme add"; declare the code on this command and use its own constant`) {
			t.Errorf("dry=%v: warnings = %q, want only the other command's constant", dry, got)
		}
	}
}

// TestExitAudit_modelsSelector pins that a handler qualifying the constant with the models
// package is read the same way.
func TestExitAudit_modelsSelector(t *testing.T) {
	cmdDir, gen := namedExitFixture(t, modelsConf)
	writeTestFile(t, cmdDir, "extra.go", `package acme

import (
	m "example.com/acme/internal/models"

	"github.com/go-rotini/rotini"
)

func (*acmeAddHandler) bail(rtx *rotini.Context) {
	rtx.HaltWithCode(m.AcmeAddExitConflict)
	rtx.HaltWithCode(m.AcmeGetExitNotFound)
}
`)
	got := gen(false)
	if len(got) != 1 || !strings.Contains(got[0], `"acme add" exits with AcmeGetExitNotFound, which belongs to "acme get"`) {
		t.Errorf("warnings = %q, want the other command's constant through the models package", got)
	}
}

// squash collapses runs of white space, so a check doesn't depend on gofmt's alignment.
func squash(s string) string { return strings.Join(strings.Fields(s), " ") }
