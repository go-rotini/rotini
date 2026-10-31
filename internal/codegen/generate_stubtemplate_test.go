package codegen

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// The data a stub_template gets is a documented contract, pinned as golden files: the template
// testdata/stubtemplate/data/template prints every field for each command of spec.yaml, and
// want/ holds what it rendered. Refresh with
// `go test ./internal/codegen -run StubTemplateData -update-stubtemplate`.
var updateStubTemplate = flag.Bool("update-stubtemplate", false, "rewrite the stub_template data golden files")

// stubTemplateConf is a conf whose cmd target sets the stub_template and stub_test_template
// given (each "" for none), with the help feature on.
func stubTemplateConf(stub, test string) string {
	conf := "version: 0.0.0\ngenerate:\n  packages:\n    - type: cmd\n      file: internal/cmd/demo/zz_demo.go\n      package: demo\n      hook_returns: error\n"
	if stub != "" {
		conf += "      stub_template: " + stub + "\n"
	}
	if test != "" {
		conf += "      stub_test_template: " + test + "\n"
	}
	return conf + "  features:\n    - type: help\n      enabled: true\n"
}

const stubTemplateSpec = `version: 0.0.0
command:
  name: demo
  summary: a demo
  commands:
    - name: ship
      summary: ship it
      arguments:
        - name: target
          schema: { type: string }
`

// teamStub is a team's handler stub: a logger field and a wrapped error.
const teamStub = `package {{.Package}}

import (
	"context"
	"fmt"
	"log/slog"

	{{.RuntimeImport}}
)

var _ rotini.ErrorHandler = (*{{.HandlerType}})(nil)

// {{.HandlerType}} runs ` + "`{{.Invocation}}`" + `.
type {{.HandlerType}} struct {
	rotini.NoCascadingPreRunE
	rotini.NoPreRunE
	rotini.NoPostRunE
	rotini.NoCascadingPostRunE
	log *slog.Logger
}

func (h *{{.HandlerType}}) Run(ctx context.Context, rtx *rotini.Context) error {
	if _, err := rtx.Inputs[{{.InputsType}}](); err != nil {
		return fmt.Errorf("{{.Invocation}}: %w", err)
	}
	return nil
}
`

const teamTest = `package {{.Package}}

import "testing"

func Test{{title .HandlerType}}(t *testing.T) {
	var _ = &{{.HandlerType}}{}
}
`

// stubTemplateModule writes a module holding stubTemplateSpec, a conf naming the templates
// written under templates/ (an empty body writes none), and returns its directory after a
// generate, with the processor's error.
func stubTemplateModule(t *testing.T, stub, test string) (string, error) {
	t.Helper()
	dir := t.TempDir()
	writeTestFile(t, dir, "go.mod", "module example.com/demo\n\ngo 1.27\n")
	writeTestFile(t, dir, ".rotini.spec.yaml", stubTemplateSpec)
	var stubPath, testPath string
	if stub != "" {
		stubPath = "templates/stub.go.tmpl"
		writeTestFile(t, dir, stubPath, stub)
	}
	if test != "" {
		testPath = "templates/stub_test.go.tmpl"
		writeTestFile(t, dir, testPath, test)
	}
	writeTestFile(t, dir, ".rotini.conf.yaml", stubTemplateConf(stubPath, testPath))
	t.Chdir(dir)
	return dir, generateHere(t)
}

// generateHere runs generate in the working directory and returns its error.
func generateHere(t *testing.T) error {
	t.Helper()
	var genErr error
	if err := NewProcessor("0.0.0").Generate(".rotini.spec.yaml", ".rotini.conf.yaml", false, func(_ string, e error) {
		if e != nil {
			genErr = e
		}
	}, func([]error) {}); err != nil {
		return err
	}
	return genErr
}

