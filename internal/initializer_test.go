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

// TestInitialize_seedsOnly verifies the whole `rotini init` job: it writes ONLY
// the seed spec and conf — no code generation. The seed is "batteries-declared":
// the spec declares the help/version/completion commands + root -h/-v flags, and
// the conf declares an entrypoint with the help/completion features enabled (man/
// markdown off). NO generated files (entrypoint, handlers, codegen) are written —
// that is `rotini generate`'s job.
func TestInitialize_seedsOnly(t *testing.T) {
	tmp := initTestModule(t)
	if err := Initialize("mycli", "yaml", false, ""); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	dir := filepath.Join(tmp, "cmd", "mycli")
	// Batteries-declared spec: root + help/version/completion commands and flags.
	mustContain(t, filepath.Join(dir, ".rotini.spec.yaml"),
		"name: mycli", "schema-spec.json", "commands:",
		"name: help", "name: version", "name: completion", "name: shell",
		"--help", "--version", "- bash", "- zsh", "- fish", "- powershell")
	// Conf declares the entrypoint and enables help+completion (man/markdown off).
	mustContain(t, filepath.Join(dir, ".rotini.conf.yaml"),
		"schema-conf.json",
		"package: cmd/mycli", "package: internal/cmd/mycli",
		"file: main.go", "file: zz_rotini.gen.go",
		"help:\n      enabled: true",
		"completion:\n      enabled: true",
		"man:\n      enabled: false",
		"embed_dir: internal/cmd/mycli/renders")

	// Init does NOT generate: no entrypoint, no handlers, no codegen file.
	for _, p := range []string{
		filepath.Join(dir, "main.go"),
		filepath.Join(tmp, "internal", "cmd", "mycli", "zz_rotini.gen.go"),
		filepath.Join(tmp, "internal", "cmd", "mycli", "mycli.go"),
	} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s should NOT exist after init (init seeds only; stat err = %v)", p, err)
		}
	}
}

// TestInitialize_thenGenerate is the W5 acceptance: `rotini generate` run over
// nothing but the two seed files produces a complete CLI — the entrypoint main.go,
// the codegen file, and one EMPTY handler stub per command (root + help/version/
// completion). The stubs carry no wiring; the author fills them.
func TestInitialize_thenGenerate(t *testing.T) {
	tmp := initTestModule(t)
	if err := Initialize("mycli", "yaml", false, ""); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	specPath := filepath.Join(tmp, "cmd", "mycli", ".rotini.spec.yaml")
	confPath := filepath.Join(tmp, "cmd", "mycli", ".rotini.conf.yaml")
	if err := Generate(specPath, confPath, false, "", nil); err != nil {
		t.Fatalf("Generate after init: %v", err)
	}

	// Entrypoint + codegen exist.
	mustContain(t, filepath.Join(tmp, "cmd", "mycli", "main.go"), "Execute()")
	genDir := filepath.Join(tmp, "internal", "cmd", "mycli")
	mustContain(t, filepath.Join(genDir, "zz_rotini.gen.go"),
		"package mycli", "var Program = NewProgram(&handlers{})")

	// Every command gets an EMPTY stub — no MustGet/parse/help wiring.
	for _, f := range []string{"mycli.go", "mycli_help.go", "mycli_version.go", "mycli_completion.go"} {
		path := filepath.Join(genDir, f)
		mustContain(t, path, "rotini.CommandHandlers", "func (*", "Run(ctx context.Context")
		mustNotContain(t, path, "MustGet", "Parse", "HelpMycli")
	}
}

// TestGenerate_preservesEditedHandlers confirms a re-run of `rotini generate`
// never overwrites an existing handler file (create-once, like main.go) — only
// the codegen file is rewritten.
func TestGenerate_preservesEditedHandlers(t *testing.T) {
	tmp := initTestModule(t)
	if err := Initialize("mycli", "yaml", false, ""); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	specPath := filepath.Join(tmp, "cmd", "mycli", ".rotini.spec.yaml")
	confPath := filepath.Join(tmp, "cmd", "mycli", ".rotini.conf.yaml")
	if err := Generate(specPath, confPath, false, "", nil); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	stub := filepath.Join(tmp, "internal", "cmd", "mycli", "mycli.go")
	writeTestFile(t, stub, "package mycli\n\n// EDITED BY USER\n")
	if err := Generate(specPath, confPath, false, "", nil); err != nil {
		t.Fatalf("re-generate: %v", err)
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
