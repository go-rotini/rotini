package codegen

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestInitialize_endToEnd runs a real initialize into a fresh module and pins that the
// scaffold has the expected files and compiles.
func TestInitialize_endToEnd(t *testing.T) {
	skipUnlessCompiling(t)

	// The scaffold imports this repo; resolve its root before chdir'ing into the temp module.
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	writeTestFile(t, dir, "go.mod", "module example.com/demo\n\ngo 1.26\n\nrequire github.com/go-rotini/rotini v0.0.0\n\nreplace github.com/go-rotini/rotini => "+filepath.ToSlash(repoRoot)+"\n")
	t.Chdir(dir)

	if _, err := NewProcessor("0.0.0").Initialize("demo", InitOptions{}); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

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

	// The runtime is imported, never emitted.
	for _, gone := range []string{"internal/cmd/demo/rotini", "internal/demo"} {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(gone))); err == nil {
			t.Errorf("%s exists — the runtime must be imported, not emitted", gone)
		}
	}

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
	if _, err := NewProcessor("0.0.0").Initialize("demo", InitOptions{}); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	return dir
}

// TestInitialize_forceNeverDeletes pins that `init --force` does not prune handlers for
// commands the seed lacks.
func TestInitialize_forceNeverDeletes(t *testing.T) {
	dir := initDemo(t)
	handler := filepath.Join(dir, "internal", "cmd", "demo", "demo_extra.go")
	writeTestFile(t, filepath.Dir(handler), "demo_extra.go", "package demo\n\n"+stubMarker+"*demoExtraHandler)(nil)\n\n// edited by hand\n")

	if _, err := NewProcessor("0.0.0").Initialize("demo", InitOptions{Force: true}); err != nil {
		t.Fatalf("Initialize --force: %v", err)
	}
	if _, err := os.Stat(handler); err != nil {
		t.Errorf("init --force deleted a handler: %v", err)
	}
}

// TestGenerate_reportsValidationWarnings pins that generate reports validate's warnings as
// notices.
func TestGenerate_reportsValidationWarnings(t *testing.T) {
	dir := initDemo(t)
	conf := filepath.Join(dir, "cmd", "demo", ".rotini.conf.yaml")
	body, err := os.ReadFile(conf)
	if err != nil {
		t.Fatal(err)
	}
	// embed_dir on a non-embedded feature triggers a validate warning.
	warned := strings.Replace(string(body), "    - type: help\n      enabled: true\n", "    - type: help\n      enabled: true\n      embed_dir: docs\n", 1)
	if warned == string(body) {
		t.Fatal("the seed conf no longer has the help feature this test edits")
	}
	writeTestFile(t, filepath.Dir(conf), ".rotini.conf.yaml", warned)

	p := NewProcessor("0.0.0")
	var validateWarnings, generateNotices []error
	spec := filepath.Join(dir, "cmd", "demo", ".rotini.spec.yaml")
	if err := p.Validate(spec, "", false, "", "", nil, func(w []error) { validateWarnings = append(validateWarnings, w...) }); err != nil {
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

// TestGenerate_missingExplicitConfIsAnError pins that an explicit conf path that does not
// exist is an error, not a fallback to defaults.
func TestGenerate_missingExplicitConfIsAnError(t *testing.T) {
	dir := initDemo(t)
	spec := filepath.Join(dir, "cmd", "demo", ".rotini.spec.yaml")
	err := NewProcessor("0.0.0").Validate(spec, filepath.Join(dir, "nosuch.yaml"), false, "", "", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "nosuch.yaml") {
		t.Errorf("Validate with a missing --config = %v, want an error naming it", err)
	}
}

// TestGenerate_findsTheConfBesideTheSpec pins that, with no conf given, the conf beside the
// spec is used rather than one in the working directory.
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

// TestInitialize_templates pins that every template's seeds validate and generate in every
// format, writing nothing in a dry run.
func TestInitialize_templates(t *testing.T) {
	names := map[string]string{"plugin": "kubectl-hello", "daemon": "demo", "suite": "demo"}
	for _, template := range initTemplateNames {
		for _, format := range []string{"yaml", "json", "jsonc", "toml"} {
			t.Run(template+"/"+format, func(t *testing.T) {
				dir := t.TempDir()
				writeTestFile(t, dir, "go.mod", "module example.com/demo\n\ngo 1.27\n")
				t.Chdir(dir)
				out, err := NewProcessor("0.0.0").InitializeDryRun(names[template], InitOptions{Format: format, Template: template})
				if err != nil {
					t.Fatalf("InitializeDryRun: %v", err)
				}
				if want := "cmd/" + names[template] + "/.rotini.spec." + format; filepath.ToSlash(out.Spec) != want {
					t.Errorf("Spec = %q, want %q", out.Spec, want)
				}
				if template == "suite" && len(out.Also) != 2 {
					t.Errorf("Also = %v, want the shared child and the admin binary", out.Also)
				}
				if entries, err := os.ReadDir(dir); err != nil || len(entries) != 1 {
					t.Errorf("a dry run wrote files: %v", entries)
				}
			})
		}
	}
}

// TestInitialize_templateRefusals pins the names a template refuses.
func TestInitialize_templateRefusals(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "go.mod", "module example.com/demo\n\ngo 1.27\n")
	t.Chdir(dir)
	for _, c := range []struct{ name, template, want string }{
		{"hello", "plugin", "<host>-<plugin>"},
		{"kubectl-", "plugin", "<host>-<plugin>"},
		{"status", "suite", `can't be named "status"`},
		{"demo", "nope", `unknown template "nope"`},
	} {
		_, err := NewProcessor("0.0.0").InitializeDryRun(c.name, InitOptions{Template: c.template})
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("init %s --template %s = %v, want an error containing %q", c.name, c.template, err, c.want)
		}
	}
}