// TestStubTemplate_rendersNewStubsAndTests pins that the author's templates shape every new
// handler file and the test beside it, formatted, with the header on top and rotini's note on
// the test.
func TestStubTemplate_rendersNewStubsAndTests(t *testing.T) {
	dir, err := stubTemplateModule(t, teamStub, teamTest)
	if err != nil {
		t.Fatal(err)
	}
	cmdDir := filepath.Join(dir, "internal", "cmd", "demo")
	stub := readText(t, filepath.Join(cmdDir, "demo_ship.go"))
	for _, want := range []string{"log *slog.Logger", "// demoShipHandler runs `demo ship`.", `return fmt.Errorf("demo ship: %w", err)`, "rtx.Inputs[DemoShipInputs]()"} {
		if !strings.Contains(stub, want) {
			t.Errorf("demo_ship.go lacks %q:\n%s", want, stub)
		}
	}
	// Imports are grouped as a built-in stub's are.
	if !strings.Contains(stub, "\"log/slog\"\n\n\t\"github.com/go-rotini/rotini\"") {
		t.Errorf("demo_ship.go imports aren't grouped:\n%s", stub)
	}
	test := readText(t, filepath.Join(cmdDir, "demo_ship_test.go"))
	if !strings.HasPrefix(test, seededTestNote+"\n\npackage demo\n") || !strings.Contains(test, "func TestDemoShipHandler(t *testing.T)") {
		t.Errorf("demo_ship_test.go =\n%s\nwant rotini's note, then the template's test", test)
	}
	if _, err := os.Stat(filepath.Join(cmdDir, "demo_test.go")); err != nil {
		t.Errorf("the root got no seeded test: %v", err)
	}
}

// TestStubTemplate_header pins that the conf header goes above an author stub and its test, as
// it does above a built-in stub.
func TestStubTemplate_header(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "go.mod", "module example.com/demo\n\ngo 1.27\n")
	writeTestFile(t, dir, ".rotini.spec.yaml", stubTemplateSpec)
	writeTestFile(t, dir, "stub.tmpl", teamStub)
	writeTestFile(t, dir, "test.tmpl", teamTest)
	conf := strings.Replace(stubTemplateConf("stub.tmpl", "test.tmpl"), "      package: demo\n", "      package: demo\n      header: \"// Copyright Acme\"\n", 1)
	writeTestFile(t, dir, ".rotini.conf.yaml", conf)
	t.Chdir(dir)
	if err := generateHere(t); err != nil {
		t.Fatal(err)
	}
	cmdDir := filepath.Join(dir, "internal", "cmd", "demo")
	if got := readText(t, filepath.Join(cmdDir, "demo_ship.go")); !strings.HasPrefix(got, "// Copyright Acme\n\npackage demo\n") {
		t.Errorf("demo_ship.go =\n%s\nwant the header on top", got)
	}
	if got := readText(t, filepath.Join(cmdDir, "demo_ship_test.go")); !strings.HasPrefix(got, "// Copyright Acme\n\n"+seededTestNote+"\n\npackage demo\n") {
		t.Errorf("demo_ship_test.go =\n%s\nwant the header, then rotini's note", got)
	}
}

// TestStubTemplate_errors pins that a template that is missing, fails to parse or run, or
// renders something other than Go stops generate, naming the conf key, the template and its line.
func TestStubTemplate_errors(t *testing.T) {
	cases := []struct {
		name, stub, test string
		want             []string
	}{
		{"parse", "package {{.Package}}\n\n{{if .ErrorHooks}}\n", "", []string{"stub_template: template: templates/stub.go.tmpl:4: unexpected EOF"}},
		{"unknown field", "package {{.Package}}\n\n// {{.Logger}}\n", "", []string{"stub_template: template: templates/stub.go.tmpl:3:5: executing", "can't evaluate field Logger"}},
		{"not go", "package {{.Package}}\n\nfunc {\n", "", []string{"stub_template templates/stub.go.tmpl: what it renders for demo.go is not valid Go: 3:6:", "--- rendered source ---"}},
		{"test parse", teamStub, "package {{.Package\n", []string{"stub_test_template: template: templates/stub_test.go.tmpl:2: unclosed action"}},
		{"test not go", teamStub, "package {{.Package}}\n\nvar = 1\n", []string{"stub_test_template templates/stub_test.go.tmpl: what it renders for demo_test.go is not valid Go:"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir, err := stubTemplateModule(t, tc.stub, tc.test)
			if err == nil {
				t.Fatal("generate succeeded")
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error = %v\nwant it to contain %q", err, want)
				}
			}
			// Nothing of the cmd package is written when a stub fails to render.
			if _, err := os.Stat(filepath.Join(dir, "internal", "cmd", "demo")); !os.IsNotExist(err) {
				t.Errorf("the cmd package was written: %v", err)
			}
		})
	}
}

