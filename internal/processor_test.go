package internal

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

// newTestSession builds a session (the per-pass loaded unit) for tests.
func newTestSession(t *testing.T, specPath, confPath string) *session {
	t.Helper()
	return newSession(specPath, confPath, "")
}

// TestLoad_explicitSpec_confBesideSpec covers the common path: an explicit spec
// path and a conf discovered beside it via the fallback locations.
func TestLoad_explicitSpec_confBesideSpec(t *testing.T) {
	dir := t.TempDir()
	specPath := filepath.Join(dir, "myspec.yaml")
	mustWriteSpec(t, specPath, &Spec{Command: Command{Schema: "https://x/spec.json", Name: "demo"}})
	confPath := filepath.Join(dir, ".rotini.conf.yaml")
	mustWriteConf(t, confPath, &Conf{Schema: "https://x/conf.json"})

	p := newTestSession(t, specPath, "")
	if err := p.load(); err != nil {
		t.Fatalf("load: %v", err)
	}

	if p.spec == nil || p.spec.spec.Command.Name != "demo" {
		t.Errorf("spec not loaded: %+v", p.spec)
	}
	if p.spec.path != specPath {
		t.Errorf("spec path = %q, want %q", p.spec.path, specPath)
	}
	if p.conf == nil || p.conf.conf.Schema != "https://x/conf.json" {
		t.Errorf("conf not loaded from beside spec: %+v", p.conf)
	}
	if p.conf.path != confPath {
		t.Errorf("conf path = %q, want %q", p.conf.path, confPath)
	}
}

