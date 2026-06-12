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

// TestInitialize_scaffolds verifies the whole `rotini init` job: the seed spec
// (root command named after the CLI, with the default version/help flags and
// the help sub-command — the version COMMAND is `--wire version`) and the seed
// conf (package layout + feature toggles) under cmd/<name>/, validated, then
// generated init-style — the generated package, the WIRED root/help handlers,
// and the entrypoint main.go, all ready to go.
func TestInitialize_scaffolds(t *testing.T) {
	tmp := initTestModule(t)
	if err := Initialize("mycli", "yaml", false, "", nil); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	dir := filepath.Join(tmp, "cmd", "mycli")
	mustContain(t, filepath.Join(dir, ".rotini.spec.yaml"),
		"name: mycli", "schema-spec.json",
		"--version", "name: help", "--help")
	// The version COMMAND is wire-gated (`--wire version`); the root
	// -v/--version FLAG above is default wiring. (The flag is also literally
	// "name: version", so the needle is the command-indented two-line shape.)
	mustNotContain(t, filepath.Join(dir, ".rotini.spec.yaml"), "    - name: version\n      summary: print version")
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
		"var HelpMycli string", "var HelpMycliHelp string")
	// Root handler: -h/--help and -v/--version wired, default prints help.
	mustContain(t, filepath.Join(genDir, "mycli.go"),
		"type mycliHandlers struct {",
		"var inputs MycliInputs",
		"case flags.Help:", "case flags.Version:",
		"HelpMycli", "v.VersionSemantic")
	// help command handler: wired to the generated Help resolver.
	mustContain(t, filepath.Join(genDir, "mycli_help.go"),
		"type mycliHelpHandlers struct {",
		"Help(args.Commands...)", "HelpMycliHelp")
	// No version command handler by default — it rides `--wire version`.
	if _, err := os.Stat(filepath.Join(genDir, "mycli_version.go")); !os.IsNotExist(err) {
		t.Errorf("mycli_version.go should not exist on a default init (stat err = %v)", err)
	}
	// Entrypoint main.go written to the conf-declared entrypoint package.
	mustContain(t, filepath.Join(dir, "main.go"),
		"//go:generate go tool rotini generate",
		`cli "example.com/myclis/internal/cmd/mycli"`,
		`Bind(rotini.KeyParser, rotini.NewParser())`,
		`Bind(rotini.KeyVersioner, rotini.NewVersioner(version))`,
		"Execute()")
}

// TestInitialize_wire covers `rotini init --wire`: "completion" and "version"
// seed their commands + WIRED handlers; "man" and "markdown" only flip their
// conf feature toggles (no command — a man-printing command is
// unconventional); "help" is default-wired and idempotent; "all" expands to
// everything.
func TestInitialize_wire(t *testing.T) {
	tmp := initTestModule(t)
	if err := Initialize("mycli", "yaml", false, "", []string{"all"}); err != nil {
		t.Fatalf("Initialize(--wire all): %v", err)
	}

	dir := filepath.Join(tmp, "cmd", "mycli")
	mustContain(t, filepath.Join(dir, ".rotini.spec.yaml"),
		"name: completion", "name: shell",
		"    - name: version\n      summary: print version", // the COMMAND, not the root flag
		"- bash", "- zsh", "- fish", "- powershell")
	// man is feature-only: no command in the spec.
	mustNotContain(t, filepath.Join(dir, ".rotini.spec.yaml"), "name: man")
	// Every wired feature toggled on in the conf (help was already true).
	conf := readFileString(t, filepath.Join(dir, ".rotini.conf.yaml"))
	for _, feat := range []string{"help", "man", "completion", "markdown"} {
		if !strings.Contains(conf, feat+":\n      enabled: true") {
			t.Errorf("conf: feature %s not enabled:\n%s", feat, conf)
		}
	}

	genDir := filepath.Join(tmp, "internal", "cmd", "mycli")
	// The framework serves all four features…
	mustContain(t, filepath.Join(genDir, "zz_rotini.gen.go"),
		"func Man(path ...string) (string, error)",
		"func Markdown(path ...string) (string, error)",
		"func Completion(shell string) (string, error)",
		"var CompletionZsh string")
	// …the wired handlers read theirs…
	mustContain(t, filepath.Join(genDir, "mycli_completion.go"),
		"type mycliCompletionHandlers struct {",
		"Completion(args.Shell)", "HelpMycliCompletion")
	mustContain(t, filepath.Join(genDir, "mycli_version.go"),
		"type mycliVersionHandlers struct {",
		"v.VersionSemantic", "HelpMycliVersion")
	// …man has no handler file (feature-only), and markdown emitted its pages.
	if _, err := os.Stat(filepath.Join(genDir, "mycli_man.go")); !os.IsNotExist(err) {
		t.Errorf("mycli_man.go should not exist (stat err = %v)", err)
	}
	if _, err := os.Stat(filepath.Join(genDir, "embed", "markdown_mycli.md")); err != nil {
		t.Errorf("markdown feature output missing: %v", err)
	}

	// The default (no --wire) seeds none of it, and unknown values are loud.
	if err := Initialize("plain", "yaml", false, "", nil); err != nil {
		t.Fatalf("Initialize(plain): %v", err)
	}
	plainSpec := readFileString(t, filepath.Join(tmp, "cmd", "plain", ".rotini.spec.yaml"))
	for _, frag := range []string{"name: man", "name: completion"} {
		if strings.Contains(plainSpec, frag) {
			t.Errorf("default seed spec unexpectedly contains %q", frag)
		}
	}
	mustContain(t, filepath.Join(tmp, "cmd", "plain", ".rotini.conf.yaml"),
		"man:\n      enabled: false",
		"completion:\n      enabled: false",
		"markdown:\n      enabled: false")
	mustNotContain(t, filepath.Join(tmp, "cmd", "plain", ".rotini.spec.yaml"), "    - name: version\n      summary: print version")
	if err := Initialize("bad", "yaml", false, "", []string{"tui"}); err == nil || !strings.Contains(err.Error(), `unknown --wire value "tui"`) {
		t.Errorf("Initialize(--wire tui) = %v, want the unknown-value rejection", err)
	}
}

// TestInitialize_optOutWiredHandlers covers the documented opt-out: delete the
// wired root/help/version handler files and run a normal `rotini generate` —
// the empty handler stubs are seeded in their place (and main.go, a create-once
// file, survives).
func TestInitialize_optOutWiredHandlers(t *testing.T) {
	tmp := initTestModule(t)
	if err := Initialize("mycli", "yaml", false, "", nil); err != nil {
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