// TestStubTemplate_missingFile pins the error for a stub_template path with no file behind it.
func TestStubTemplate_missingFile(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "go.mod", "module example.com/demo\n\ngo 1.27\n")
	writeTestFile(t, dir, ".rotini.spec.yaml", stubTemplateSpec)
	writeTestFile(t, dir, ".rotini.conf.yaml", stubTemplateConf("", "templates/nope.tmpl"))
	t.Chdir(dir)
	err := generateHere(t)
	if err == nil || !strings.Contains(err.Error(), "stub_test_template: no file at templates/nope.tmpl (the path is relative to the module root)") {
		t.Errorf("error = %v", err)
	}
}

// TestStubTemplate_createOnce pins that the templates shape only new files: an existing handler
// file and an existing test are never rewritten, and no test is seeded beside a handler file
// that was already there.
func TestStubTemplate_createOnce(t *testing.T) {
	dir, err := stubTemplateModule(t, teamStub, teamTest)
	if err != nil {
		t.Fatal(err)
	}
	cmdDir := filepath.Join(dir, "internal", "cmd", "demo")
	const ownStub = "package demo\n\n// mine\n"
	const ownTest = "package demo\n\n// my test\n"
	writeTestFile(t, cmdDir, "demo_ship.go", strings.Replace(readText(t, filepath.Join(cmdDir, "demo_ship.go")), "log *slog.Logger", "log *slog.Logger // edited", 1))
	writeTestFile(t, cmdDir, "demo_test.go", ownTest)
	if err := os.Remove(filepath.Join(cmdDir, "demo_ship_test.go")); err != nil {
		t.Fatal(err)
	}
	// A command added now gets a test, since its handler file is new; a handler file written by
	// hand before its command arrives is left alone, and gets no test.
	writeTestFile(t, dir, ".rotini.spec.yaml", stubTemplateSpec+"    - name: pack\n      summary: pack it\n    - name: load\n      summary: load it\n")
	writeTestFile(t, cmdDir, "demo_load.go", ownStub)
	if err := generateHere(t); err != nil {
		t.Fatal(err)
	}
	if got := readText(t, filepath.Join(cmdDir, "demo_ship.go")); !strings.Contains(got, "// edited") {
		t.Errorf("an existing handler file was rewritten:\n%s", got)
	}
	if got := readText(t, filepath.Join(cmdDir, "demo_test.go")); got != ownTest {
		t.Errorf("an existing test was rewritten:\n%s", got)
	}
	if got := readText(t, filepath.Join(cmdDir, "demo_load.go")); got != ownStub {
		t.Errorf("a hand-written handler file was rewritten:\n%s", got)
	}
	for _, name := range []string{"demo_ship_test.go", "demo_load_test.go"} {
		if _, err := os.Stat(filepath.Join(cmdDir, name)); !os.IsNotExist(err) {
			t.Errorf("%s was seeded beside a handler file that already existed", name)
		}
	}
	if got := readText(t, filepath.Join(cmdDir, "demo_pack_test.go")); !isSeededTest([]byte(got)) {
		t.Errorf("the new command's test =\n%s", got)
	}
}

