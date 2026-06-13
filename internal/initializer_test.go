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
	if err := Initialize("mycli", "", false, "", nil); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	dir := filepath.Join(tmp, "tools", "mycli")
	mustContain(t, filepath.Join(dir, ".rotini.spec.jsonc"), `"name": "mycli"`)
	mustContain(t, filepath.Join(dir, ".rotini.conf.jsonc"), `"internal/cmd/mycli"`)

	// An explicit --format overrides the conf default (still under the conf package).
	if err := Initialize("other", "yaml", false, "", nil); err != nil {
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

// TestInitialize_scaffolds verifies the whole `rotini init` job for a BARE
// init (no --with): everything is opt-in, so the seed spec is a minimal root
// (no flags, no commands), the seed conf has every feature off, the root
// handler is the plain stub, and the entrypoint main.go is written — a clean
// skeleton ready for the user's own spec work.
func TestInitialize_scaffolds(t *testing.T) {
	tmp := initTestModule(t)
	if err := Initialize("mycli", "yaml", false, "", nil); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	dir := filepath.Join(tmp, "cmd", "mycli")
	mustContain(t, filepath.Join(dir, ".rotini.spec.yaml"),
		"name: mycli", "schema-spec.json")
	mustNotContain(t, filepath.Join(dir, ".rotini.spec.yaml"),
		"--help", "--version", "name: help", "commands:")
	mustContain(t, filepath.Join(dir, ".rotini.conf.yaml"),
		"schema-conf.json",
		"package: cmd/mycli", "package: internal/cmd/mycli",
		"file: main.go", "file: zz_rotini.gen.go",
		"help:\n      enabled: false",
		"dir: internal/cmd/mycli/embed")

	// The init-style generate ran: framework + rollup, a PLAIN STUB root
	// handler (nothing wired → nothing special), and the entrypoint main.go.
	genDir := filepath.Join(tmp, "internal", "cmd", "mycli")
	mustContain(t, filepath.Join(genDir, "zz_rotini.gen.go"),
		"package mycli", "type ProgramHandlers interface", "var definition",
		"var Program = NewProgram(&handlers{})")
	mustNotContain(t, filepath.Join(genDir, "zz_rotini.gen.go"), "var HelpMycli string")
	mustContain(t, filepath.Join(genDir, "mycli.go"),
		"type mycliHandlers struct {", "rotini.CommandHandlers")
	mustNotContain(t, filepath.Join(genDir, "mycli.go"), "MustGet", "HelpMycli")
	for _, f := range []string{"mycli_help.go", "mycli_version.go"} {
		if _, err := os.Stat(filepath.Join(genDir, f)); !os.IsNotExist(err) {
			t.Errorf("%s should not exist on a bare init (stat err = %v)", f, err)
		}
	}
	// Entrypoint main.go written to the conf-declared entrypoint package.
	mustContain(t, filepath.Join(dir, "main.go"),
		"//go:generate go tool rotini generate",
		`cli "example.com/myclis/internal/cmd/mycli"`,
		`Bind(rotini.KeyParser, rotini.NewParser())`,
		`Bind(rotini.KeyVersioner, rotini.NewVersioner(version))`,
		"Execute()")
}

// TestInitialize_with covers `rotini init --with`: "help" seeds the -h flags,
// the help command, the help feature, and the wired root/help handlers;
// "version" seeds the root -v flag, the version command, and its wired
// handler; "completion" seeds its command + handler; "man"/"markdown" only
// flip conf feature toggles (no command); "all" expands to everything — and a
// PARTIAL wiring (version without help) still generates compiling, help-less
// handlers.
func TestInitialize_with(t *testing.T) {
	tmp := initTestModule(t)
	if err := Initialize("mycli", "yaml", false, "", []string{"all"}); err != nil {
		t.Fatalf("Initialize(--with all): %v", err)
	}

	dir := filepath.Join(tmp, "cmd", "mycli")
	mustContain(t, filepath.Join(dir, ".rotini.spec.yaml"),
		"name: help", "--help", "--version",
		"name: completion", "name: shell",
		"    - name: version\n      summary: print version", // the COMMAND, not the root flag
		"- bash", "- zsh", "- fish", "- powershell")
	// man is feature-only: no command in the spec.
	mustNotContain(t, filepath.Join(dir, ".rotini.spec.yaml"), "name: man")
	// Every wired feature toggled on in the conf.
	conf := readFileString(t, filepath.Join(dir, ".rotini.conf.yaml"))
	for _, feat := range []string{"help", "man", "completion", "markdown"} {
		if !strings.Contains(conf, feat+":\n      enabled: true") {
			t.Errorf("conf: feature %s not enabled:\n%s", feat, conf)
		}
	}

	genDir := filepath.Join(tmp, "internal", "cmd", "mycli")
	// The framework serves all four features. The seed conf template uses
	// embed:false for help+completion (inline string vars) and embed:true for
	// man+markdown (//go:embed-backed vars) — so the var forms differ by mode.
	mustContain(t, filepath.Join(genDir, "zz_rotini.gen.go"),
		"var HelpMycli =", // inline (help embed:false)
		"func Help(path ...string) (string, error)",
		"func Man(path ...string) (string, error)",
		"func Markdown(path ...string) (string, error)",
		"func Completion(shell string) (string, error)",
		"var CompletionZsh =") // inline (completion embed:false)
	// …the wired handlers read theirs…
	mustContain(t, filepath.Join(genDir, "mycli.go"),
		"case flags.Help:", "case flags.Version:", "HelpMycli", "v.VersionSemantic")
	mustContain(t, filepath.Join(genDir, "mycli_help.go"),
		"Help(args.Commands...)", "HelpMycliHelp")
	mustContain(t, filepath.Join(genDir, "mycli_completion.go"),
		"Arguments.Shell)", "HelpMycliCompletion")
	mustContain(t, filepath.Join(genDir, "mycli_version.go"),
		"v.VersionSemantic", "HelpMycliVersion")
	// …man has no handler file (feature-only), and markdown emitted its pages.
	if _, err := os.Stat(filepath.Join(genDir, "mycli_man.go")); !os.IsNotExist(err) {
		t.Errorf("mycli_man.go should not exist (stat err = %v)", err)
	}
	if _, err := os.Stat(filepath.Join(genDir, "embed", "markdown_mycli.md")); err != nil {
		t.Errorf("markdown feature output missing: %v", err)
	}

	// Partial wiring: version WITHOUT help — the root handler gets the version
	// branch but no help branch (and no Help* embed references anywhere), and
	// the version command/handler carry no -h flag.
	if err := Initialize("partial", "yaml", false, "", []string{"version"}); err != nil {
		t.Fatalf("Initialize(--with version): %v", err)
	}
	pSpec := readFileString(t, filepath.Join(tmp, "cmd", "partial", ".rotini.spec.yaml"))
	if strings.Contains(pSpec, "--help") || strings.Contains(pSpec, "name: help") {
		t.Errorf("version-only seed spec carries help wiring:\n%s", pSpec)
	}
	pGen := filepath.Join(tmp, "internal", "cmd", "partial")
	mustContain(t, filepath.Join(pGen, "partial.go"), "case flags.Version:")
	mustNotContain(t, filepath.Join(pGen, "partial.go"), "flags.Help", "HelpPartial")
	mustContain(t, filepath.Join(pGen, "partial_version.go"), "v.VersionSemantic")
	mustNotContain(t, filepath.Join(pGen, "partial_version.go"), "Help")

	// Unknown values are loud.
	if err := Initialize("bad", "yaml", false, "", []string{"tui"}); err == nil || !strings.Contains(err.Error(), `unknown --with value "tui"`) {
		t.Errorf("Initialize(--with tui) = %v, want the unknown-value rejection", err)
	}
}

// TestInitialize_optOutWiredHandlers covers the documented opt-out: delete the
// wired root/help/version handler files and run a normal `rotini generate` —
// the empty handler stubs are seeded in their place (and main.go, a create-once
// file, survives).
func TestInitialize_optOutWiredHandlers(t *testing.T) {
	tmp := initTestModule(t)
	if err := Initialize("mycli", "yaml", false, "", []string{"help"}); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	genDir := filepath.Join(tmp, "internal", "cmd", "mycli")
	for _, f := range []string{"mycli.go", "mycli_help.go"} {
		if err := os.Remove(filepath.Join(genDir, f)); err != nil {
			t.Fatalf("remove wired handler %s: %v", f, err)
		}
	}

	specPath := filepath.Join(tmp, "cmd", "mycli", ".rotini.spec.yaml")
	confPath := filepath.Join(tmp, "cmd", "mycli", ".rotini.conf.yaml")
	if err := Generate(specPath, confPath, false, "", nil); err != nil {
		t.Fatalf("Generate after opt-out: %v", err)
	}

	// Empty stubs replace the wired handlers — no parsing, no help wiring.
	for _, f := range []string{"mycli.go", "mycli_help.go"} {
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
	if err := Initialize("mycli", "yaml", false, "", nil); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	stub := filepath.Join(tmp, "internal", "cmd", "mycli", "mycli.go")
	writeTestFile(t, stub, "package mycli\n\n// EDITED BY USER\n")

	if err := Initialize("mycli", "yaml", true, "", nil); err != nil {
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

	if err := Initialize("rel", "yaml", false, "1.4.0", nil); err != nil {
		t.Fatalf("Initialize(ref=1.4.0): %v", err)
	}
	relDir := filepath.Join(tmp, "cmd", "rel")
	mustContain(t, filepath.Join(relDir, ".rotini.spec.yaml"), "refs/tags/1.4.0/schema-spec.json")
	mustContain(t, filepath.Join(relDir, ".rotini.conf.yaml"), "refs/tags/1.4.0/schema-conf.json")

	if err := Initialize("dev", "yaml", false, "", nil); err != nil {
		t.Fatalf("Initialize(ref=\"\"): %v", err)
	}
	devDir := filepath.Join(tmp, "cmd", "dev")
	mustContain(t, filepath.Join(devDir, ".rotini.spec.yaml"), "refs/tags/0.0.0/schema-spec.json")
	mustContain(t, filepath.Join(devDir, ".rotini.conf.yaml"), "refs/tags/0.0.0/schema-conf.json")
}

func TestInitialize_noClobberThenForce(t *testing.T) {
	initTestModule(t)
	if err := Initialize("mycli", "yaml", false, "", nil); err != nil {
		t.Fatalf("first Initialize: %v", err)
	}
	err := Initialize("mycli", "yaml", false, "", nil)
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("re-init without force: got %v, want 'already exists'", err)
	}
	if err := Initialize("mycli", "yaml", true, "", nil); err != nil {
		t.Fatalf("re-init with force: %v", err)
	}
}

func TestInitialize_formatJSON(t *testing.T) {
	tmp := initTestModule(t)
	if err := Initialize("tool", "json", false, "", nil); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	mustContain(t, filepath.Join(tmp, "cmd", "tool", ".rotini.spec.json"), `"name": "tool"`)
	mustContain(t, filepath.Join(tmp, "cmd", "tool", ".rotini.conf.json"), `"internal/cmd/tool"`)
}

// TestInitialize_formatTOML covers the toml seed path: the YAML templates are
// transcoded to TOML (via yaml→json→toml).
func TestInitialize_formatTOML(t *testing.T) {
	tmp := initTestModule(t)
	if err := Initialize("tool", "toml", false, "", nil); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	mustContain(t, filepath.Join(tmp, "cmd", "tool", ".rotini.spec.toml"), `name = "tool"`)
	mustContain(t, filepath.Join(tmp, "cmd", "tool", ".rotini.conf.toml"), "internal/cmd/tool")
}

func TestInitialize_errors(t *testing.T) {
	initTestModule(t)
	if err := Initialize("", "yaml", false, "", nil); err == nil {
		t.Error("empty name should error")
	}
	if err := Initialize("x", "xml", false, "", nil); err == nil {
		t.Error("unsupported format should error")
	}
	// The name becomes a directory, a Go package, and the root command — unsafe
	// names are rejected before anything touches the filesystem.
	for _, bad := range []string{"../evil", "a/b", "my cli", "9lives", ".hidden"} {
		if err := Initialize(bad, "yaml", false, "", nil); err == nil || !strings.Contains(err.Error(), "invalid CLI name") {
			t.Errorf("Initialize(%q) = %v, want an invalid-name error", bad, err)
		}
	}
}

func TestInitialize_outsideModule(t *testing.T) {
	t.Chdir(t.TempDir()) // no go.mod up the tree
	if err := Initialize("mycli", "yaml", false, "", nil); err == nil {
		t.Error("Initialize outside a module should error")
	}
}
