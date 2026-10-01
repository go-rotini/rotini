package codegen

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestInitialize_endToEnd is the guard the `rotini init` breakage slipped past: every
// other test stops at "the files were written and validate", which a scaffold that
// cannot COMPILE still passes. This one runs the real initialize into a fresh module
// and then builds the result, so a bad seed conf, a stale template, a missing require,
// or a wrong runtime import path fails here rather than in a new user's terminal.
//
// It shells out to `go build`, so it is the slowest test in the package — but it covers
// the one path every single user walks first.
func TestInitialize_endToEnd(t *testing.T) {
	skipUnlessCompiling(t)

	// The scaffold imports github.com/go-rotini/rotini, which is THIS repo — two levels
	// up from internal/codegen. Resolve it before chdir'ing into the temp module.
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	writeTestFile(t, dir, "go.mod", "module example.com/demo\n\ngo 1.26\n\nrequire github.com/go-rotini/rotini v0.0.0\n\nreplace github.com/go-rotini/rotini => "+filepath.ToSlash(repoRoot)+"\n")
	t.Chdir(dir)

	if _, err := NewProcessor("0.0.0").Initialize("demo", "", false); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	// The scaffold's shape: the seed files, the entrypoint, the editable stub, and the
	// generated framework file. A missing one means a codegen step silently no-op'd.
	for _, want := range []string{
		"cmd/demo/.rotini.spec.yaml",
		"cmd/demo/.rotini.conf.yaml",
		"cmd/demo/main.go",
		"internal/cmd/demo/demo.go",
		"internal/cmd/demo/zz_rotini.go",
	} {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(want))); err != nil {
			t.Errorf("scaffold missing %s: %v", want, err)
		}
	}

	// The runtime is IMPORTED, never emitted — no copy of it may appear in the module.
	for _, gone := range []string{"internal/cmd/demo/rotini", "internal/demo"} {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(gone))); err == nil {
			t.Errorf("%s exists — the runtime must be imported, not emitted", gone)
		}
	}

	// `go mod tidy` resolves the runtime's own requirements (recon/fs/…) from the module
	// cache; the build is the real assertion.
	for _, args := range [][]string{{"mod", "tidy"}, {"build", "./..."}} {
		cmd := exec.Command("go", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("go %s in the scaffolded module: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
}

// initDemo runs a real initialize of "demo" into a fresh module and returns its root.
func initDemo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeTestFile(t, dir, "go.mod", "module example.com/demo\n\ngo 1.27\n")
	t.Chdir(dir)
	if _, err := NewProcessor("0.0.0").Initialize("demo", "", false); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	return dir
}

// TestInitialize_forceNeverDeletes: `init --force` replaces the spec with the seed, which may
// have fewer commands than the spec it replaces. Pruning then would delete those commands'
// handlers — edited ones included — without a word, so init never prunes.
func TestInitialize_forceNeverDeletes(t *testing.T) {
	dir := initDemo(t)
	handler := filepath.Join(dir, "internal", "cmd", "demo", "demo_extra.go")
	writeTestFile(t, filepath.Dir(handler), "demo_extra.go", "package demo\n\n"+stubMarker+"*demoExtraHandlers)(nil)\n\n// edited by hand\n")

	if _, err := NewProcessor("0.0.0").Initialize("demo", "", true); err != nil {
		t.Fatalf("Initialize --force: %v", err)
	}
	if _, err := os.Stat(handler); err != nil {
		t.Errorf("init --force deleted a handler: %v", err)
	}
}

// TestGenerate_reportsValidationWarnings: generate runs validate's checks first, so it reports
// the same warnings — before, it dropped them and printed nothing.
func TestGenerate_reportsValidationWarnings(t *testing.T) {
	dir := initDemo(t)
	conf := filepath.Join(dir, "cmd", "demo", ".rotini.conf.yaml")
	body, err := os.ReadFile(conf)
	if err != nil {
		t.Fatal(err)
	}
	// An embed_dir on a feature that is not embedded does nothing, which validate warns about.
	warned := strings.Replace(string(body), "    - type: help\n      enabled: true\n", "    - type: help\n      enabled: true\n      embed_dir: docs\n", 1)
	if warned == string(body) {
		t.Fatal("the seed conf no longer has the help feature this test edits")
	}
	writeTestFile(t, filepath.Dir(conf), ".rotini.conf.yaml", warned)

	p := NewProcessor("0.0.0")
	var validateWarnings, generateNotices []error
	spec := filepath.Join(dir, "cmd", "demo", ".rotini.spec.yaml")
	if err := p.Validate(spec, "", false, "", nil, func(w []error) { validateWarnings = append(validateWarnings, w...) }); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(validateWarnings) == 0 {
		t.Fatal("validate reported no warning for embed_dir on an inline feature; pick another warning")
	}
	if err := p.Generate(spec, "", false, nil, func(n []error) { generateNotices = append(generateNotices, n...) }); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	for _, w := range validateWarnings {
		found := false
		for _, n := range generateNotices {
			found = found || n.Error() == w.Error()
		}
		if !found {
			t.Errorf("generate did not report validate's warning %q", w)
		}
	}
}

// TestGenerate_missingExplicitConfIsAnError: a conf path the user typed must exist. Quietly
// generating with the defaults instead is how a typo in --config went unnoticed.
func TestGenerate_missingExplicitConfIsAnError(t *testing.T) {
	dir := initDemo(t)
	spec := filepath.Join(dir, "cmd", "demo", ".rotini.spec.yaml")
	err := NewProcessor("0.0.0").Validate(spec, filepath.Join(dir, "nosuch.yaml"), false, "", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "nosuch.yaml") {
		t.Errorf("Validate with a missing --config = %v, want an error naming it", err)
	}
}

// TestGenerate_findsTheConfBesideTheSpec: with no conf given, the one beside the spec is read —
// not a .rotini.conf.yaml in the working directory, and not the defaults.
func TestGenerate_findsTheConfBesideTheSpec(t *testing.T) {
	initDemo(t)
	spec, conf, err := ResolvePaths(filepath.Join("cmd", "demo", ".rotini.spec.yaml"), "")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join("cmd", "demo", ".rotini.conf.yaml"); conf != want {
		t.Errorf("ResolvePaths(%s) conf = %q, want %q", spec, conf, want)
	}
}