// TestStubTemplate_pruning pins that a seeded test goes with its handler file: disabled with it,
// deleted with it, and restored with it when its command comes back. A test without rotini's
// note is the author's, and stays.
func TestStubTemplate_pruning(t *testing.T) {
	dir, err := stubTemplateModule(t, teamStub, teamTest)
	if err != nil {
		t.Fatal(err)
	}
	cmdDir := filepath.Join(dir, "internal", "cmd", "demo")
	shipTest := filepath.Join(cmdDir, "demo_ship_test.go")
	rootTest := filepath.Join(cmdDir, "demo_test.go")
	// The root's test is claimed by the author: its note is gone.
	writeTestFile(t, cmdDir, "demo_test.go", strings.Replace(readText(t, rootTest), seededTestNote+"\n", "", 1))
	claimed := readText(t, rootTest)
	seeded := readText(t, shipTest)

	without := "version: 0.0.0\ncommand:\n  name: demo\n  summary: a demo\n"
	writeTestFile(t, dir, ".rotini.spec.yaml", without)
	if err := generateHere(t); err != nil {
		t.Fatal(err)
	}
	if got := readText(t, shipTest); !stubDisabled([]byte(got)) || !strings.HasSuffix(got, seeded) {
		t.Errorf("after the first generate, demo_ship_test.go =\n%s\nwant it disabled", got)
	}

	// The command comes back before the second generate: both return to the build.
	writeTestFile(t, dir, ".rotini.spec.yaml", stubTemplateSpec)
	if err := generateHere(t); err != nil {
		t.Fatal(err)
	}
	if got := readText(t, shipTest); got != seeded {
		t.Errorf("after the command came back, demo_ship_test.go =\n%s\nwant it as seeded", got)
	}

	writeTestFile(t, dir, ".rotini.spec.yaml", without)
	for range 2 {
		if err := generateHere(t); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"demo_ship.go", "demo_ship_test.go"} {
		if _, err := os.Stat(filepath.Join(cmdDir, name)); !os.IsNotExist(err) {
			t.Errorf("%s survived the second generate", name)
		}
	}
	if got := readText(t, rootTest); got != claimed {
		t.Errorf("the author's test was changed:\n%s", got)
	}
}

// TestPruneGoDir_seededTest pins each step pruning reports for a seeded test.
func TestPruneGoDir_seededTest(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "demo_ship.go", orphanStub)
	writeTestFile(t, dir, "demo_ship_test.go", seededTestNote+"\n\npackage demo\n")
	var notes []string
	for range 2 {
		if err := pruneGoDir(newPlanner(false), dir, map[string]bool{}, true, func(n string) { notes = append(notes, n) }); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{
		"demo_ship.go (disabled; removed by the next generate)", "demo_ship_test.go (disabled; removed by the next generate)",
		"demo_ship.go", "demo_ship_test.go",
	}
	if !slices.Equal(notes, want) {
		t.Errorf("notes = %q, want %q", notes, want)
	}
}

// TestStubTemplate_wiringFollowsTheTemplate pins that the wiring of a stub written this pass
// follows the methods its template declares, not hook_returns: the conf says error, the
// template seeds a rotini.Handler.
func TestStubTemplate_wiringFollowsTheTemplate(t *testing.T) {
	const plain = `package {{.Package}}

import (
	"context"

	{{.RuntimeImport}}
)

var _ rotini.Handler = (*{{.HandlerType}})(nil)

type {{.HandlerType}} struct {
	rotini.NoCascadingPreRun
	rotini.NoPreRun
	rotini.NoPostRun
	rotini.NoCascadingPostRun
}

func (*{{.HandlerType}}) Run(ctx context.Context, rtx *rotini.Context) {}
`
	dir, err := stubTemplateModule(t, plain, "")
	if err != nil {
		t.Fatal(err)
	}
	gen := readText(t, filepath.Join(dir, "internal", "cmd", "demo", "zz_demo.go"))
	if strings.Contains(gen, "FromErrorHandler(&demoShipHandler") {
		t.Errorf("the wiring wraps a rotini.Handler stub as an ErrorHandler:\n%s", gen)
	}
	if !strings.Contains(gen, "&demoShipHandler{}") {
		t.Errorf("the wiring doesn't return the stub:\n%s", gen)
	}
}

