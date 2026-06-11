package internal

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestInitialize_confDefaults verifies `rotini init` honors the module-root conf's
// `initialize` block (format + package), and that an explicit --format overrides it.
func TestInitialize_confDefaults(t *testing.T) {
	tmp := initTestModule(t)
	writeTestFile(t, filepath.Join(tmp, ".rotini.conf.yaml"),
		"$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-conf.json\n"+
			"initialize:\n  format: jsonc\n  package: tools\n")

	// No explicit --format → conf's format (jsonc) and package (tools).
	if err := Initialize("mycli", "", false, ""); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	dir := filepath.Join(tmp, "tools", "mycli")
	mustContain(t, filepath.Join(dir, ".rotini.spec.jsonc"), `"name": "mycli"`)
	mustContain(t, filepath.Join(dir, ".rotini.conf.jsonc"), `"internal/cmd/mycli"`)

	// An explicit --format overrides the conf default (still under the conf package).
	if err := Initialize("other", "yaml", false, ""); err != nil {
		t.Fatalf("Initialize (explicit format): %v", err)
	}
	if _, err := os.Stat(filepath.Join(tmp, "tools", "other", ".rotini.spec.yaml")); err != nil {
		t.Errorf("explicit --format=yaml not honored: %v", err)
	}
}

func initTestModule(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	writeTestFile(t, filepath.Join(tmp, "go.mod"), "module example.com/myclis\n\ngo 1.26\n")
	t.Chdir(tmp)
	return tmp
}

// TestInitialize_scaffoldsSeeds verifies the whole `rotini init` job: the seed
// spec (root command named after the CLI, with the default version/help flags)
// and the seed conf (package layout + feature toggles) under cmd/<name>/.
// Nothing else is written — code generation is a separate `rotini generate` run.
func TestInitialize_scaffoldsSeeds(t *testing.T) {
	tmp := initTestModule(t)
	if err := Initialize("mycli", "yaml", false, ""); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	dir := filepath.Join(tmp, "cmd", "mycli")
	mustContain(t, filepath.Join(dir, ".rotini.spec.yaml"),
		"name: mycli", "schema-spec.json",
		"name: version", "--version", "name: help", "--help")
	mustContain(t, filepath.Join(dir, ".rotini.conf.yaml"),
		"schema-conf.json",
		"package: cmd/mycli", "package: internal/cmd/mycli",
		"file: main.go", "file: zz_rotini.gen.go",
		"dir: internal/cmd/mycli/embed/help")

	// Initialize seeds only — no main.go and no generated package.
	if _, err := os.Stat(filepath.Join(dir, "main.go")); !os.IsNotExist(err) {
		t.Errorf("initialize should not write main.go (err=%v)", err)
	}
	if _, err := os.Stat(filepath.Join(tmp, "internal", "cmd", "mycli")); !os.IsNotExist(err) {
		t.Errorf("initialize should not generate code (err=%v)", err)
	}
}

// TestInitialize_stampsSchemaRef covers the scaffold side of the $schema↔version
// guard: a release ref is stamped verbatim into both $schema URLs (always the
// refs/tags/<VER> form), while a non-release build (empty ref) falls back to the
// baseline 0.0.0 tag.
func TestInitialize_stampsSchemaRef(t *testing.T) {
	tmp := initTestModule(t)

	if err := Initialize("rel", "yaml", false, "1.4.0"); err != nil {
		t.Fatalf("Initialize(ref=1.4.0): %v", err)
	}
	relDir := filepath.Join(tmp, "cmd", "rel")
	mustContain(t, filepath.Join(relDir, ".rotini.spec.yaml"), "refs/tags/1.4.0/schema-spec.json")
	mustContain(t, filepath.Join(relDir, ".rotini.conf.yaml"), "refs/tags/1.4.0/schema-conf.json")

	if err := Initialize("dev", "yaml", false, ""); err != nil {
		t.Fatalf("Initialize(ref=\"\"): %v", err)
	}
	devDir := filepath.Join(tmp, "cmd", "dev")
	mustContain(t, filepath.Join(devDir, ".rotini.spec.yaml"), "refs/tags/0.0.0/schema-spec.json")
	mustContain(t, filepath.Join(devDir, ".rotini.conf.yaml"), "refs/tags/0.0.0/schema-conf.json")
}

func TestInitialize_noClobberThenForce(t *testing.T) {
	initTestModule(t)
	if err := Initialize("mycli", "yaml", false, ""); err != nil {
		t.Fatalf("first Initialize: %v", err)
	}
	err := Initialize("mycli", "yaml", false, "")
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("re-init without force: got %v, want 'already exists'", err)
	}
	if err := Initialize("mycli", "yaml", true, ""); err != nil {
		t.Fatalf("re-init with force: %v", err)
	}
}

func TestInitialize_formatJSON(t *testing.T) {
	tmp := initTestModule(t)
	if err := Initialize("tool", "json", false, ""); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	mustContain(t, filepath.Join(tmp, "cmd", "tool", ".rotini.spec.json"), `"name": "tool"`)
	mustContain(t, filepath.Join(tmp, "cmd", "tool", ".rotini.conf.json"), `"internal/cmd/tool"`)
}

// TestInitialize_formatTOML covers the toml seed path: the YAML templates are
// transcoded to TOML (via yaml→json→toml).
func TestInitialize_formatTOML(t *testing.T) {
	tmp := initTestModule(t)
	if err := Initialize("tool", "toml", false, ""); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	mustContain(t, filepath.Join(tmp, "cmd", "tool", ".rotini.spec.toml"), `name = "tool"`)
	mustContain(t, filepath.Join(tmp, "cmd", "tool", ".rotini.conf.toml"), "internal/cmd/tool")
}

func TestInitialize_errors(t *testing.T) {
	initTestModule(t)
	if err := Initialize("", "yaml", false, ""); err == nil {
		t.Error("empty name should error")
	}
	if err := Initialize("x", "xml", false, ""); err == nil {
		t.Error("unsupported format should error")
	}
}

func TestInitialize_outsideModule(t *testing.T) {
	t.Chdir(t.TempDir()) // no go.mod up the tree
	if err := Initialize("mycli", "yaml", false, ""); err == nil {
		t.Error("Initialize outside a module should error")
	}
}
