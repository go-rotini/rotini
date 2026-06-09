package internal

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// mustWriteSpec/mustWriteConf seed a fixture document at path, failing the test on
// error. The serialization is chosen from path's extension (so a ".toml" path
// exercises the TOML writer).
func mustWriteSpec(t *testing.T, path string, s *Spec) {
	t.Helper()
	if err := writeSpec(path, s); err != nil {
		t.Fatalf("seed spec %s: %v", path, err)
	}
}

func mustWriteConf(t *testing.T, path string, c *Conf) {
	t.Helper()
	if err := writeConf(path, c); err != nil {
		t.Fatalf("seed conf %s: %v", path, err)
	}
}

// newProcessor builds a processor and fails the test if schema compilation fails.
func newProcessor(t *testing.T, specPath, confPath string) *processor {
	t.Helper()
	p, err := NewProcessor(specPath, confPath, "")
	if err != nil {
		t.Fatalf("NewProcessor: %v", err)
	}
	return p
}

// TestLoad_explicitSpec_confBesideSpec covers the common path: an explicit spec
// path and a conf discovered beside it via the fallback locations.
func TestLoad_explicitSpec_confBesideSpec(t *testing.T) {
	dir := t.TempDir()
	specPath := filepath.Join(dir, "myspec.yaml")
	mustWriteSpec(t, specPath, &Spec{Schema: "https://x/spec.json", Command: Command{Name: "demo"}})
	confPath := filepath.Join(dir, ".rotini.conf.yaml")
	mustWriteConf(t, confPath, &Conf{Schema: "https://x/conf.json"})

	p := newProcessor(t, specPath, "")
	if err := p.load(); err != nil {
		t.Fatalf("load: %v", err)
	}

	if p.spec == nil || p.spec.Command.Name != "demo" {
		t.Errorf("spec not loaded: %+v", p.spec)
	}
	if p.specPath != specPath {
		t.Errorf("specPath = %q, want %q", p.specPath, specPath)
	}
	if p.conf == nil || p.conf.Schema != "https://x/conf.json" {
		t.Errorf("conf not loaded from beside spec: %+v", p.conf)
	}
	if p.confPath != confPath {
		t.Errorf("confPath = %q, want %q", p.confPath, confPath)
	}
}

// TestLoad_confDefaultsWhenAbsent confirms the conf is optional: no explicit path
// and no fallback match yields a non-nil default Conf and an empty confPath.
func TestLoad_confDefaultsWhenAbsent(t *testing.T) {
	dir := t.TempDir()
	specPath := filepath.Join(dir, "spec.yaml")
	mustWriteSpec(t, specPath, &Spec{Command: Command{Name: "demo"}})

	p := newProcessor(t, specPath, "")
	if err := p.load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	if p.conf == nil {
		t.Fatal("conf = nil, want default &Conf{}")
	}
	if p.confPath != "" {
		t.Errorf("confPath = %q, want empty", p.confPath)
	}
}

// TestLoad_confTOMLFallback confirms a TOML conf is discovered beside the spec —
// covering both the new TOML format and the fallback ordering.
func TestLoad_confTOMLFallback(t *testing.T) {
	dir := t.TempDir()
	specPath := filepath.Join(dir, "spec.yaml")
	mustWriteSpec(t, specPath, &Spec{Command: Command{Name: "demo"}})
	confPath := filepath.Join(dir, ".rotini.conf.toml")
	mustWriteConf(t, confPath, &Conf{Schema: "https://x/conf.json"})

	p := newProcessor(t, specPath, "")
	if err := p.load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	if p.confPath != confPath {
		t.Errorf("confPath = %q, want %q", p.confPath, confPath)
	}
	if p.conf == nil || p.conf.Schema != "https://x/conf.json" {
		t.Errorf("toml conf not decoded: %+v", p.conf)
	}
}

// TestLoadSpec_cwdFallback confirms an empty spec path resolves to the first
// .rotini.spec.* in the working directory.
func TestLoadSpec_cwdFallback(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustWriteSpec(t, filepath.Join(dir, ".rotini.spec.yaml"), &Spec{Command: Command{Name: "demo"}})

	p := newProcessor(t, "", "")
	if err := p.load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	if p.spec == nil || p.spec.Command.Name != "demo" {
		t.Errorf("spec not resolved from cwd: %+v", p.spec)
	}
	if want := filepath.Join(dir, ".rotini.spec.yaml"); p.specPath != want {
		t.Errorf("specPath = %q, want %q", p.specPath, want)
	}
}