// TestStubTemplate_warnsWithoutMarker pins the notice for a template whose stubs generate can't
// recognize as its own, so would never retire.
func TestStubTemplate_warnsWithoutMarker(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "go.mod", "module example.com/demo\n\ngo 1.27\n")
	writeTestFile(t, dir, ".rotini.spec.yaml", stubTemplateSpec)
	writeTestFile(t, dir, "stub.tmpl", strings.Replace(teamStub, "var _ rotini.ErrorHandler = (*{{.HandlerType}})(nil)\n", "", 1))
	writeTestFile(t, dir, ".rotini.conf.yaml", stubTemplateConf("stub.tmpl", ""))
	t.Chdir(dir)
	var warnings []error
	if err := NewProcessor("0.0.0").Generate(".rotini.spec.yaml", ".rotini.conf.yaml", false, func(_ string, e error) {
		if e != nil {
			t.Fatal(e)
		}
	}, func(w []error) { warnings = append(warnings, w...) }); err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, w := range warnings {
		found = found || strings.Contains(w.Error(), "stub_template stub.tmpl renders no `var _ rotini.ErrorHandler") && strings.Contains(w.Error(), "(demo.go, demo_ship.go)")
	}
	if !found {
		t.Errorf("warnings = %v, want the missing-marker notice", warnings)
	}
}

// TestStubTemplateData pins the template data for each kind of command a spec can declare.
func TestStubTemplateData(t *testing.T) {
	dir, err := filepath.Abs("testdata/stubtemplate/data")
	if err != nil {
		t.Fatal(err)
	}
	spec := readText(t, filepath.Join(dir, "spec.yaml"))
	tmpl := readText(t, filepath.Join(dir, "template"))
	conf := strings.Replace(stubShapesConf, "      package: demo\n", "      package: demo\n      hook_returns: error\n      stub_template: stub.go.tmpl\n", 1) +
		"    - type: completion\n      enabled: true\n"
	m := t.TempDir()
	writeTestFile(t, m, "go.mod", "module example.com/demo\n\ngo 1.27\n")
	writeTestFile(t, m, ".rotini.spec.yaml", spec)
	writeTestFile(t, m, ".rotini.conf.yaml", conf)
	writeTestFile(t, m, "stub.go.tmpl", tmpl)
	t.Chdir(m)
	if err := generateHere(t); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for name, body := range collectGoFiles(t, m) {
		if strings.HasPrefix(name, "internal/") && !strings.HasPrefix(filepath.Base(name), "zz_") {
			got[filepath.Base(name)] = body
		}
	}
	want := filepath.Join(dir, "want")
	if *updateStubTemplate {
		if err := os.RemoveAll(want); err != nil {
			t.Fatal(err)
		}
		for name, body := range got {
			writeTestFile(t, want, name, body)
		}
		return
	}
	compareTree(t, want, got)
}

// TestStubTemplateData_documented pins that the stub_template description documents every field
// of the template's data, and that the data golden's template prints each one.
func TestStubTemplateData_documented(t *testing.T) {
	var schema struct {
		Definitions map[string]struct {
			AllOf []struct {
				Properties map[string]struct {
					Description string `json:"description"`
				} `json:"properties"`
			} `json:"allOf"`
		} `json:"definitions"`
	}
	if err := json.Unmarshal([]byte(readText(t, "schema-conf.json")), &schema); err != nil {
		t.Fatal(err)
	}
	desc := schema.Definitions["PackageConfig"].AllOf[0].Properties["stub_template"].Description
	tmpl := readText(t, "testdata/stubtemplate/data/template")
	typ := reflect.TypeFor[templateHandlerData]()
	for field := range typ.Fields() {
		name := "." + field.Name
		if !regexp.MustCompile(regexp.QuoteMeta(name) + `\b`).MatchString(desc) {
			t.Errorf("the stub_template description doesn't document %s", name)
		}
		if !strings.Contains(tmpl, name+"}}") {
			t.Errorf("the data golden's template doesn't print %s", name)
		}
	}
}
