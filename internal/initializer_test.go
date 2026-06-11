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

// TestInitialize_scaffolds verifies the whole `rotini init` job: the seed spec
// (root command named after the CLI, with the default version/help flags AND the
// version/help sub-commands) and the seed conf (package layout + feature
// toggles) under cmd/<name>/, validated, then generated init-style — the
// generated package, the WIRED root/help/version handlers, and the entrypoint
// main.go, all ready to go.
func TestInitialize_scaffolds(t *testing.T) {
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
		"dir: internal/cmd/mycli/embed")

	// The init-style generate ran: framework + rollup, wired handlers, help
	// pages, and the entrypoint main.go.
	genDir := filepath.Join(tmp, "internal", "cmd", "mycli")
	mustContain(t, filepath.Join(genDir, "zz_rotini.gen.go"),
		"package mycli", "type ProgramHandlers interface", "var definition",
		"var Program = NewProgram(&handlers{})",
		"var HelpMycli string", "var HelpMycliHelp string", "var HelpMycliVersion string")
	// Root handler: -h/--help and -v/--version wired, default prints help.
	mustContain(t, filepath.Join(genDir, "mycli.go"),
		"type mycliHandlers struct {",
		"var inputs MycliInputs",
		"case flags.Help:", "case flags.Version:",
		"HelpMycli", "build.VersionSemantic")
	// help command handler: wired to the generated Help resolver.
	mustContain(t, filepath.Join(genDir, "mycli_help.go"),
		"type mycliHelpHandlers struct {",
		"Help(args.Commands...)", "HelpMycliHelp")
	// version command handler: prints the bound build version.
	mustContain(t, filepath.Join(genDir, "mycli_version.go"),
		"type mycliVersionHandlers struct {",
		"build.VersionSemantic", "HelpMycliVersion")
	// Entrypoint main.go written to the conf-declared entrypoint package.
	mustContain(t, filepath.Join(dir, "main.go"),
		"//go:generate go tool rotini generate",
		`cli "example.com/myclis/internal/cmd/mycli"`,
		`Bind("parser", rotini.NewParser())`,
		`Bind("build", rotini.BuildInfo(version))`,
		"Execute()")
}

// TestInitialize_optOutWiredHandlers covers the documented opt-out: delete the
// wired root/help/version handler files and run a normal `rotini generate` —
// the empty handler stubs are seeded in their place (and main.go, a create-once
// file, survives).
func TestInitialize_optOutWiredHandlers(t *testing.T) {
	tmp := initTestModule(t)
	if err := Initialize("mycli", "yaml", false, ""); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	genDir := filepath.Join(tmp, "internal", "cmd", "mycli")
	for _, f := range []string{"mycli.go", "mycli_help.go", "mycli_version.go"} {
		if err := os.Remove(filepath.Join(genDir, f)); err != nil {
			t.Fatalf("remove wired handler %s: %v", f, err)
		}
	}

	specPath := filepath.Join(tmp, "cmd", "mycli", ".rotini.spec.yaml")
	confPath := filepath.Join(tmp, "cmd", "mycli", ".rotini.conf.yaml")
	if err := Generate(specPath, confPath, false, "", nil); err != nil {
		t.Fatalf("Generate after opt-out: %v", err)
	}

	// Empty stubs replace the wired handlers — no parsing, no help/version wiring.
	for _, f := range []string{"mycli.go", "mycli_help.go", "mycli_version.go"} {
		path := filepath.Join(genDir, f)
		mustContain(t, path, "rotini.CommandHandlers", "func (*")
		mustNotContain(t, path, "MustGet", "HelpMycli")
	}
	if _, err := os.Stat(filepath.Join(tmp, "cmd", "mycli", "main.go")); err != nil {
		t.Errorf("main.go should survive a regenerate: %v", err)
	}
}

// TestInitialize_preservesEditedHandlers confirms a force re-init never
// overwrites existing handler files (they are create-once, like any stub) —
// force applies only to the seed spec/conf.
func TestInitialize_preservesEditedHandlers(t *testing.T) {
	tmp := initTestModule(t)
	if err := Initialize("mycli", "yaml", false, ""); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	stub := filepath.Join(tmp, "internal", "cmd", "mycli", "mycli.go")
	writeTestFile(t, stub, "package mycli\n\n// EDITED BY USER\n")

	if err := Initialize("mycli", "yaml", true, ""); err != nil {
		t.Fatalf("re-init with force: %v", err)
	}
	mustContain(t, stub, "EDITED BY USER")
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
	// The name becomes a directory, a Go package, and the root command — unsafe
	// names are rejected before anything touches the filesystem.
	for _, bad := range []string{"../evil", "a/b", "my cli", "9lives", ".hidden"} {
		if err := Initialize(bad, "yaml", false, ""); err == nil || !strings.Contains(err.Error(), "invalid CLI name") {
			t.Errorf("Initialize(%q) = %v, want an invalid-name error", bad, err)
		}
	}
}

func TestInitialize_outsideModule(t *testing.T) {
	t.Chdir(t.TempDir()) // no go.mod up the tree
	if err := Initialize("mycli", "yaml", false, ""); err == nil {
		t.Error("Initialize outside a module should error")
	}
}
