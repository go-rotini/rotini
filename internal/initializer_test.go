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
	if err := Initialize("mycli", "", false, "", ""); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	dir := filepath.Join(tmp, "tools", "mycli")
	mustContain(t, filepath.Join(dir, ".rotini.spec.jsonc"), `"name": "mycli"`)
	mustContain(t, filepath.Join(dir, ".rotini.conf.jsonc"), `"tools/mycli/cli"`)
	mustContain(t, filepath.Join(dir, "main.go"), `"example.com/myclis/tools/mycli/cli"`)

	// An explicit --format overrides the conf default (still under the conf package).
	if err := Initialize("other", "yaml", false, "", ""); err != nil {
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

func TestInitialize_scaffoldsStandalone(t *testing.T) {
	tmp := initTestModule(t)
	if err := Initialize("mycli", "yaml", false, "", ""); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	dir := filepath.Join(tmp, "cmd", "mycli")
	mustContain(t, filepath.Join(dir, ".rotini.spec.yaml"), "name: mycli", "schema-spec.json")
	mustContain(t, filepath.Join(dir, ".rotini.conf.yaml"),
		"package: cmd/mycli/cli", "file: rotini.gen.go", "schema-conf.json")
	mustContain(t, filepath.Join(dir, "main.go"),
		"//go:generate rotini generate",
		`"example.com/myclis/cmd/mycli/cli"`,
		`"github.com/go-rotini/rotini"`,
		`Bind("parser", rotini.NewParser())`, "Execute()")
	gen := filepath.Join(dir, "cli", "rotini.gen.go")
	mustContain(t, gen,
		"package cli", "type ProgramHandlers interface", "var definition",
		"var Program = NewProgram(&handlers{})")
	mustContain(t, filepath.Join(dir, "cli", "mycli.go"), "type mycliHandlers struct{}")
}

// TestInitialize_stampsSchemaRef covers Item 3's scaffold side: a release ref is
// stamped verbatim into both $schema URLs (always the refs/tags/<VER> form), while
// a non-release build (empty ref) falls back to the baseline 0.0.0 tag.
func TestInitialize_stampsSchemaRef(t *testing.T) {
	tmp := initTestModule(t)

	if err := Initialize("rel", "yaml", false, "", "1.4.0"); err != nil {
		t.Fatalf("Initialize(ref=1.4.0): %v", err)
	}
	relDir := filepath.Join(tmp, "cmd", "rel")
	mustContain(t, filepath.Join(relDir, ".rotini.spec.yaml"), "refs/tags/1.4.0/schema-spec.json")
	mustContain(t, filepath.Join(relDir, ".rotini.conf.yaml"), "refs/tags/1.4.0/schema-conf.json")

	if err := Initialize("dev", "yaml", false, "", ""); err != nil {
		t.Fatalf("Initialize(ref=\"\"): %v", err)
	}
	devDir := filepath.Join(tmp, "cmd", "dev")
	mustContain(t, filepath.Join(devDir, ".rotini.spec.yaml"), "refs/tags/0.0.0/schema-spec.json")
	mustContain(t, filepath.Join(devDir, ".rotini.conf.yaml"), "refs/tags/0.0.0/schema-conf.json")
}

func TestInitialize_noClobberThenForce(t *testing.T) {
	initTestModule(t)
	if err := Initialize("mycli", "yaml", false, "", ""); err != nil {
		t.Fatalf("first Initialize: %v", err)
	}
	err := Initialize("mycli", "yaml", false, "", "")
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("re-init without force: got %v, want 'already exists'", err)
	}
	if err := Initialize("mycli", "yaml", true, "", ""); err != nil {
		t.Fatalf("re-init with force: %v", err)
	}
}

func TestInitialize_forcePreservesEditedStub(t *testing.T) {
	tmp := initTestModule(t)
	if err := Initialize("mycli", "yaml", false, "", ""); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	stub := filepath.Join(tmp, "cmd", "mycli", "cli", "mycli.go")
	writeTestFile(t, stub, "package cli\n\n// EDITED BY USER\n")

	if err := Initialize("mycli", "yaml", true, "", ""); err != nil {
		t.Fatalf("re-init with force: %v", err)
	}
	mustContain(t, stub, "EDITED BY USER")
}

func TestInitialize_formatJSON(t *testing.T) {
	tmp := initTestModule(t)
	if err := Initialize("tool", "json", false, "", ""); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	mustContain(t, filepath.Join(tmp, "cmd", "tool", ".rotini.spec.json"), `"name": "tool"`)
	mustContain(t, filepath.Join(tmp, "cmd", "tool", ".rotini.conf.json"), `"cmd/tool/cli"`)
}

func TestInitialize_errors(t *testing.T) {
	initTestModule(t)
	if err := Initialize("", "yaml", false, "", ""); err == nil {
		t.Error("empty name should error")
	}
	if err := Initialize("x", "toml", false, "", ""); err == nil {
		t.Error("unsupported format should error")
	}
}

func TestInitialize_outsideModule(t *testing.T) {
	t.Chdir(t.TempDir()) // no go.mod up the tree
	if err := Initialize("mycli", "yaml", false, "", ""); err == nil {
		t.Error("Initialize outside a module should error")
	}
}

func TestInitialize_into(t *testing.T) {
	tmp := initTestModule(t)
	if err := Initialize("parent", "yaml", false, "", ""); err != nil {
		t.Fatalf("init parent: %v", err)
	}
	if err := Initialize("child", "yaml", false, "parent", ""); err != nil {
		t.Fatalf("init child --into parent: %v", err)
	}

	// The parent spec gained a $ref to the child (the value may be quoted by the
	// YAML encoder).
	mustContain(t, filepath.Join(tmp, "cmd/parent/.rotini.spec.yaml"), "$ref:", "../child/.rotini.spec.yaml")
	// The parent re-generated to compose the child (combined framework + rollup).
	gen := filepath.Join(tmp, "cmd/parent/cli/rotini.gen.go")
	mustContain(t, gen,
		"ParentChild() rotini.CommandHandlers", `Handler: "ParentChild"`,
		"childcli.Handlers().Child()")
	// The child is still its own standalone binary.
	mustContain(t, filepath.Join(tmp, "cmd/child/main.go"), `Bind("parser", rotini.NewParser())`, "Execute()")
}
