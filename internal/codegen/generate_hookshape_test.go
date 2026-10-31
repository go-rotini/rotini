package codegen

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-rotini/rotini/internal/testmod"
)

// TestErrorHooks_stubsCompile generates each error-shaped stub case into a module on this
// rotini and vets it, so every seeded body (help, version, completion, secret, bare groups)
// type-checks as an ErrorHandler and the wiring wraps it.
func TestErrorHooks_stubsCompile(t *testing.T) {
	skipUnlessCompiling(t)
	cases, _ := filepath.Glob(filepath.Join("testdata", "stubshapes", "error-*"))
	if len(cases) == 0 {
		t.Fatal("no error-* stub cases")
	}
	for _, c := range cases {
		t.Run(filepath.Base(c), func(t *testing.T) {
			spec, err := os.ReadFile(filepath.Join(c, "spec.yaml"))
			if err != nil {
				t.Fatal(err)
			}
			conf, err := os.ReadFile(filepath.Join(c, "conf.yaml"))
			if err != nil {
				t.Fatal(err)
			}
			dir := hookShapeModule(t, string(spec), string(conf), nil)
			zz := readTestFile(t, filepath.Join(dir, "internal/cmd/demo/zz_demo.go"))
			if !strings.Contains(zz, "return rotini.FromErrorHandler(&demoHandler{})") {
				t.Errorf("the root's wiring is not wrapped:\n%s", zz)
			}
			vet(t, dir)
		})
	}
}

// TestErrorHooks_existingStubKeepsItsShape pins that the conf decides only new stubs: a void
// stub already on disk is wired as a Handler, while the stubs generate seeds now are
// ErrorHandlers, and the mixed package builds.
func TestErrorHooks_existingStubKeepsItsShape(t *testing.T) {
	skipUnlessCompiling(t)
	spec := readTestFile(t, filepath.Join("testdata", "stubshapes", "error-v1.4-seed", "spec.yaml"))
	conf := readTestFile(t, filepath.Join("testdata", "stubshapes", "error-v1.4-seed", "conf.yaml"))
	voidAdd := `package demo

import (
	"context"

	"github.com/go-rotini/rotini"
)

var _ rotini.Handler = (*demoRemoteAddHandler)(nil)

type demoRemoteAddHandler struct{ rotini.NoHooks }

func (*demoRemoteAddHandler) Run(ctx context.Context, rtx *rotini.Context) {}
`
	dir := hookShapeModule(t, spec, conf, map[string]string{"internal/cmd/demo/demo_remote_add.go": voidAdd})
	zz := readTestFile(t, filepath.Join(dir, "internal/cmd/demo/zz_demo.go"))
	for _, want := range []string{
		"return &demoRemoteAddHandler{}",
		"return rotini.FromErrorHandler(&demoRemoteHandler{})",
		"return rotini.FromErrorHandler(&demoHandler{})",
	} {
		if !strings.Contains(zz, want) {
			t.Errorf("wiring lacks %q:\n%s", want, zz)
		}
	}
	if got := readTestFile(t, filepath.Join(dir, "internal/cmd/demo/demo_remote_add.go")); got != voidAdd {
		t.Errorf("the existing stub was rewritten:\n%s", got)
	}
	vet(t, dir)
}

// TestErrorHooks_noConfKeepsVoidWiring pins that a project without hook_returns regenerates
// exactly as before: no stub or wiring line mentions the error shape.
func TestErrorHooks_noConfKeepsVoidWiring(t *testing.T) {
	spec := readTestFile(t, filepath.Join("testdata", "stubshapes", "v1.4-seed", "spec.yaml"))
	for name, body := range emitInModule(t, spec, stubShapesConf) {
		if strings.Contains(body, "ErrorHandler") || strings.Contains(body, "RunE") {
			t.Errorf("%s mentions the error shape:\n%s", name, body)
		}
	}
}

func TestHookShapes(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeTestFile(t, dir, "a.go", `package demo

type plain struct{}
func (*plain) Run(ctx, rtx any) {}

type errs struct{}
func (errs) PreRun(ctx, rtx any) error { return nil }
func (*errs) Run(ctx, rtx any) error { return nil }

type helper struct{}
func (helper) Other() error { return nil }
`)
	writeTestFile(t, dir, "off.go", "//go:build ignore\n\npackage demo\n\ntype gone struct{}\n\nfunc (gone) Run() error { return nil }\n")
	writeTestFile(t, dir, "a_test.go", "package demo\n\ntype tested struct{}\n\nfunc (tested) Run() error { return nil }\n")
	writeTestFile(t, dir, "broken.go", "package demo\n\nfunc (")
	shapes := hookShapes(dir)
	if returns, ok := shapes["plain"]; !ok || returns {
		t.Errorf("plain = %v, %v; want known and void", returns, ok)
	}
	if !shapes["errs"] {
		t.Error("errs is not an ErrorHandler")
	}
	for _, typ := range []string{"helper", "gone", "tested"} {
		if _, ok := shapes[typ]; ok {
			t.Errorf("%s was read as a handler type", typ)
		}
	}
	if !errorHooks(shapes, "unseeded", true) || errorHooks(shapes, "unseeded", false) || errorHooks(shapes, "plain", true) {
		t.Error("errorHooks: an unknown type follows the conf, a known one its methods")
	}
	if hookShapes(filepath.Join(dir, "missing")) != nil {
		t.Error("a missing dir has shapes")
	}
}

// hookShapeModule writes a module on this rotini with spec, conf and the given files, and
// generates it.
func hookShapeModule(t *testing.T, spec, conf string, files map[string]string) string {
	t.Helper()
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	writeTestFile(t, dir, "go.mod", "module example.com/demo\n\ngo 1.27\n\nrequire github.com/go-rotini/rotini v0.0.0\n\nreplace github.com/go-rotini/rotini => "+testmod.Quote(filepath.ToSlash(repoRoot))+"\n"+testmod.Directives(repoRoot))
	writeTestFile(t, dir, ".rotini.spec.yaml", spec)
	writeTestFile(t, dir, ".rotini.conf.yaml", conf)
	for name, body := range files {
		writeTestFile(t, dir, name, body)
	}
	t.Chdir(dir)
	if err := NewProcessor("0.0.0").Generate(".rotini.spec.yaml", ".rotini.conf.yaml", false, func(string, error) {}, func([]error) {}); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	return dir
}

func vet(t *testing.T, dir string) {
	t.Helper()
	if out, err := exec.Command("go", "mod", "tidy").CombinedOutput(); err != nil {
		t.Fatalf("go mod tidy: %v\n%s", err, out)
	}
	cmd := exec.Command("go", "vet", "./...")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go vet: %v\n%s", err, out)
	}
}
