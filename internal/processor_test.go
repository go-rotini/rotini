package internal

import (
	"errors"
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