// TestLoad_confDefaultsWhenAbsent confirms the conf is optional: no explicit path
// and no fallback match yields a non-nil default Conf and an empty confPath.
func TestLoad_confDefaultsWhenAbsent(t *testing.T) {
	dir := t.TempDir()
	specPath := filepath.Join(dir, "spec.yaml")
	mustWriteSpec(t, specPath, &Spec{Command: Command{Name: "demo"}})

	p := newTestSession(t, specPath, "")
	if err := p.load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	if p.conf == nil || p.conf.conf == nil {
		t.Fatal("conf = nil, want default &Conf{}")
	}
	if p.conf.path != "" {
		t.Errorf("conf path = %q, want empty", p.conf.path)
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

	p := newTestSession(t, specPath, "")
	if err := p.load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	if p.conf.path != confPath {
		t.Errorf("conf path = %q, want %q", p.conf.path, confPath)
	}
	if p.conf == nil || p.conf.conf.Schema != "https://x/conf.json" {
		t.Errorf("toml conf not decoded: %+v", p.conf)
	}
}

// TestLoadSpec_cwdFallback confirms an empty spec path resolves to the first
// .rotini.spec.* in the working directory.
func TestLoadSpec_cwdFallback(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustWriteSpec(t, filepath.Join(dir, ".rotini.spec.yaml"), &Spec{Command: Command{Name: "demo"}})

	p := newTestSession(t, "", "")
	if err := p.load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	if p.spec == nil || p.spec.spec.Command.Name != "demo" {
		t.Errorf("spec not resolved from cwd: %+v", p.spec)
	}
	if want := filepath.Join(dir, ".rotini.spec.yaml"); p.spec.path != want {
		t.Errorf("spec path = %q, want %q", p.spec.path, want)
	}
}

// TestLoadSpec_requiredErr confirms the spec is required: no explicit path and no
// fallback match in the working directory is errSpecPathRequired.
func TestLoadSpec_requiredErr(t *testing.T) {
	t.Chdir(t.TempDir()) // empty dir — no .rotini.spec.*

	p := newTestSession(t, "", "")
	if err := p.load(); !errors.Is(err, errSpecPathRequired) {
		t.Errorf("load err = %v, want errSpecPathRequired", err)
	}
}

// TestLoadConf_explicitMissingDefaults confirms an explicit conf path that does not
// exist falls back to a default Conf (the conf is optional), rather than erroring —
// matching the established Generate contract.
func TestLoadConf_explicitMissingDefaults(t *testing.T) {
	dir := t.TempDir()
	specPath := filepath.Join(dir, "spec.yaml")
	mustWriteSpec(t, specPath, &Spec{Command: Command{Name: "demo"}})

	p := newTestSession(t, specPath, filepath.Join(dir, "does-not-exist.yaml"))
	if err := p.load(); err != nil {
		t.Fatalf("load with missing explicit conf = %v, want nil (defaults used)", err)
	}
	if p.conf == nil || p.conf.conf == nil {
		t.Error("conf = nil, want a default &Conf{}")
	}
	if p.conf.path != "" {
		t.Errorf("conf path = %q, want empty (no usable conf)", p.conf.path)
	}
}

// validateOnce runs the validator phase over the spec and conf through the processor,
// returning the aggregated result. It is the test seam that replaced the production
// validateOnce retired in the processor migration, so the existing call sites are
// unchanged.
func validateOnce(specPath, confPath, failMode, version string) error {
	s := newSession(specPath, confPath, version)
	s.failMode = failMode
	if err := s.load(); err != nil {
		return err
	}
	return s.validate()
}

// loadAndValidate builds a processor, runs the loader, then the validator phase,
// returning that phase's aggregated result.
func loadAndValidate(t *testing.T, specPath, confPath, version string) error {
	t.Helper()
	s := newSession(specPath, confPath, version)
	if err := s.load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	return s.validate()
}

// TestProcessorValidate_validSpecAndConf: a schema-valid spec and conf pass with no
// problems.
func TestProcessorValidate_validSpecAndConf(t *testing.T) {
	spec := writeTemp(t, "spec.yaml", validSpecHeader+"name: demo\n")
	conf := writeTemp(t, "conf.yaml", validConfHeader)
	if err := loadAndValidate(t, spec, conf, ""); err != nil {
		t.Errorf("validate(valid spec+conf) = %v, want nil", err)
	}
}

// TestProcessorValidate_schemaViolation: a spec missing the required root name is
// a schema violation (caught on the raw instance). The document is the root
// command, so 'name' is required at the top level (W3 reshape).
func TestProcessorValidate_schemaViolation(t *testing.T) {
	spec := writeTemp(t, "spec.yaml", validSpecHeader) // no name
	err := loadAndValidate(t, spec, "", "")
	if err == nil || !strings.Contains(err.Error(), "name") {
		t.Errorf("validate(missing name) = %v, want a schema error naming name", err)
	}
}

// TestProcessorValidate_ruleViolation: a duplicate flag identifier is a
// rotini-specific rule the JSON Schema can't express — caught by the lints.
func TestProcessorValidate_ruleViolation(t *testing.T) {
	spec := validSpecHeader +
		"name: app\n" +
		"flags:\n" +
		"  - name: output\n" +
		"    identifiers: [-o, --output]\n" +
		"    schema: { type: string }\n" +
		"  - name: organization\n" +
		"    identifiers: [-o, --org]\n" +
		"    schema: { type: string }\n"
	err := loadAndValidate(t, writeTemp(t, "spec.yaml", spec), "", "")
	if err == nil || !strings.Contains(err.Error(), `"-o"`) || !strings.Contains(err.Error(), "output") {
		t.Errorf("validate(duplicate -o) = %v, want a duplicate-identifier rule violation", err)
	}
}

// TestProcessorValidate_versionGuard: the $schema↔version guard fires through the
// processor — a matching version passes, a mismatch is reported.
func TestProcessorValidate_versionGuard(t *testing.T) {
	spec := writeTemp(t, "spec.yaml", validSpecHeader+"name: demo\n") // tag 1.2.3

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
	s := newSession(specPath, confPath, "")
	if err := s.load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	if err := s.generate(); err != nil {
		t.Fatalf("generate: %v", err)
	}

	// The always-(re)generated combined framework + rollup file is reproduced
	// byte-for-byte.
	assertGoEqual(t,
		filepath.Join(tmp, "internal/cmd/rotini/zz_rotini.gen.go"),
		filepath.Join(repoRoot, "internal/cmd/rotini/zz_rotini.gen.go"))

	// The companion's features are all inline (no feature files on disk) — the
	// dogfooded output lives in the gen file, compared byte-for-byte above.
}

// TestProcessorInitialize confirms the Processor's initialize scaffolds and
// validates the seed spec + conf under cmd/<name>/ and runs the first generate
// (entrypoint + codegen).
func TestProcessorInitialize(t *testing.T) {
	tmp := initTestModule(t)
	p := NewProcessor("")

	if err := p.initialize("mycli", "yaml", false); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	dir := filepath.Join(tmp, "cmd", "mycli")
	mustContain(t, filepath.Join(dir, ".rotini.spec.yaml"), "name: mycli")
	mustContain(t, filepath.Join(dir, ".rotini.conf.yaml"), "package: internal/cmd/mycli")
	mustContain(t, filepath.Join(dir, "main.go"), "//go:generate go tool rotini generate")
	mustContain(t, filepath.Join(tmp, "internal", "cmd", "mycli", "zz_rotini.go"),
		"package mycli", "var Program = NewProgram(&handlers{})")
}

// TestProcessorValidatePass confirms the validate workflow composes load + validate,
// propagating a load failure.
func TestProcessorValidatePass(t *testing.T) {
	good := writeTemp(t, "spec.yaml", validSpecHeader+"name: demo\n")
	if err := newTestSession(t, good, "").validatePass(); err != nil {
		t.Errorf("validatePass(valid) = %v, want nil", err)
	}

	missing := filepath.Join(t.TempDir(), "nope.yaml")
	if err := newTestSession(t, missing, "").validatePass(); err == nil {
		t.Error("validatePass(missing spec) = nil, want a load error")
	}
}

// TestProcessorGeneratePass_gatesOnValidation confirms the generate workflow does
// not reach codegen when validation fails: the error is returned and nothing is
// written.
func TestProcessorGeneratePass_gatesOnValidation(t *testing.T) {
	tmp := t.TempDir()
	writeTestFile(t, filepath.Join(tmp, "go.mod"), minimalGoMod)
	specPath := filepath.Join(tmp, ".rotini.spec.yaml")
	writeTestFile(t, specPath, validSpecHeader) // no command → schema-invalid
	t.Chdir(tmp)

	p := newTestSession(t, specPath, "")
	if err := p.generatePass(); err == nil {
		t.Fatal("generatePass(invalid spec) = nil, want a validation error")
	}
	if _, statErr := os.Stat(filepath.Join(tmp, "cmd")); statErr == nil {
		t.Error("codegen ran despite invalid input — the validate gate did not hold")
	}
}

// TestProcessorGeneratePass_valid confirms a valid spec flows load → validate →
// generate and emits the program.
func TestProcessorGeneratePass_valid(t *testing.T) {
	tmp := t.TempDir()
	writeTestFile(t, filepath.Join(tmp, "go.mod"), minimalGoMod)
	writeTestFile(t, filepath.Join(tmp, ".rotini.spec.yaml"),
		validSpecHeader+"name: rotini\ncommands:\n  - name: generate\n")
	t.Chdir(tmp)

	p := newTestSession(t, ".rotini.spec.yaml", "")
	if err := p.generatePass(); err != nil {
		t.Fatalf("generatePass(valid) = %v, want nil", err)
	}
	mustContain(t, filepath.Join(tmp, "internal", "cmd", "rotini", "zz_rotini.gen.go"),
		"package rotini", "var Program = NewProgram(&handlers{})")
}

// TestProcessorValidate_routing confirms the non-watch path: a valid pass routes a
// "[HH:MM:SS] <took>" summary to onResult and returns nil; a failing pass returns the
// error and is NOT routed through onResult.
// TestProcessorValidate_warningsSurfaceAndDontFail pins the end-to-end severity
// split through Processor.Validate: a warning-only spec passes (Validate returns
// nil) AND the onWarnings callback receives the finding for the OnWarning funnel.
func TestProcessorValidate_warningsSurfaceAndDontFail(t *testing.T) {
	spec := validSpecHeader +
		"name: app\nconfig_files:\n" +
		"  - name: a\n    path: ~/.app.yaml\n" +
		"  - name: b\n    path: ~/.app.yaml\n"
	var warns []error
	err := NewProcessor("").Validate(writeTemp(t, "spec.yaml", spec), "", false, "", nil,
		func(w []error) { warns = append(warns, w...) })
	if err != nil {
		t.Errorf("Validate = %v, want nil (warnings never fail)", err)
	}
	if len(warns) != 1 {
		t.Fatalf("onWarnings received %d, want 1", len(warns))
	}
}

func TestProcessorValidate_routing(t *testing.T) {
	spec := writeTemp(t, "spec.yaml", validSpecHeader+"name: demo\n")
	var summary string
	var cbErr error
	calls := 0
	if err := NewProcessor("").Validate(spec, "", false, "", func(s string, e error) { calls++; summary, cbErr = s, e }, nil); err != nil {
		t.Fatalf("Validate(valid) = %v, want nil", err)
	}
	if calls != 1 || cbErr != nil || summary == "" {
		t.Errorf("valid pass: calls=%d summary=%q err=%v, want one call with a summary and nil err", calls, summary, cbErr)
	}

	calls = 0
	bad := writeTemp(t, "bad.yaml", validSpecHeader) // no command
	if err := NewProcessor("").Validate(bad, "", false, "", func(string, error) { calls++ }, nil); err == nil {
		t.Error("Validate(invalid) = nil, want an error")
	}
	if calls != 0 {
		t.Errorf("onResult called %d times on a non-watch failure, want 0", calls)
	}
}

// TestProcessorGenerate_workflow confirms the generate workflow emits the program
// through Processor.Generate, including resolving an empty spec path from the working
// directory.
func TestProcessorGenerate_workflow(t *testing.T) {
	tmp := t.TempDir()
	writeTestFile(t, filepath.Join(tmp, "go.mod"), minimalGoMod)
	writeTestFile(t, filepath.Join(tmp, ".rotini.spec.yaml"),
		validSpecHeader+"name: rotini\ncommands:\n  - name: generate\n")
	t.Chdir(tmp)

	noop := func(string, error) {}
	gen := filepath.Join(tmp, "internal", "cmd", "rotini", "zz_rotini.gen.go")

	if err := NewProcessor("").Generate(".rotini.spec.yaml", "", false, noop); err != nil {
		t.Fatalf("Generate = %v, want nil", err)
	}
	mustContain(t, gen, "package rotini", "var Program = NewProgram(&handlers{})")

	// An empty spec path resolves from the working directory.
	if err := NewProcessor("").Generate("", "", false, noop); err != nil {
		t.Fatalf("Generate(empty spec, cwd fallback) = %v, want nil", err)
	}
}

// generatePassClosure returns a watchLoop-compatible pass that builds a fresh session
// and runs generatePass — the test seam that replaced generateTimed.
func generatePassClosure(specPath string) func() (string, error) {
	return func() (string, error) {
		return "", newSession(specPath, "", "").generatePass()
	}
}

// TestProcessorWatch_regenerates drives the run/watch engine with the fresh-processor
// generate pass (the shape runProcessorWorkflow builds) to confirm the processor
// re-reads and regenerates when the spec changes.
func TestProcessorWatch_regenerates(t *testing.T) {
	tmp := t.TempDir()
	writeTestFile(t, filepath.Join(tmp, "go.mod"), minimalGoMod)
	specPath := filepath.Join(tmp, ".rotini.spec.yaml")
	writeTestFile(t, specPath, specWith("alpha"))
	t.Chdir(tmp)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- watchLoop(ctx, specPath, "", generatePassClosure(specPath), func(string, error) {}) }()

	rtg := filepath.Join(tmp, "internal", "cmd", "mycli", "zz_rotini.gen.go")
	if !waitForCond(3*time.Second, func() bool { return fileContains(rtg, "MycliAlpha") }) {
		t.Fatal("initial generate did not produce MycliAlpha")
	}

	writeTestFile(t, specPath, specWith("alpha", "beta"))
	if !waitForCond(5*time.Second, func() bool { return fileContains(rtg, "MycliBeta") }) {
		t.Fatal("watch did not regenerate after the spec change (no MycliBeta)")
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("watchLoop returned error after cancel: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("watchLoop did not return after cancel")
	}
}