// TestLoadSpec_requiredErr confirms the spec is required: no explicit path and no
// fallback match in the working directory is errSpecPathRequired.
func TestLoadSpec_requiredErr(t *testing.T) {
	t.Chdir(t.TempDir()) // empty dir — no .rotini.spec.*

	p := newProcessor(t, "", "")
	if err := p.load(); !errors.Is(err, errSpecPathRequired) {
		t.Errorf("load err = %v, want errSpecPathRequired", err)
	}
}

// TestLoadConf_explicitMissingIsError confirms an explicit conf path that does not
// exist is a hard read error (not a silent fallback to defaults).
func TestLoadConf_explicitMissingIsError(t *testing.T) {
	dir := t.TempDir()
	specPath := filepath.Join(dir, "spec.yaml")
	mustWriteSpec(t, specPath, &Spec{Command: Command{Name: "demo"}})

	p := newProcessor(t, specPath, filepath.Join(dir, "does-not-exist.yaml"))
	if err := p.load(); err == nil {
		t.Error("load err = nil, want a read error for the missing explicit conf")
	}
}

// loadAndValidate builds a processor, runs the loader, then the validator phase,
// returning that phase's aggregated result.
func loadAndValidate(t *testing.T, specPath, confPath, version string) error {
	t.Helper()
	p, err := NewProcessor(specPath, confPath, version)
	if err != nil {
		t.Fatalf("NewProcessor: %v", err)
	}
	if err := p.load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	return p.validate()
}

// TestProcessorValidate_validSpecAndConf: a schema-valid spec and conf pass with no
// problems.
func TestProcessorValidate_validSpecAndConf(t *testing.T) {
	spec := writeTemp(t, "spec.yaml", validSpecHeader+"command:\n  name: demo\n")
	conf := writeTemp(t, "conf.yaml", validConfHeader)
	if err := loadAndValidate(t, spec, conf, ""); err != nil {
		t.Errorf("validate(valid spec+conf) = %v, want nil", err)
	}
}

// TestProcessorValidate_schemaViolation: a spec missing the required command is a
// schema violation (caught on the raw instance).
func TestProcessorValidate_schemaViolation(t *testing.T) {
	spec := writeTemp(t, "spec.yaml", validSpecHeader) // no command
	err := loadAndValidate(t, spec, "", "")
	if err == nil || !strings.Contains(err.Error(), "command") {
		t.Errorf("validate(missing command) = %v, want a schema error naming command", err)
	}
}

// TestProcessorValidate_ruleViolation: a duplicate flag identifier is a
// rotini-specific rule the JSON Schema can't express — caught by the lints.
func TestProcessorValidate_ruleViolation(t *testing.T) {
	spec := validSpecHeader +
		"command:\n" +
		"  name: app\n" +
		"  inputs:\n" +
		"    flags:\n" +
		"      - name: output\n" +
		"        identifiers: [-o, --output]\n" +
		"        schema: { type: string }\n" +
		"      - name: organization\n" +
		"        identifiers: [-o, --org]\n" +
		"        schema: { type: string }\n"
	err := loadAndValidate(t, writeTemp(t, "spec.yaml", spec), "", "")
	if err == nil || !strings.Contains(err.Error(), `"-o"`) || !strings.Contains(err.Error(), "output") {
		t.Errorf("validate(duplicate -o) = %v, want a duplicate-identifier rule violation", err)
	}
}

// TestProcessorValidate_versionGuard: the $schema↔version guard fires through the
// processor — a matching version passes, a mismatch is reported.
func TestProcessorValidate_versionGuard(t *testing.T) {
	spec := writeTemp(t, "spec.yaml", validSpecHeader+"command:\n  name: demo\n") // tag 1.2.3

	if err := loadAndValidate(t, spec, "", "1.2.3"); err != nil {
		t.Errorf("validate(matching 1.2.3) = %v, want nil", err)
	}
	err := loadAndValidate(t, spec, "", "2.0.0")
	if err == nil || !strings.Contains(err.Error(), "1.2.3") || !strings.Contains(err.Error(), "2.0.0") {
		t.Errorf("validate(mismatch 2.0.0) = %v, want an error naming both versions", err)
	}
}

// TestProcessorValidate_failMode: fast/collect is read off the loaded conf —
// collect joins every problem, fast returns the first.
func TestProcessorValidate_failMode(t *testing.T) {
	spec := writeTemp(t, "spec.json", multiViolationDoc)
	collectConf := writeTemp(t, "collect.yaml", validConfHeader+"validate:\n  fail: collect\n")
	fastConf := writeTemp(t, "fast.yaml", validConfHeader+"validate:\n  fail: fast\n")

	collect := loadAndValidate(t, spec, collectConf, "")
	fast := loadAndValidate(t, spec, fastConf, "")
	if collect == nil || fast == nil {
		t.Fatal("expected validation errors in both modes")
	}
	if n := strings.Count(collect.Error(), "\n") + 1; n < 2 {
		t.Fatalf("collect should report multiple problems, got %d:\n%v", n, collect)
	}
	if n := strings.Count(fast.Error(), "\n") + 1; n != 1 {
		t.Errorf("fast should report a single problem, got %d:\n%v", n, fast)
	}
}

// TestProcessorGenerate_matchesCompanion is the byte-stability guard for step 4: the
// processor's generate phase, driven from the committed companion spec, must
// reproduce the committed companion files byte-for-byte — proving the wrap around
// generateAll changed no output.
func TestProcessorGenerate_matchesCompanion(t *testing.T) {
	repoRoot := repoRoot(t)
	specPath := filepath.Join(repoRoot, "cmd", "rotini", ".rotini.spec.yaml")

	tmp := t.TempDir()
	writeTestFile(t, filepath.Join(tmp, "go.mod"), minimalGoMod)
	confPath := filepath.Join(tmp, ".rotini.conf.yaml")
	writeTestFile(t, confPath, companionConf)

	t.Chdir(tmp)
	p, err := NewProcessor(specPath, confPath, "")
	if err != nil {
		t.Fatalf("NewProcessor: %v", err)
	}
	if err := p.load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	if err := p.generate(); err != nil {
		t.Fatalf("generate: %v", err)
	}

	// The always-(re)generated combined framework + rollup file is reproduced
	// byte-for-byte.
	assertGoEqual(t,
		filepath.Join(tmp, "cmd/rotini/cli/rotini.gen.go"),
		filepath.Join(repoRoot, "cmd/rotini/cli/rotini.gen.go"))

	// The companion's generated help feature files are reproduced byte-for-byte
	// (only help is enabled in the committed companion conf).
	featRel := "cmd/rotini/cli/embed/help"
	entries, err := os.ReadDir(filepath.Join(repoRoot, featRel))
	if err != nil {
		t.Fatalf("read companion help dir: %v", err)
	}
	for _, e := range entries {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		got := readFileString(t, filepath.Join(tmp, featRel, e.Name()))
		want := readFileString(t, filepath.Join(repoRoot, featRel, e.Name()))
		if got != want {
			t.Errorf("companion help/%s not reproduced via processor:\n--- generated ---\n%s\n--- committed ---\n%s", e.Name(), got, want)
		}
	}
}

// TestProcessorInitialize_cmdRecipe confirms the cmd recipe (and the empty default)
// scaffold the established cmd/<name>/ layout — delegating to the proven path.
func TestProcessorInitialize_cmdRecipe(t *testing.T) {
	tmp := initTestModule(t)
	p, err := NewProcessor("", "", "")
	if err != nil {
		t.Fatalf("NewProcessor: %v", err)
	}

	// Explicit cmd recipe.
	if err := p.initialize("mycli", "yaml", false, "", recipeCmd); err != nil {
		t.Fatalf("initialize(cmd): %v", err)
	}
	dir := filepath.Join(tmp, "cmd", "mycli")
	mustContain(t, filepath.Join(dir, ".rotini.spec.yaml"), "name: mycli")
	mustContain(t, filepath.Join(dir, ".rotini.conf.yaml"), "package: cmd/mycli/cli")
	mustContain(t, filepath.Join(dir, "main.go"), "//go:generate rotini generate")
	mustContain(t, filepath.Join(dir, "cli", "rotini.gen.go"), "package cli", "var Program = NewProgram(&handlers{})")

	// The empty recipe defaults to cmd.
	if err := p.initialize("other", "yaml", false, "", ""); err != nil {
		t.Fatalf("initialize(default): %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(tmp, "cmd", "other", "main.go")); statErr != nil {
		t.Errorf("empty recipe did not scaffold the cmd layout: %v", statErr)
	}
}

// TestProcessorInitialize_recipeErrors confirms the flat recipe is a flagged seam
// and an unknown recipe is rejected — both before touching the filesystem.
func TestProcessorInitialize_recipeErrors(t *testing.T) {
	p, err := NewProcessor("", "", "")
	if err != nil {
		t.Fatalf("NewProcessor: %v", err)
	}
	if err := p.initialize("mycli", "yaml", false, "", recipeFlat); err == nil || !strings.Contains(err.Error(), "not yet supported") {
		t.Errorf("initialize(flat) = %v, want a 'not yet supported' error", err)
	}
	if err := p.initialize("mycli", "yaml", false, "", recipe("bogus")); err == nil || !strings.Contains(err.Error(), "unknown init recipe") {
		t.Errorf("initialize(bogus) = %v, want an 'unknown recipe' error", err)
	}
}
