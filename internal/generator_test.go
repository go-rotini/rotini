package internal

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// companionConf points generation at the same package layout as the committed
// rotini companion CLI so the output can be compared against it.
const companionConf = `$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-conf.json
generate:
  packages:
    main:
      package: cmd/rotini
      file: main.go
    cmd:
      package: internal/cmd/rotini
      file: zz_rotini.gen.go
    cmdgen:
      package: internal/cmd/rotini
      file: zz_rotini.gen.go
  features:
    help:
      enabled: true
      embed: false
      embed_dir: internal/cmd/rotini/renders
      template: false
      template_dir: internal/cmd/rotini/templates
    completion:
      enabled: true
      embed: false
      embed_dir: internal/cmd/rotini/renders
      template: false
      template_dir: internal/cmd/rotini/templates
    man:
      enabled: false
      embed: false
      embed_dir: internal/cmd/rotini/renders
      template: false
      template_dir: internal/cmd/rotini/templates
    markdown:
      enabled: false
      embed: false
      embed_dir: internal/cmd/rotini/renders
      template: false
      template_dir: internal/cmd/rotini/templates
`

// minimalGoMod uses the same module path the committed handlers rollup imports,
// so the generated framework import path matches the golden file byte-for-byte.
const minimalGoMod = "module github.com/go-rotini/rotini\n\ngo 1.26\n"

// confSchemaHeader is the conf $schema line every test conf must carry — generate now
// validates the conf (when present) before codegen, and schema-conf.json requires $schema.
const confSchemaHeader = "$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-conf.json\n"

// TestGenerateMatchesCompanionExample generates from the committed companion
// spec into a throwaway module and asserts the output matches the hand-written
// example files that define the target shape.
func TestGenerateMatchesCompanionExample(t *testing.T) {
	repoRoot := repoRoot(t)
	specPath := filepath.Join(repoRoot, "cmd", "rotini", ".rotini.spec.yaml")

	tmp := t.TempDir()
	writeTestFile(t, filepath.Join(tmp, "go.mod"), minimalGoMod)
	confPath := filepath.Join(tmp, ".rotini.conf.yaml")
	writeTestFile(t, confPath, companionConf)

	t.Chdir(tmp)
	if err := Generate(specPath, confPath, false, "", nil); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	// The always-(re)generated file (the combined framework + rollup) is reproduced
	// byte-for-byte.
	for _, rel := range []string{
		"internal/cmd/rotini/zz_rotini.gen.go",
	} {
		assertGoEqual(t, filepath.Join(tmp, rel), filepath.Join(repoRoot, rel))
	}

	// The companion's features are all inline (help/completion embed:false; man/
	// markdown disabled), so there are no feature files on disk — the dogfooded
	// output lives in the gen file, already compared byte-for-byte above.

	// Handler stubs are create-if-missing user code, so the committed copies are
	// edited (wired to internal funcs) and intentionally diverge from a fresh
	// stub. Assert the generator produced each with the expected stub shape.
	for _, name := range []string{
		"rotini", "rotini_generate",
		"rotini_help", "rotini_initialize", "rotini_validate", "rotini_version",
	} {
		mustContain(t, filepath.Join(tmp, "internal/cmd/rotini", name+".go"),
			"package rotini", "rotini.Handlers", "*rotini.Context")
	}
}

// TestGenerateDefaultLayout verifies that, with no conf alongside the spec and
// none supplied, the sane defaults place the combined framework + rollup file at
// internal/cmd/<root>/zz_rotini.gen.go (relative to the module root).
func TestGenerateDefaultLayout(t *testing.T) {
	tmp := t.TempDir()
	writeTestFile(t, filepath.Join(tmp, "go.mod"), minimalGoMod)
	// A spec with NO adjacent conf, so generation falls back to the defaults.
	writeTestFile(t, filepath.Join(tmp, ".rotini.spec.yaml"),
		"$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-spec.json\nname: rotini\ncommands:\n  - name: generate\n")

	t.Chdir(tmp)
	if err := Generate(".rotini.spec.yaml", "", false, "", nil); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	gen := filepath.Join(tmp, "internal", "cmd", "rotini", "zz_rotini.gen.go")
	mustContain(t, gen, "package rotini", "type ProgramHandlers interface", "var Program = NewProgram(&handlers{})")
	mustContain(t, filepath.Join(tmp, "internal", "cmd", "rotini", "rotini_generate.go"),
		"type rotiniGenerateHandlers struct {", "rotini.DefaultPreRun")
}

// TestGenerateEscapesReservedFilenames verifies that a command whose name would make
// the go tool read its handler stub specially — a "_test.go" test file, or a
// "_<GOOS>.go"/"_<GOARCH>.go" build-constrained file — gets a trailing-underscore
// escape, so the stub compiles into the ordinary build like any other handler.
func TestGenerateEscapesReservedFilenames(t *testing.T) {
	tmp := t.TempDir()
	writeTestFile(t, filepath.Join(tmp, "go.mod"), minimalGoMod)
	writeTestFile(t, filepath.Join(tmp, ".rotini.spec.yaml"),
		"$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-spec.json\n"+
			"name: app\ncommands:\n  - name: test\n  - name: windows\n  - name: build\n")

	t.Chdir(tmp)
	if err := Generate(".rotini.spec.yaml", "", false, "", nil); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	dir := filepath.Join(tmp, "internal", "cmd", "app")
	// Reserved-name commands (_test / a GOOS) are escaped with a trailing underscore;
	// the un-escaped names the go tool would treat specially are never produced.
	for _, f := range []string{"app_test_.go", "app_windows_.go"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("expected escaped stub %s: %v", f, err)
		}
	}
	for _, f := range []string{"app_test.go", "app_windows.go"} {
		if _, err := os.Stat(filepath.Join(dir, f)); !os.IsNotExist(err) {
			t.Errorf("reserved stub %s should not be produced (err=%v)", f, err)
		}
	}
	// A normal command name is untouched, and the rollup wires every handler.
	if _, err := os.Stat(filepath.Join(dir, "app_build.go")); err != nil {
		t.Errorf("expected normal stub app_build.go: %v", err)
	}
	mustContain(t, filepath.Join(dir, "zz_rotini.gen.go"),
		"return &appTestHandlers{}", "return &appWindowsHandlers{}", "return &appBuildHandlers{}")
}

// TestGenerateFilenameOverride verifies a command's `filename` override names its
// generated handler stub, replacing the path-derived default.
func TestGenerateFilenameOverride(t *testing.T) {
	tmp := t.TempDir()
	writeTestFile(t, filepath.Join(tmp, "go.mod"), minimalGoMod)
	writeTestFile(t, filepath.Join(tmp, ".rotini.spec.yaml"),
		"$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-spec.json\n"+
			"name: app\ncommands:\n  - name: build\n    filename: build_handlers.go\n")

	t.Chdir(tmp)
	if err := Generate(".rotini.spec.yaml", "", false, "", nil); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	dir := filepath.Join(tmp, "internal", "cmd", "app")
	mustContain(t, filepath.Join(dir, "build_handlers.go"), "type appBuildHandlers struct {")
	if _, err := os.Stat(filepath.Join(dir, "app_build.go")); !os.IsNotExist(err) {
		t.Errorf("derived stub app_build.go should not be produced when overridden (err=%v)", err)
	}
}

// TestGenerateTwoFilesOnePackage covers the middle layout: cli and cmdgen name
// the SAME package but DIFFERENT files. The framework and the rollup are written
// as two files in one package, and — because they share a package — the rollup
// refers to the framework unqualified (no "cmdgen." prefix, no second import) and
// no combined zz_rotini.gen.go is produced.
func TestGenerateTwoFilesOnePackage(t *testing.T) {
	tmp := t.TempDir()
	writeTestFile(t, filepath.Join(tmp, "go.mod"), minimalGoMod)
	writeTestFile(t, filepath.Join(tmp, ".rotini.spec.yaml"),
		"$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-spec.json\nname: mycli\ncommands:\n  - name: build\n")
	// Same package ("app"), distinct files → two files, one package.
	writeTestFile(t, filepath.Join(tmp, ".rotini.conf.yaml"),
		confSchemaHeader+"generate:\n  packages:\n    cmd:\n      package: cmd/mycli/app\n      file: handlers.gen.go\n    cmdgen:\n      package: cmd/mycli/app\n      file: framework.gen.go\n")
	t.Chdir(tmp)

	if err := Generate(".rotini.spec.yaml", ".rotini.conf.yaml", false, "", nil); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	dir := filepath.Join(tmp, "cmd", "mycli", "app")
	framework := filepath.Join(dir, "framework.gen.go")
	rollup := filepath.Join(dir, "handlers.gen.go")

	// The framework file holds the typed surface; the rollup holds the handlers.
	// Both declare package "app".
	mustContain(t, framework, "package app", "type ProgramHandlers interface", "func NewProgram(handlers ProgramHandlers)")
	mustContain(t, rollup, "package app", "type handlers struct{}", "var Program = NewProgram(&handlers{})")

	// Same-package refs are unqualified: the rollup must not qualify the framework
	// with a "cmdgen." selector or import a separate framework package.
	rollupSrc, err := os.ReadFile(rollup)
	if err != nil {
		t.Fatalf("read rollup: %v", err)
	}
	if bytes.Contains(rollupSrc, []byte("cmdgen.")) {
		t.Errorf("rollup qualifies framework refs with a package selector; want unqualified same-package refs:\n%s", rollupSrc)
	}

	// No combined file in this layout.
	if _, statErr := os.Stat(filepath.Join(dir, "zz_rotini.gen.go")); statErr == nil {
		t.Error("two-file layout unexpectedly produced a combined zz_rotini.gen.go")
	}

	// Both files are syntactically valid Go (readAndFormat gofmt-parses, failing the
	// test otherwise), confirming the split surface forms a well-formed package.
	readAndFormat(t, framework)
	readAndFormat(t, rollup)
}

// TestGenerateRejectsInvalidSpec confirms codegen is gated on validation: an invalid spec
// (here a duplicate flag identifier the lints catch) is rejected before any files are written.
func TestGenerateRejectsInvalidSpec(t *testing.T) {
	tmp := t.TempDir()
	writeTestFile(t, filepath.Join(tmp, "go.mod"), minimalGoMod)
	writeTestFile(t, filepath.Join(tmp, ".rotini.spec.yaml"),
		"$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-spec.json\n"+
			"name: mycli\nflags:\n  - name: a\n    identifiers: [\"-x\"]\n  - name: b\n    identifiers: [\"-x\"]\n")
	t.Chdir(tmp)

	err := Generate(".rotini.spec.yaml", "", false, "", nil)
	if err == nil {
		t.Fatal("Generate on an invalid spec = nil, want a validation error")
	}
	if !strings.Contains(err.Error(), "identifier") {
		t.Errorf("error = %v, want a duplicate-identifier validation error", err)
	}
	if _, statErr := os.Stat(filepath.Join(tmp, "internal", "cmd", "mycli", "zz_rotini.gen.go")); statErr == nil {
		t.Error("codegen wrote the gen file despite the invalid spec — validation did not gate generation")
	}
}

// TestGenerateRejectsInvalidConf confirms a present-but-invalid conf is rejected before codegen
// (here a conf missing the required $schema); no files are written.
func TestGenerateRejectsInvalidConf(t *testing.T) {
	tmp := t.TempDir()
	writeTestFile(t, filepath.Join(tmp, "go.mod"), minimalGoMod)
	writeTestFile(t, filepath.Join(tmp, ".rotini.spec.yaml"),
		"$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-spec.json\nname: mycli\n")
	writeTestFile(t, filepath.Join(tmp, ".rotini.conf.yaml"), "generate:\n  packages:\n    cmdgen:\n      file: rotini.go\n") // no $schema
	t.Chdir(tmp)

	err := Generate(".rotini.spec.yaml", ".rotini.conf.yaml", false, "", nil)
	if err == nil {
		t.Fatal("Generate with an invalid conf = nil, want a validation error")
	}
	if !strings.Contains(err.Error(), "conf") {
		t.Errorf("error = %v, want a conf validation error", err)
	}
	if _, statErr := os.Stat(filepath.Join(tmp, "internal", "cmd", "mycli", "zz_rotini.gen.go")); statErr == nil {
		t.Error("codegen wrote files despite the invalid conf")
	}
}

// TestGenerateGuardsSchemaVersion confirms generate inherits the Item-3 $schema↔binary
// version guard (it validates first): a non-empty release ref that differs from the spec's
// $schema version rejects the run before any files are written; a matching ref proceeds; and
// an empty ref (dev/pseudo build) skips the guard.
func TestGenerateGuardsSchemaVersion(t *testing.T) {
	tmp := t.TempDir()
	writeTestFile(t, filepath.Join(tmp, "go.mod"), minimalGoMod)
	writeTestFile(t, filepath.Join(tmp, ".rotini.spec.yaml"),
		"$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-spec.json\nname: mycli\n")
	t.Chdir(tmp)
	gen := filepath.Join(tmp, "internal", "cmd", "mycli", "zz_rotini.gen.go")

	err := Generate(".rotini.spec.yaml", "", false, "1.0.0", nil)
	if err == nil {
		t.Fatal("Generate(ref 1.0.0 vs $schema 0.0.0) = nil, want a $schema mismatch error")
	}
	if !strings.Contains(err.Error(), "$schema") {
		t.Errorf("error = %v, want a $schema version error", err)
	}
	if _, statErr := os.Stat(gen); statErr == nil {
		t.Error("codegen wrote files despite the $schema version mismatch")
	}

	if err := Generate(".rotini.spec.yaml", "", false, "0.0.0", nil); err != nil {
		t.Fatalf("Generate(matching ref 0.0.0) = %v, want nil", err)
	}
	mustContain(t, gen, "package mycli")
}

// TestGenerateMissingConfPathUsesDefaults confirms the conf is optional: a -c path that doesn't
// exist is not a validation error — generation proceeds with the built-in defaults.
func TestGenerateMissingConfPathUsesDefaults(t *testing.T) {
	tmp := t.TempDir()
	writeTestFile(t, filepath.Join(tmp, "go.mod"), minimalGoMod)
	writeTestFile(t, filepath.Join(tmp, ".rotini.spec.yaml"),
		"$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-spec.json\nname: mycli\n")
	t.Chdir(tmp)

	if err := Generate(".rotini.spec.yaml", "does-not-exist.conf.yaml", false, "", nil); err != nil {
		t.Fatalf("Generate with a missing conf path = %v, want nil (defaults used)", err)
	}
	mustContain(t, filepath.Join(tmp, "internal", "cmd", "mycli", "zz_rotini.gen.go"), "package mycli")
}

// TestGeneratePrunesOrphanStubs verifies that pruning (implicit/always-on) drops
// a stub that no longer maps to a command, while rth.keep files survive and
// existing command stubs are left untouched.
func TestGeneratePrunesOrphanStubs(t *testing.T) {
	repoRoot := repoRoot(t)
	specPath := filepath.Join(repoRoot, "cmd", "rotini", ".rotini.spec.yaml")

	tmp := t.TempDir()
	writeTestFile(t, filepath.Join(tmp, "go.mod"), minimalGoMod)
	handlersDir := filepath.Join(tmp, "internal", "cmd", "rotini")
	orphan := filepath.Join(handlersDir, "rotini_obsolete.go")
	keep := filepath.Join(handlersDir, "help.go")
	writeTestFile(t, orphan, "package cli\n")
	writeTestFile(t, keep, "package cli\n")

	conf := confSchemaHeader + "generate:\n  packages:\n    cmd:\n      keep:\n        - help.go\n"
	confPath := filepath.Join(tmp, ".rotini.conf.yaml")
	writeTestFile(t, confPath, conf)

	t.Chdir(tmp)
	if err := Generate(specPath, confPath, false, "", nil); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Errorf("orphan stub was not pruned: %v", err)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Errorf("kept file was removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(handlersDir, "rotini_generate.go")); err != nil {
		t.Errorf("current command stub missing: %v", err)
	}
}

// helpKeepConf enables help and keeps one rtg feature-dir file by package-relative path.
const helpKeepConf = confSchemaHeader + "generate:\n  packages:\n    cmdgen:\n      keep:\n        - renders/help_legacy.txt\n  features:\n    help:\n      enabled: true\n      embed: true\n      template: true\n"

// TestGeneratePrunesOrphanHelp verifies rtg pruning (implicit/always-on): a help
// .txt for a command no longer in the spec is removed on regenerate, while the
// editable template, current commands' .txt, and rtg.keep paths survive.
func TestGeneratePrunesOrphanHelp(t *testing.T) {
	tmp := t.TempDir()
	writeTestFile(t, filepath.Join(tmp, "go.mod"), minimalGoMod)
	writeTestFile(t, filepath.Join(tmp, ".rotini.spec.yaml"), helpSpecYAML)
	writeTestFile(t, filepath.Join(tmp, ".rotini.conf.yaml"), helpKeepConf)

	t.Chdir(tmp)
	if err := Generate(".rotini.spec.yaml", ".rotini.conf.yaml", false, "", nil); err != nil {
		t.Fatalf("first Generate: %v", err)
	}

	helpDir := filepath.Join(tmp, "internal", "cmd", "mycli", "renders")
	orphan := filepath.Join(helpDir, "help_mycli_obsolete.txt")
	legacy := filepath.Join(helpDir, "help_legacy.txt")
	writeTestFile(t, orphan, "stale\n")
	writeTestFile(t, legacy, "kept\n")

	if err := Generate(".rotini.spec.yaml", ".rotini.conf.yaml", false, "", nil); err != nil {
		t.Fatalf("second Generate: %v", err)
	}

	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Errorf("orphan help .txt was not pruned: %v", err)
	}
	if _, err := os.Stat(legacy); err != nil {
		t.Errorf("rtg.keep file was removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(helpDir), "templates", helpTemplateName)); err != nil {
		t.Errorf("editable help template was removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(helpDir, "help_mycli.txt")); err != nil {
		t.Errorf("current command help page missing: %v", err)
	}
}

// completionConf enables only the completion feature.
const completionConf = confSchemaHeader + "generate:\n  features:\n    completion:\n      enabled: true\n      embed: true\n"

// TestGenerateCompletionEnabled verifies the features group's exception: completion
// emits per-shell embed vars + a shell-keyed resolver (not a command-path one),
// writes a script per supported shell with the program name substituted, and seeds
// no editable template (rotini-owned, Q9 = no template).
func TestGenerateCompletionEnabled(t *testing.T) {
	tmp := t.TempDir()
	writeTestFile(t, filepath.Join(tmp, "go.mod"), minimalGoMod)
	writeTestFile(t, filepath.Join(tmp, ".rotini.spec.yaml"), helpSpecYAML)
	writeTestFile(t, filepath.Join(tmp, ".rotini.conf.yaml"), completionConf)

	t.Chdir(tmp)
	if err := Generate(".rotini.spec.yaml", ".rotini.conf.yaml", false, "", nil); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	compDir := filepath.Join(tmp, "internal", "cmd", "mycli", "renders")
	mustContain(t, filepath.Join(tmp, "internal", "cmd", "mycli", "zz_rotini.gen.go"),
		`_ "embed"`,
		"//go:embed renders/completion_bash.txt", "var CompletionBash string",
		"var CompletionZsh string", "var CompletionFish string",
		"var CompletionPowershell string",
		"func Completion(shell string) (string, error)",
		`case "bash":`,
	)
	// Scripts written per shell, program name substituted — and each handles
	// the "name\tdescription" wire shape its shell's way: bash strips the
	// description, zsh feeds _describe, fish renders natively (the comment
	// documents it), powershell makes it the tooltip.
	mustContain(t, filepath.Join(compDir, "completion_bash.txt"),
		"mycli __complete", "complete -o default -F _mycli_complete mycli",
		`${line%%$'\t'*}`)
	mustContain(t, filepath.Join(compDir, "completion_zsh.txt"),
		"_describe 'mycli' pairs")
	mustContain(t, filepath.Join(compDir, "completion_fish.txt"),
		"fish renders that shape natively")
	mustContain(t, filepath.Join(compDir, "completion_powershell.txt"),
		"-split \"`t\", 2")
	for _, sh := range []string{"completion_bash.txt", "completion_zsh.txt", "completion_fish.txt", "completion_powershell.txt"} {
		if _, err := os.Stat(filepath.Join(compDir, sh)); err != nil {
			t.Errorf("missing completion script %s: %v", sh, err)
		}
	}
	// Completion is rotini-owned — no editable template is seeded.
	entries, err := os.ReadDir(compDir)
	if err != nil {
		t.Fatalf("read completion dir: %v", err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmpl") {
			t.Errorf("completion must not seed a template, found %s", e.Name())
		}
	}
}

// TestGenerateOutputTypes verifies the spec `output` key (and document-level
// `schemas`) generate typed Go types into the framework file: a named schema type,
// a "<Prefix>Output" alias for a bare $ref output, and a "<Prefix>Output" struct
// for an inline output — with no flag, no rendering, and no leaked root sentinel.
func TestGenerateOutputTypes(t *testing.T) {
	tmp := t.TempDir()
	writeTestFile(t, filepath.Join(tmp, "go.mod"), minimalGoMod)
	spec := "$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-spec.json\n" +
		"name: mycli\n" +
		"output:\n" +
		"  type: object\n" +
		"  properties:\n" +
		"    count: { type: integer }\n" +
		"    items: { type: array, items: { $ref: \"#/schemas/Widget\" } }\n" +
		"commands:\n" +
		"  - name: get\n" +
		"    output: { $ref: \"#/schemas/Widget\" }\n" +
		"schemas:\n" +
		"  Widget:\n" +
		"    type: object\n" +
		"    required: [id]\n" +
		"    properties:\n" +
		"      id: { type: string }\n" +
		"      size: { type: integer }\n"
	writeTestFile(t, filepath.Join(tmp, ".rotini.spec.yaml"), spec)

	t.Chdir(tmp)
	if err := Generate(".rotini.spec.yaml", "", false, "", nil); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	rotiniGo := filepath.Join(tmp, "internal", "cmd", "mycli", "zz_rotini.gen.go")
	mustContain(t, rotiniGo,
		"type Widget struct {", "`json:\"id\"`", // the named schema
		"type MycliGetOutput Widget", // sub-command bare-$ref output → named alias type
		"type MycliOutput struct {",  // root inline output → struct
		"[]Widget",                   // nested array of the named type
	)
	// The throwaway generation root never leaks into the output.
	mustNotContain(t, rotiniGo, outputRootSentinel)
}

// TestGenerateInputImports verifies the spec `import:` key drives the framework
// file's import block: explicit imports (stdlib + third-party + aliased) are emitted,
// rotini's own time-family aliases auto-import "time", and duplicates dedupe.
func TestGenerateDeprecated(t *testing.T) {
	tmp := t.TempDir()
	writeTestFile(t, filepath.Join(tmp, "go.mod"), minimalGoMod)
	spec := "$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-spec.json\n" +
		"name: app\n" +
		"commands:\n" +
		"  - name: compile\n" +
		"    aliases: [build]\n" +
		"    deprecated_identifiers: [build]\n" +
		"    flags:\n" +
		"      - name: config\n" +
		"        identifiers: [--config, --conf]\n" +
		"        deprecated_identifiers: [--conf]\n" +
		"        schema: { type: string }\n"
	writeTestFile(t, filepath.Join(tmp, ".rotini.spec.yaml"), spec)

	t.Chdir(tmp)
	if err := Generate(".rotini.spec.yaml", "", false, "", nil); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	mustContain(t, filepath.Join(tmp, "internal", "cmd", "app", "zz_rotini.gen.go"),
		`DeprecatedIdentifiers: []string{"build"}`,  // command-level → CommandDef
		`DeprecatedIdentifiers: []string{"--conf"}`, // flag-level → FlagDef
	)
}

// TestGenerateConfigSchema verifies a configuration_files entry's schema: is
// rendered as a self-contained JSON Schema into the BindMeta ConfigFile
// literal (fidelity F1), with document-level named schemas as definitions.
func TestGenerateConfigSchema(t *testing.T) {
	tmp := t.TempDir()
	writeTestFile(t, filepath.Join(tmp, "go.mod"), minimalGoMod)
	spec := "$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-spec.json\n" +
		"name: app\n" +
		"config_files:\n" +
		"  - name: main\n" +
		"    path: ~/.app.yaml\n" +
		"    format: yaml\n" +
		"    schema:\n" +
		"      type: object\n" +
		"      required: [server]\n" +
		"      properties:\n" +
		"        server: { $ref: \"#/schemas/Server\" }\n" +
		"schemas:\n" +
		"  Server:\n" +
		"    type: object\n" +
		"    properties:\n" +
		"      port: { type: integer }\n"
	writeTestFile(t, filepath.Join(tmp, ".rotini.spec.yaml"), spec)

	t.Chdir(tmp)
	if err := Generate(".rotini.spec.yaml", "", false, "", nil); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	mustContain(t, filepath.Join(tmp, "internal", "cmd", "app", "zz_rotini.gen.go"),
		`{Name: "main", Scope: "app", Path: "~/.app.yaml", Format: "yaml", Schema: `,
		`"required":["server"]`,
		`"definitions"`, `"Server"`, // named schemas resolve inside the rendered schema
	)
}

func TestGenerateMapFlag(t *testing.T) {
	tmp := t.TempDir()
	writeTestFile(t, filepath.Join(tmp, "go.mod"), minimalGoMod)
	spec := "$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-spec.json\n" +
		"name: widget\n" +
		"flags:\n" +
		"  - name: label\n" +
		"    identifiers: [--label]\n" +
		"    schema: { type: 'map[string]string' }\n" +
		"  - name: meta\n" +
		"    identifiers: [--meta]\n" +
		"    schema: { type: map }\n" // the rotini alias → map[string]any
	writeTestFile(t, filepath.Join(tmp, ".rotini.spec.yaml"), spec)

	t.Chdir(tmp)
	if err := Generate(".rotini.spec.yaml", "", false, "", nil); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	mustContain(t, filepath.Join(tmp, "internal", "cmd", "widget", "zz_rotini.gen.go"),
		"Label map[string]string", `rotini:"label"`, // explicit map type passes through
		"Meta  map[string]any", `rotini:"meta"`, // the `map` alias → map[string]any
		`Type: "map[string]string"`, // recorded in the Definition FlagDef
	)
}

func TestGenerateInputImports(t *testing.T) {
	tmp := t.TempDir()
	writeTestFile(t, filepath.Join(tmp, "go.mod"), minimalGoMod)
	spec := "$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-spec.json\n" +
		"name: widget\n" +
		"flags:\n" +
		"  - name: since\n" +
		"    identifiers: [--since]\n" +
		"    schema: { type: time.Time, import: time }\n" +
		"  - name: ttl\n" +
		"    identifiers: [--ttl]\n" +
		"    schema: { type: duration }\n" + // rotini alias → auto "time", and dedupes with the above
		"  - name: id\n" +
		"    identifiers: [--id]\n" +
		"    schema: { type: uuid.UUID, import: github.com/google/uuid }\n" +
		"  - name: home\n" +
		"    identifiers: [--home]\n" +
		"    schema: { type: urlx.URL, import: urlx net/url }\n" // aliased import
	writeTestFile(t, filepath.Join(tmp, ".rotini.spec.yaml"), spec)

	t.Chdir(tmp)
	if err := Generate(".rotini.spec.yaml", "", false, "", nil); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	rotiniGo := filepath.Join(tmp, "internal", "cmd", "widget", "zz_rotini.gen.go")
	mustContain(t, rotiniGo,
		"\"time\"",                   // time.Time + duration alias, deduped to one
		"\"github.com/google/uuid\"", // third-party
		"urlx \"net/url\"",           // aliased form rendered as `urlx "net/url"`
		"type WidgetFlags struct {",
		"uuid.UUID", "urlx.URL", // the typed flag fields
		// explicitly-imported argv fields carry the contract nudge (F5-S2);
		// the builtin `duration` alias does not.
		"// parsed via its encoding.TextUnmarshaler",
	)
	for _, line := range strings.Split(readFileString(t, rotiniGo), "\n") {
		if strings.Contains(line, "`rotini:\"ttl\"`") && strings.Contains(line, "//") {
			t.Errorf("builtin duration alias must NOT carry the contract comment: %s", line)
		}
		if strings.Contains(line, "`rotini:\"id\"`") && !strings.Contains(line, "encoding.TextUnmarshaler") {
			t.Errorf("explicitly-imported field missing the contract comment: %s", line)
		}
	}
	// "time" appears once in the import block (deduped), not twice.
	if got := readFileString(t, rotiniGo); strings.Count(got, "\t\"time\"\n") != 1 {
		t.Errorf("expected exactly one \"time\" import line, got %d:\n%s", strings.Count(got, "\t\"time\"\n"), got)
	}
}

// TestGenerateRemoteDiscovery verifies remote_discovery is emitted into the
// Definition: the root gets a default "<host>-" prefix, a sub-command keeps its
// explicit prefix + hidden flag.
func TestGenerateRemoteDiscovery(t *testing.T) {
	tmp := t.TempDir()
	writeTestFile(t, filepath.Join(tmp, "go.mod"), minimalGoMod)
	spec := "$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-spec.json\n" +
		"name: acme\n" +
		"remote_discovery:\n" +
		"  path: /opt/acme/plugins\n" +
		"  verify:\n" +
		"    version: true\n" +
		"commands:\n" +
		"  - name: cluster\n" +
		"    remote_discovery:\n" +
		"      prefix: acme-plugin-\n" +
		"      hidden: true\n"
	writeTestFile(t, filepath.Join(tmp, ".rotini.spec.yaml"), spec)

	t.Chdir(tmp)
	if err := Generate(".rotini.spec.yaml", "", false, "", nil); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	rotiniGo := filepath.Join(tmp, "internal", "cmd", "acme", "zz_rotini.gen.go")
	mustContain(t, rotiniGo,
		"RemoteDiscoveryDef{Prefix: \"acme-\"", // root: default prefix <host>-
		"Path: \"/opt/acme/plugins\"",
		"Verify: &rotini.RemoteVerify{Version: true}", // discovery-level version handshake
		"RemoteDiscoveryDef{Prefix: \"acme-plugin-\"", // sub: explicit prefix
		"Hidden: true",
	)
}

// TestGenerateInputChannels verifies the typed input channels (Phase 1, types only):
// pure env/config inputs get their own <Prefix>Env/<Prefix>Config structs (env is NOT
// folded into Flags), stdin gets a typed payload type + a *Stdin field, and
// CommandInputs gains the new fields only when the channel is declared.
// TestGenerate_configChannelCascades pins the per-scope Config channel + cascade
// codegen (D-W3.1): config inputs at two levels each get their own <Scope>Config
// struct, the BindMeta sources are scope-tagged, and the deeper command's Inputs
// NESTS the root scope — so a deploy handler reaches inputs.Acme.Config.LogLevel
// (the cascaded ancestor value) the same way it reaches ancestor flags/env.
func TestGenerate_configChannelCascades(t *testing.T) {
	tmp := t.TempDir()
	writeTestFile(t, filepath.Join(tmp, "go.mod"), minimalGoMod)
	spec := "$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-spec.json\n" +
		"name: acme\n" +
		"config_files:\n  - { name: app, path: ~/.acme.yaml }\n" +
		"config:\n  - { name: log_level, schema: { type: string, file: app, key: log.level } }\n" +
		"commands:\n  - name: deploy\n" +
		"    config_files:\n      - { name: targets, discover: { strategy: walk-up, file: .targets.yaml } }\n" +
		"    config:\n      - { name: region, schema: { type: string, file: targets, key: aws.region } }\n"
	writeTestFile(t, filepath.Join(tmp, ".rotini.spec.yaml"), spec)

	t.Chdir(tmp)
	if err := Generate(".rotini.spec.yaml", "", false, "", nil); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	gen := filepath.Join(tmp, "internal", "cmd", "acme", "zz_rotini.gen.go")
	mustContain(t, gen,
		"type AcmeConfig struct {", "LogLevel string", // root scope's config channel
		"type AcmeDeployConfig struct {", "Region string", // deploy scope's own
		"type AcmeDeployInputs struct {",        // deploy's nested inputs
		`Scope: "acme"`, `Scope: "acme/deploy"`, // BindMeta sources scope-tagged for the cascade
	)

	// The deploy command's Inputs must nest the ROOT scope's CommandInputs, so a
	// deploy handler can read the cascaded ancestor config value.
	src, err := os.ReadFile(gen)
	if err != nil {
		t.Fatal(err)
	}
	start := strings.Index(string(src), "type AcmeDeployInputs struct {")
	block := string(src)[start : start+strings.Index(string(src)[start:], "}")]
	if !strings.Contains(block, "AcmeCommandInputs") {
		t.Errorf("AcmeDeployInputs must nest the root AcmeCommandInputs:\n%s", block)
	}
}

func TestGenerateInputChannels(t *testing.T) {
	tmp := t.TempDir()
	writeTestFile(t, filepath.Join(tmp, "go.mod"), minimalGoMod)
	spec := "$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-spec.json\n" +
		"name: widget\n" +
		"flags:\n" +
		"  - name: color\n" +
		"    identifiers: [--color]\n" +
		"    schema: { type: string, key: create.color }\n" + // config-fallback flag
		"  - name: quiet\n" +
		"    identifiers: [--quiet]\n" +
		"    schema: { type: bool }\n" + // argv-only flag (no recon tag)

		"env:\n" +
		"  - name: region\n" +
		"    schema: { type: string, variable: WIDGET_REGION }\n" +
		"config:\n" +
		"  - name: endpoint\n" +
		"    schema: { type: string, file: app, key: api.endpoint }\n" +
		"  - name: token\n" +
		"    schema: { type: string, key: api.token, secret: true, required: true }\n" +
		"stdin:\n" +
		"  format: yaml\n" +
		"  schema: { $ref: \"#/schemas/Manifest\" }\n" +
		"config_files:\n" +
		"  - name: app\n" +
		"    path: ~/.config/widget.yaml\n" +
		"    format: yaml\n" +
		"schemas:\n" +
		"  Manifest:\n" +
		"    type: object\n" +
		"    required: [kind]\n" +
		"    properties:\n" +
		"      kind: { type: string }\n"
	writeTestFile(t, filepath.Join(tmp, ".rotini.spec.yaml"), spec)

	t.Chdir(tmp)
	if err := Generate(".rotini.spec.yaml", "", false, "", nil); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	rotiniGo := filepath.Join(tmp, "internal", "cmd", "widget", "zz_rotini.gen.go")
	mustContain(t, rotiniGo,
		"type WidgetFlags struct {",  // argv flag stays
		"Color string",               //
		"type WidgetEnv struct {",    // env channel — its own struct
		"Region string",              //
		"type WidgetConfig struct {", // config channel — its own struct
		"Endpoint string",            //
		"type WidgetStdin Manifest",  // stdin payload type (from $ref, via the type machinery)
		"type Manifest struct {",     // the named schema
		"Env       WidgetEnv",        // CommandInputs gains the channels
		"Config    WidgetConfig",     //
		"Stdin     *WidgetStdin",     // the stdin payload field
		"`stdin:\"yaml\"`",           // its decode format rides on a tag
		// a config-fallback flag carries a recon key (the argv-only `quiet` flag does not).
		"recon:\"create.color\"",
		// recon tags drive the binder; env key = name, config key = schema.key, + secret/required.
		// An env input's explicit `variable` rides on an `env:"…"` tag.
		"recon:\"region\" env:\"WIDGET_REGION\"",
		// file: pins the input to that configuration_files entry (cfgfile tag).
		"`rotini:\"endpoint\" recon:\"api.endpoint\" cfgfile:\"app\"`",
		"`rotini:\"token\" recon:\"api.token,required,secret\"`",
		// the BindMeta descriptor carries the config-file sources.
		"var BindMeta = rotini.BindMeta{",
		"{Name: \"app\", Scope: \"widget\", Path: \"~/.config/widget.yaml\", Format: \"yaml\"}",
	)
	// env is NOT folded into the Flags struct (the channel break).
	flags := readFileString(t, rotiniGo)
	if _, block, ok := strings.Cut(flags, "type WidgetFlags struct {"); ok {
		if before, _, found := strings.Cut(block, "}"); found && strings.Contains(before, "Region") {
			t.Errorf("env field Region must not be folded into WidgetFlags:\n%s", before)
		}
	}
}

// helpSpecYAML is a spec exercising generated help: root summary/description plus
// a sub-command with a summary, a required argument, and a bool flag. Help fields
// live directly on the command (flattened).
const helpSpecYAML = "$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-spec.json\n" +
	"name: mycli\n" +
	"summary: my cli\n" +
	"description: A demo CLI.\n" +
	"footer: run 'mycli help <command>' for details\n" +
	"commands:\n" +
	"  - name: build\n" +
	"    aliases: [b]\n" +
	"    summary: build the project\n" +
	"    arguments:\n" +
	"      - name: target\n" +
	"        summary: thing to build\n" +
	"        schema:\n" +
	"          type: string\n" +
	"          required: true\n" +
	"    flags:\n" +
	"      - name: verbose\n" +
	"        summary: chattier output\n" +
	"        identifiers: [-v, --verbose]\n" +
	"        schema:\n" +
	"          type: bool\n"

const helpEnabledConf = confSchemaHeader + "generate:\n  features:\n    help:\n      enabled: true\n      embed: true\n      template: true\n"

const placeholderSpecYAML = "$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-spec.json\n" +
	"name: mycli\n" +
	"summary: my cli\n" +
	"commands:\n" +
	"  - name: build\n" +
	"    summary: build the project\n" +
	"    arguments:\n" +
	"      - name: target\n" +
	"        summary: thing to build\n" +
	"        schema:\n" +
	"          type: string\n" +
	"          required: true\n" +
	"          placeholder: TARGET\n" +
	"    flags:\n" +
	"      - name: out\n" +
	"        summary: write result here\n" +
	"        identifiers: [-o, --out]\n" +
	"        schema:\n" +
	"          type: string\n" +
	"          placeholder: <PATH>\n" +
	"      - name: verbose\n" +
	"        summary: chattier output\n" +
	"        identifiers: [--verbose]\n" +
	"        schema:\n" +
	"          type: bool\n"

const envPrefixSpecYAML = "$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-spec.json\n" +
	"env_prefix: ACME\n" +
	"name: mycli\n" +
	"summary: my cli\n" +
	"env:\n" +
	"  - name: home\n" +
	"    summary: home override\n" +
	"    schema: { type: string }\n" +
	"  - name: region\n" +
	"    summary: region override\n" +
	"    schema: { type: string, variable: PLAIN_REGION }\n" +
	"  - name: http\n" +
	"    summary: http family\n" +
	"    schema: { type: map, nesting: \"__\" }\n"

// env_prefix flows into every codegen artifact that names a DERIVED env var:
// the BindMeta descriptor (the runtime's signal), the envnest tag's base, and
// the help page's Environment rows — while an explicit variable: stays exact.
func TestGenerateEnvPrefix(t *testing.T) {
	tmp := t.TempDir()
	writeTestFile(t, filepath.Join(tmp, "go.mod"), minimalGoMod)
	writeTestFile(t, filepath.Join(tmp, ".rotini.spec.yaml"), envPrefixSpecYAML)
	writeTestFile(t, filepath.Join(tmp, ".rotini.conf.yaml"), helpEnabledConf)

	t.Chdir(tmp)
	if err := Generate(".rotini.spec.yaml", ".rotini.conf.yaml", false, "", nil); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	mustContain(t, filepath.Join(tmp, "internal", "cmd", "mycli", "zz_rotini.gen.go"),
		`EnvPrefix: "ACME"`,      // BindMeta carries the prefix to the binder
		`envnest:"ACME_HTTP,__"`, // derived family base is prefixed
		`env:"PLAIN_REGION"`,     // explicit variable: exempt
	)
	mustContain(t, filepath.Join(tmp, "internal", "cmd", "mycli", "renders", "help_mycli.txt"),
		"ACME_HOME",    // derived row shows the real, prefixed name
		"PLAIN_REGION", // explicit row stays exact
	)
}

// TestGenerateEmbedAndTemplateModes pins the orthogonal embed × template knobs.
// embed: var is //go:embed-backed + a file on disk (true) vs an inline string
// literal + no file (false). template: the editable *.tmpl is seeded to dir
// (true) vs not, rendering from rotini's built-in default (false). Four features
// exercise four quadrants at once.
func TestGenerateEmbedAndTemplateModes(t *testing.T) {
	spec := "$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-spec.json\n" +
		"name: mycli\nsummary: a tool\ndescription: a demo\n"
	// help: inline + no template | man: embed + no template
	// markdown: embed + template | completion: inline
	conf := confSchemaHeader + "generate:\n  features:\n" +
		"    help:\n      enabled: true\n      embed: false\n      template: false\n" +
		"    man:\n      enabled: true\n      embed: true\n      template: false\n" +
		"    markdown:\n      enabled: true\n      embed: true\n      template: true\n" +
		"    completion:\n      enabled: true\n      embed: false\n"

	tmp := t.TempDir()
	writeTestFile(t, filepath.Join(tmp, "go.mod"), minimalGoMod)
	writeTestFile(t, filepath.Join(tmp, ".rotini.spec.yaml"), spec)
	writeTestFile(t, filepath.Join(tmp, ".rotini.conf.yaml"), conf)
	t.Chdir(tmp)
	if err := Generate(".rotini.spec.yaml", ".rotini.conf.yaml", false, "", nil); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	gen := readFileString(t, filepath.Join(tmp, "internal", "cmd", "mycli", "zz_rotini.gen.go"))
	// embed:false → inline string vars (help, completion).
	for _, want := range []string{"var HelpMycli = ", "var CompletionBash = "} {
		if !strings.Contains(gen, want) {
			t.Errorf("gen file missing inline var %q", want)
		}
	}
	// embed:true → //go:embed-backed vars (man, markdown) + the embed import.
	for _, want := range []string{"//go:embed renders/man_mycli.txt", "var ManMycli string", "//go:embed renders/markdown_mycli.md", `_ "embed"`} {
		if !strings.Contains(gen, want) {
			t.Errorf("gen file missing embed form %q", want)
		}
	}
	// Inline features must NOT carry an embed directive for their pages.
	if strings.Contains(gen, "//go:embed renders/help_mycli.txt") {
		t.Error("help is embed:false but the gen file has its //go:embed directive")
	}

	base := filepath.Join(tmp, "internal", "cmd", "mycli")
	rendered := func(name string) bool { // output files live in renders/
		_, err := os.Stat(filepath.Join(base, "renders", name))
		return err == nil
	}
	templated := func(name string) bool { // editable templates live in templates/
		_, err := os.Stat(filepath.Join(base, "templates", name))
		return err == nil
	}
	// Output files: present only for embed:true features (man, markdown).
	if rendered("help_mycli.txt") || rendered("completion_bash.txt") {
		t.Error("inline feature wrote an output file (want none)")
	}
	if !rendered("man_mycli.txt") || !rendered("markdown_mycli.md") {
		t.Error("embed feature missing its output file")
	}
	// Templates: seeded only for template:true (markdown); not for template:false.
	if !templated("markdown.md.tmpl") {
		t.Error("template:true markdown did not seed its template")
	}
	if templated("help.txt.tmpl") || templated("man.txt.tmpl") {
		t.Error("template:false feature seeded a template (want none)")
	}
}

// TestGenerateNoEmbedImportWhenAllInline confirms `import _ "embed"` is dropped
// when no enabled feature uses //go:embed.
func TestGenerateNoEmbedImportWhenAllInline(t *testing.T) {
	spec := "$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-spec.json\n" +
		"name: mycli\nsummary: a tool\n"
	conf := confSchemaHeader + "generate:\n  features:\n    help:\n      enabled: true\n      embed: false\n"
	tmp := t.TempDir()
	writeTestFile(t, filepath.Join(tmp, "go.mod"), minimalGoMod)
	writeTestFile(t, filepath.Join(tmp, ".rotini.spec.yaml"), spec)
	writeTestFile(t, filepath.Join(tmp, ".rotini.conf.yaml"), conf)
	t.Chdir(tmp)
	if err := Generate(".rotini.spec.yaml", ".rotini.conf.yaml", false, "", nil); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	gen := readFileString(t, filepath.Join(tmp, "internal", "cmd", "mycli", "zz_rotini.gen.go"))
	if strings.Contains(gen, `_ "embed"`) {
		t.Error("all features inline, but the gen file still imports embed")
	}
	if !strings.Contains(gen, "var HelpMycli = ") {
		t.Error("inline help var missing")
	}
}

// The markdown feature is the fourth doc feature: same render-or-verbatim
// contract as help/man, .md files, Markdown<Prefix> vars + Markdown resolver,
// editable markdown.md.tmpl seeded into the feature dir.
func TestGenerateMarkdownEnabled(t *testing.T) {
	spec := "$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-spec.json\n" +
		"name: mycli\n" +
		"summary: my cli\n" +
		"description: A demo CLI.\n" +
		"commands:\n" +
		"  - name: build\n" +
		"    summary: build the project\n" +
		"    flags:\n" +
		"      - name: verbose\n" +
		"        summary: chattier output\n" +
		"        identifiers: [-v, --verbose]\n" +
		"        schema: { type: bool }\n" +
		"  - name: verbatim\n" +
		"    summary: hand-written page\n" +
		"    markdown: |\n" +
		"      # custom page\n" +
		"      byte-for-byte.\n"
	conf := confSchemaHeader + "generate:\n  features:\n    markdown:\n      enabled: true\n      embed: true\n      template: true\n"
	tmp := t.TempDir()
	writeTestFile(t, filepath.Join(tmp, "go.mod"), minimalGoMod)
	writeTestFile(t, filepath.Join(tmp, ".rotini.spec.yaml"), spec)
	writeTestFile(t, filepath.Join(tmp, ".rotini.conf.yaml"), conf)

	t.Chdir(tmp)
	if err := Generate(".rotini.spec.yaml", ".rotini.conf.yaml", false, "", nil); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	embedDir := filepath.Join(tmp, "internal", "cmd", "mycli", "renders")
	mustContain(t, filepath.Join(tmp, "internal", "cmd", "mycli", "zz_rotini.gen.go"),
		"//go:embed renders/markdown_mycli.md",
		"var MarkdownMycli string",
		"var MarkdownMycliBuild string",
		"func Markdown(path ...string) (string, error)",
	)
	// Rendered page: markdown shapes from the doc-data.
	mustContain(t, filepath.Join(embedDir, "markdown_mycli_build.md"),
		"# mycli build",
		"## Usage",
		"`-v, --verbose`",
		"chattier output",
	)
	// The verbatim escape writes byte-for-byte.
	mustContain(t, filepath.Join(embedDir, "markdown_mycli_verbatim.md"),
		"# custom page",
	)
	// The editable default template was seeded.
	tmplDir := filepath.Join(filepath.Dir(embedDir), "templates")
	if _, err := os.Stat(filepath.Join(tmplDir, "markdown.md.tmpl")); err != nil {
		t.Errorf("default markdown template not seeded: %v", err)
	}
}

// A passthrough command's CommandDef literal carries the flag — the parser's
// signal that every token after the command is a raw positional.
func TestGeneratePassthrough(t *testing.T) {
	spec := "$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-spec.json\n" +
		"name: mycli\n" +
		"summary: my cli\n" +
		"commands:\n" +
		"  - name: exec\n" +
		"    summary: run a wrapped command\n" +
		"    passthrough: true\n" +
		"    arguments:\n" +
		"      - name: cmdline\n" +
		"        summary: the wrapped command line\n" +
		"        schema: { type: \"[]string\" }\n"
	tmp := t.TempDir()
	writeTestFile(t, filepath.Join(tmp, "go.mod"), minimalGoMod)
	writeTestFile(t, filepath.Join(tmp, ".rotini.spec.yaml"), spec)

	t.Chdir(tmp)
	if err := Generate(".rotini.spec.yaml", "", false, "", nil); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	mustContain(t, filepath.Join(tmp, "internal", "cmd", "mycli", "zz_rotini.gen.go"),
		"Passthrough: true,",
	)
}

const countSpecYAML = "$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-spec.json\n" +
	"name: mycli\n" +
	"summary: my cli\n" +
	"commands:\n" +
	"  - name: build\n" +
	"    summary: build the project\n" +
	"    flags:\n" +
	"      - name: verbose\n" +
	"        summary: chattier output, per occurrence\n" +
	"        identifiers: [-v, --verbose]\n" +
	"        schema:\n" +
	"          type: count\n"

// A count flag generates an int field, a Type:"count" FlagDef (the parser's
// signal that occurrences increment and no value is consumed), and a help row
// with no value token — exactly a bool's presentation.
func TestGenerateCountFlag(t *testing.T) {
	tmp := t.TempDir()
	writeTestFile(t, filepath.Join(tmp, "go.mod"), minimalGoMod)
	writeTestFile(t, filepath.Join(tmp, ".rotini.spec.yaml"), countSpecYAML)
	writeTestFile(t, filepath.Join(tmp, ".rotini.conf.yaml"), helpEnabledConf)

	t.Chdir(tmp)
	if err := Generate(".rotini.spec.yaml", ".rotini.conf.yaml", false, "", nil); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	fw := readFileString(t, filepath.Join(tmp, "internal", "cmd", "mycli", "zz_rotini.gen.go"))
	if !strings.Contains(fw, `Type: "count"`) {
		t.Errorf("FlagDef literal missing Type:\"count\":\n%s", fw)
	}
	var field string
	for _, line := range strings.Split(fw, "\n") {
		if strings.Contains(line, "rotini:\"verbose\"") {
			field = line
			break
		}
	}
	if !strings.Contains(field, "Verbose") || !strings.Contains(field, "int") {
		t.Errorf("generated field = %q, want an int tally", field)
	}
	build := filepath.Join(tmp, "internal", "cmd", "mycli", "renders", "help_mycli_build.txt")
	mustContain(t, build, "-v,--verbose")
	if got := readFileString(t, build); strings.Contains(got, "--verbose count") || strings.Contains(got, "--verbose int") {
		t.Errorf("count flag shows a value token in help:\n%s", got)
	}
}

// A placeholder replaces the value's display token only: the flag row shows it
// instead of the Go type, the usage line shows it inside the argument's
// existing <>/[] decoration, and the generated field/parse behavior is
// untouched (the field stays a plain string).
func TestGeneratePlaceholder(t *testing.T) {
	tmp := t.TempDir()
	writeTestFile(t, filepath.Join(tmp, "go.mod"), minimalGoMod)
	writeTestFile(t, filepath.Join(tmp, ".rotini.spec.yaml"), placeholderSpecYAML)
	writeTestFile(t, filepath.Join(tmp, ".rotini.conf.yaml"), helpEnabledConf)

	t.Chdir(tmp)
	if err := Generate(".rotini.spec.yaml", ".rotini.conf.yaml", false, "", nil); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	build := filepath.Join(tmp, "internal", "cmd", "mycli", "renders", "help_mycli_build.txt")
	mustContain(t, build,
		"mycli build <TARGET> [flags]", // placeholder under the required decoration
		"-o,--out <PATH>",              // placeholder instead of "string"
	)
	if got := readFileString(t, build); strings.Contains(got, "--out string") {
		t.Errorf("flag row still shows the type token despite a placeholder:\n%s", got)
	}
	// Presentation only: the generated input field is untouched (a plain
	// string; gofmt may pad the alignment, so match the pieces per line).
	fw := readFileString(t, filepath.Join(tmp, "internal", "cmd", "mycli", "zz_rotini.gen.go"))
	var outField string
	for _, line := range strings.Split(fw, "\n") {
		if strings.Contains(line, "rotini:\"out\"") {
			outField = line
			break
		}
	}
	if !strings.Contains(outField, "Out") || !strings.Contains(outField, "string") {
		t.Errorf("generated out field = %q, want a plain string field", outField)
	}
}

// TestGenerateHelpEnabled verifies that, with generate.help enabled, the
// framework file gains the embedded Help<Prefix> vars + an alias-aware Help
// resolver, and that each command's help .txt is rendered from the spec (the
// default generate mode), with a second pass producing byte-identical output.
func TestGenerateHelpEnabled(t *testing.T) {
	tmp := t.TempDir()
	writeTestFile(t, filepath.Join(tmp, "go.mod"), minimalGoMod)
	writeTestFile(t, filepath.Join(tmp, ".rotini.spec.yaml"), helpSpecYAML)
	writeTestFile(t, filepath.Join(tmp, ".rotini.conf.yaml"), helpEnabledConf)

	t.Chdir(tmp)
	if err := Generate(".rotini.spec.yaml", ".rotini.conf.yaml", false, "", nil); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	mustContain(t, filepath.Join(tmp, "internal", "cmd", "mycli", "zz_rotini.gen.go"),
		`_ "embed"`,
		"//go:embed renders/help_mycli.txt",
		"var HelpMycli string",
		"var HelpMycliBuild string",
		"func Help(path ...string) (string, error)",
		`case "":`,
		`case "build", "b":`,
	)

	// The editable default template was seeded.
	if _, err := os.Stat(filepath.Join(tmp, "internal", "cmd", "mycli", "templates", "help.txt.tmpl")); err != nil {
		t.Errorf("default help template not seeded: %v", err)
	}

	// The root page renders the description, a derived usage line, the commands
	// list (with the alias and summary), and the footer.
	root := filepath.Join(tmp, "internal", "cmd", "mycli", "renders", "help_mycli.txt")
	mustContain(t, root,
		"A demo CLI.",
		"Usage:",
		"mycli <command>",
		"Commands:",
		"build;b",
		"build the project",
		"run 'mycli help <command>' for details",
	)
	// Generated pages end exactly at their last line — no trailing newline.
	if got := readFileString(t, root); strings.HasSuffix(got, "\n") {
		t.Errorf("generated help should not end with a trailing newline; got %q", got)
	}
	// The leaf page renders derived usage, the decorated required arg, and the flag.
	build := filepath.Join(tmp, "internal", "cmd", "mycli", "renders", "help_mycli_build.txt")
	mustContain(t, build,
		"mycli build <target> [flags]",
		"Arguments:",
		"<target>",
		"thing to build",
		"Flags:",
		"-v,--verbose",
		"chattier output",
	)

	// Capture rendered output + framework file, then prove a second pass is a
	// byte-identical no-op (determinism + overwrite-guard "identical => skip").
	rootBefore := readFileString(t, root)
	buildBefore := readFileString(t, build)
	fwBefore := readAndFormat(t, filepath.Join(tmp, "internal", "cmd", "mycli", "zz_rotini.gen.go"))

	if err := Generate(".rotini.spec.yaml", ".rotini.conf.yaml", false, "", nil); err != nil {
		t.Fatalf("Generate (second pass): %v", err)
	}
	mustFileEqual(t, root, rootBefore)
	mustFileEqual(t, build, buildBefore)

	after := readAndFormat(t, filepath.Join(tmp, "internal", "cmd", "mycli", "zz_rotini.gen.go"))
	if !bytes.Equal(fwBefore, after) {
		t.Errorf("help generation is not idempotent:\n--- before ---\n%s\n--- after ---\n%s", fwBefore, after)
	}
}

// TestGenerateHelpRegenerates verifies that rendered help .txt files are
// rotini-managed: each pass (re)writes the rendered content, so a hand edit to a
// rendered file is replaced on the next generation (to own a command's words,
// set its verbatim `help` string in the spec instead).
func TestGenerateHelpRegenerates(t *testing.T) {
	tmp := t.TempDir()
	writeTestFile(t, filepath.Join(tmp, "go.mod"), minimalGoMod)
	writeTestFile(t, filepath.Join(tmp, ".rotini.spec.yaml"), helpSpecYAML)
	writeTestFile(t, filepath.Join(tmp, ".rotini.conf.yaml"), helpEnabledConf)

	t.Chdir(tmp)
	if err := Generate(".rotini.spec.yaml", ".rotini.conf.yaml", false, "", nil); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	// A hand edit to a generate-mode file is overwritten on the next pass.
	build := filepath.Join(tmp, "internal", "cmd", "mycli", "renders", "help_mycli_build.txt")
	writeTestFile(t, build, "hand-written help for build\n")
	if err := Generate(".rotini.spec.yaml", ".rotini.conf.yaml", false, "", nil); err != nil {
		t.Fatalf("Generate (regenerate): %v", err)
	}
	mustContain(t, build, "mycli build <target> [flags]")
	mustNotContain(t, build, "hand-written help for build")
}

// manConf enables man (but not help).
const manConf = confSchemaHeader + "generate:\n  features:\n    man:\n      enabled: true\n      embed: true\n      template: true\n"

// TestGenerateManEnabled verifies the help pipeline generalizes: with man enabled,
// the framework gains per-feature embed vars + alias-aware resolvers, the feature
// renders into its own dir with its own extension and seeded template, and a
// disabled feature (help) produces nothing.
func TestGenerateManEnabled(t *testing.T) {
	tmp := t.TempDir()
	writeTestFile(t, filepath.Join(tmp, "go.mod"), minimalGoMod)
	writeTestFile(t, filepath.Join(tmp, ".rotini.spec.yaml"), helpSpecYAML)
	writeTestFile(t, filepath.Join(tmp, ".rotini.conf.yaml"), manConf)

	t.Chdir(tmp)
	if err := Generate(".rotini.spec.yaml", ".rotini.conf.yaml", false, "", nil); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	genFile := filepath.Join(tmp, "internal", "cmd", "mycli", "zz_rotini.gen.go")
	mustContain(t, genFile,
		`_ "embed"`,
		"//go:embed renders/man_mycli.txt", "var ManMycli string", "var ManMycliBuild string",
		"func Man(path ...string) (string, error)",
		`case "build", "b":`,
	)
	// Help was not enabled — no Help resolver, no help dir.
	mustNotContain(t, genFile, "func Help(")
	if _, err := os.Stat(filepath.Join(tmp, "internal", "cmd", "mycli", "renders", "help_mycli.txt")); !os.IsNotExist(err) {
		t.Errorf("help page should not exist when help is off (err=%v)", err)
	}

	for _, p := range []string{
		"internal/cmd/mycli/templates/man.txt.tmpl", // editable template → templates/
		"internal/cmd/mycli/renders/man_mycli.txt",  // rendered output → renders/
		"internal/cmd/mycli/renders/man_mycli_build.txt",
	} {
		if _, err := os.Stat(filepath.Join(tmp, filepath.FromSlash(p))); err != nil {
			t.Errorf("expected generated %s: %v", p, err)
		}
	}
}

// TestGenerateManVerbatim verifies the per-command verbatim escape: a command's
// `man` string is written byte-for-byte (no template seeded when nothing renders),
// exactly like help's verbatim `help` string.
func TestGenerateManVerbatim(t *testing.T) {
	tmp := t.TempDir()
	writeTestFile(t, filepath.Join(tmp, "go.mod"), minimalGoMod)
	writeTestFile(t, filepath.Join(tmp, ".rotini.conf.yaml"), manConf)
	writeTestFile(t, filepath.Join(tmp, ".rotini.spec.yaml"),
		"$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-spec.json\n"+
			"name: mycli\n"+
			"man: |-\n"+
			"  MYCLI(1)\n"+
			"  exact man page\n")

	t.Chdir(tmp)
	if err := Generate(".rotini.spec.yaml", ".rotini.conf.yaml", false, "", nil); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	mustFileEqual(t, filepath.Join(tmp, "internal", "cmd", "mycli", "renders", "man_mycli.txt"), "MYCLI(1)\nexact man page")

	// Nothing renders (root supplies verbatim, no sub-commands) → no template seeded.
	if _, err := os.Stat(filepath.Join(tmp, "internal", "cmd", "mycli", "templates", "man.txt.tmpl")); !os.IsNotExist(err) {
		t.Errorf("man.txt.tmpl should not be seeded when no command renders (err=%v)", err)
	}
}

// TestGenerateManExitStatusAndSeeAlso verifies the man EXIT STATUS / SEE ALSO
// sections: a command's declared exit_status codes render as an aligned section and
// its see_also entries as a comma-joined cross-reference.
func TestGenerateManExitStatusAndSeeAlso(t *testing.T) {
	tmp := t.TempDir()
	writeTestFile(t, filepath.Join(tmp, "go.mod"), minimalGoMod)
	writeTestFile(t, filepath.Join(tmp, ".rotini.conf.yaml"), manConf)
	writeTestFile(t, filepath.Join(tmp, ".rotini.spec.yaml"),
		"$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-spec.json\n"+
			"name: mycli\n"+
			"summary: a tool\n"+
			"exit_status:\n"+
			"  - { code: 0, summary: success }\n"+
			"  - { code: 2, summary: a usage error }\n"+
			"see_also:\n"+
			"  - mycli-build(1)\n"+
			"  - https://example.com/docs\n")

	t.Chdir(tmp)
	if err := Generate(".rotini.spec.yaml", ".rotini.conf.yaml", false, "", nil); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	manPath := filepath.Join(tmp, "internal", "cmd", "mycli", "renders", "man_mycli.txt")
	mustContain(t, manPath,
		"EXIT STATUS", "0", "success", "2", "a usage error",
		"SEE ALSO", "mycli-build(1), https://example.com/docs",
	)
}

// TestGenerateStripsStylesPerSurface pins E6-S1: spec-authored ANSI is KEPT in
// the help page (the terminal surface) but STRIPPED from man and markdown —
// both in rendered doc-fields (a styled root description) and in verbatim
// feature pages (a styled `man`/`markdown` string). Help is the only surface
// that renders escapes; the others are roff/plain/markdown text.
func TestGenerateStripsStylesPerSurface(t *testing.T) {
	const esc = "\x1b" // assertions look for the real ESC byte in generated files
	// The spec carries ANSI two ways: a rendered field (the root description,
	// YAML \e escape → ESC) and verbatim feature pages on a sub-command.
	spec := "$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-spec.json\n" +
		"name: mycli\n" +
		"summary: a tool\n" +
		"description: \"\\e[1mBold desc\\e[0m\"\n" +
		"commands:\n" +
		"  - name: build\n" +
		"    summary: build it\n" +
		"    man: \"\\e[1mMAN PAGE\\e[0m\"\n" +
		"    markdown: \"# \\e[1mMD\\e[0m\"\n"
	conf := confSchemaHeader + "generate:\n  features:\n" +
		"    help:\n      enabled: true\n      embed: true\n      template: true\n" +
		"    man:\n      enabled: true\n      embed: true\n      template: true\n" +
		"    markdown:\n      enabled: true\n      embed: true\n      template: true\n"

	tmp := t.TempDir()
	writeTestFile(t, filepath.Join(tmp, "go.mod"), minimalGoMod)
	writeTestFile(t, filepath.Join(tmp, ".rotini.spec.yaml"), spec)
	writeTestFile(t, filepath.Join(tmp, ".rotini.conf.yaml"), conf)

	t.Chdir(tmp)
	if err := Generate(".rotini.spec.yaml", ".rotini.conf.yaml", false, "", nil); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	embed := func(name string) string {
		return readFileString(t, filepath.Join(tmp, "internal", "cmd", "mycli", "renders", name))
	}

	// Help KEEPS the rendered styling.
	if help := embed("help_mycli.txt"); !strings.Contains(help, esc) {
		t.Errorf("help page lost its ANSI styling:\n%q", help)
	}
	// Man + markdown STRIP it — rendered field (root) AND verbatim pages (build).
	for _, f := range []string{"man_mycli.txt", "markdown_mycli.md", "man_mycli_build.txt", "markdown_mycli_build.md"} {
		if got := embed(f); strings.Contains(got, esc) {
			t.Errorf("%s carries ANSI, want stripped:\n%q", f, got)
		}
	}
	// The verbatim text itself survives, just de-styled.
	if got := embed("man_mycli_build.txt"); !strings.Contains(got, "MAN PAGE") {
		t.Errorf("man verbatim text lost its content: %q", got)
	}
	if got := embed("markdown_mycli_build.md"); !strings.Contains(got, "# MD") {
		t.Errorf("markdown verbatim text lost its content: %q", got)
	}
}

// TestGenerateHelpVerbatim verifies that a populated `help` string is written
// EXACTLY as supplied — byte-for-byte, with no trailing-newline normalization (a
// YAML `|-` strip block yields no trailing newline, and rotini keeps it that way) —
// and that when every command supplies verbatim help no template is seeded.
func TestGenerateHelpVerbatim(t *testing.T) {
	tmp := t.TempDir()
	writeTestFile(t, filepath.Join(tmp, "go.mod"), minimalGoMod)
	writeTestFile(t, filepath.Join(tmp, ".rotini.conf.yaml"), helpEnabledConf)
	writeTestFile(t, filepath.Join(tmp, ".rotini.spec.yaml"),
		"$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-spec.json\n"+
			"name: mycli\n"+
			"help: |-\n"+ // strip: no trailing newline
			"  my exact help page\n"+
			"  line two\n")
	t.Chdir(tmp)
	if err := Generate(".rotini.spec.yaml", ".rotini.conf.yaml", false, "", nil); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	// Exactly the supplied bytes — no trailing newline added.
	mustFileEqual(t, filepath.Join(tmp, "internal", "cmd", "mycli", "renders", "help_mycli.txt"), "my exact help page\nline two")

	// Nothing renders (root supplies verbatim help, no sub-commands) → no template.
	if _, err := os.Stat(filepath.Join(tmp, "internal", "cmd", "mycli", "templates", "help.txt.tmpl")); !os.IsNotExist(err) {
		t.Errorf("help.txt.tmpl should not be seeded when no command renders (err=%v)", err)
	}

	// The verbatim string wins; a hand edit is overwritten back to the spec value.
	root := filepath.Join(tmp, "internal", "cmd", "mycli", "renders", "help_mycli.txt")
	writeTestFile(t, root, "tampered\n")
	if err := Generate(".rotini.spec.yaml", ".rotini.conf.yaml", false, "", nil); err != nil {
		t.Fatalf("Generate (regenerate): %v", err)
	}
	mustFileEqual(t, root, "my exact help page\nline two")
}

// repoRoot returns the rotini module root (the parent of the internal package
// directory the test runs in).
func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	return filepath.Dir(wd)
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// assertGoEqual compares two Go files for equality after gofmt normalization,
// so non-canonical whitespace in the hand-written golden files does not cause
// spurious failures.
func assertGoEqual(t *testing.T, generatedPath, goldenPath string) {
	t.Helper()
	gen := readAndFormat(t, generatedPath)
	golden := readAndFormat(t, goldenPath)
	if !bytes.Equal(gen, golden) {
		t.Errorf("generated %s does not match golden %s\n--- generated ---\n%s\n--- golden ---\n%s",
			generatedPath, goldenPath, gen, golden)
	}
}

func readAndFormat(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	formatted, err := format.Source(data)
	if err != nil {
		t.Fatalf("gofmt %s: %v", path, err)
	}
	return formatted
}

func mustContain(t *testing.T, path string, substrs ...string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	for _, s := range substrs {
		if !bytes.Contains(data, []byte(s)) {
			t.Errorf("%s missing %q\n%s", path, s, data)
		}
	}
}

func mustFileEqual(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(data) != want {
		t.Errorf("%s = %q, want %q", path, data, want)
	}
}

func readFileString(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

// specWith builds a minimal mycli spec declaring the given top-level commands.
func specWith(commands ...string) string {
	var b strings.Builder
	b.WriteString("$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-spec.json\nname: mycli\ncommands:\n")
	for _, c := range commands {
		b.WriteString("  - name: " + c + "\n")
	}
	return b.String()
}

func fileContains(path, substr string) bool {
	b, err := os.ReadFile(path)
	return err == nil && bytes.Contains(b, []byte(substr))
}

// waitForCond polls cond until it returns true or the timeout elapses.
func waitForCond(timeout time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(timeout)
	for {
		if cond() {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestGenerateWatchInitialAndStop verifies the initial pass runs and that
// cancelling ctx makes watchLoop return cleanly (the signal-driven exit).
func TestGenerateWatchInitialAndStop(t *testing.T) {
	tmp := t.TempDir()
	writeTestFile(t, filepath.Join(tmp, "go.mod"), minimalGoMod)
	specPath := filepath.Join(tmp, ".rotini.spec.yaml")
	writeTestFile(t, specPath, specWith("alpha"))
	t.Chdir(tmp)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var buf bytes.Buffer
	onGen := func(_ string, err error) {
		if err != nil {
			fmt.Fprintln(&buf, err)
		}
	}
	done := make(chan error, 1)
	go func() {
		done <- watchLoop(ctx, specPath, "", generatePassClosure(specPath), onGen)
	}()

	rtg := filepath.Join(tmp, "internal", "cmd", "mycli", "zz_rotini.gen.go")
	if !waitForCond(3*time.Second, func() bool { return fileContains(rtg, "MycliAlpha") }) {
		t.Fatalf("initial generate did not produce %s with MycliAlpha\noutput:\n%s", rtg, buf.String())
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("watchLoop returned error after cancel: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("watchLoop did not return after ctx cancellation")
	}
}

// TestGenerateWatchRegeneratesOnChange verifies that editing the spec while
// watching triggers a re-generation that reflects the change.
func TestGenerateWatchRegeneratesOnChange(t *testing.T) {
	tmp := t.TempDir()
	writeTestFile(t, filepath.Join(tmp, "go.mod"), minimalGoMod)
	specPath := filepath.Join(tmp, ".rotini.spec.yaml")
	writeTestFile(t, specPath, specWith("alpha"))
	t.Chdir(tmp)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var buf bytes.Buffer
	onGen := func(_ string, err error) {
		if err != nil {
			fmt.Fprintln(&buf, err)
		}
	}
	done := make(chan error, 1)
	go func() {
		done <- watchLoop(ctx, specPath, "", generatePassClosure(specPath), onGen)
	}()

	rtg := filepath.Join(tmp, "internal", "cmd", "mycli", "zz_rotini.gen.go")
	if !waitForCond(3*time.Second, func() bool { return fileContains(rtg, "MycliAlpha") }) {
		t.Fatalf("initial generate missing MycliAlpha\noutput:\n%s", buf.String())
	}
	if fileContains(rtg, "MycliBeta") {
		t.Fatal("MycliBeta present before the spec was changed")
	}

	// Add a beta command; the watcher should pick it up and re-generate.
	writeTestFile(t, specPath, specWith("alpha", "beta"))
	if !waitForCond(5*time.Second, func() bool { return fileContains(rtg, "MycliBeta") }) {
		t.Fatalf("watch did not regenerate after spec change (no MycliBeta)\noutput:\n%s", buf.String())
	}

	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("watchLoop did not return after cancel")
	}
}

// TestRoundDuration checks that the watch summary's elapsed time renders in the
// best-fitting unit, trimmed to ~3 significant figures.
func TestRoundDuration(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want string
	}{
		{312 * time.Nanosecond, "312ns"},
		{2793 * time.Nanosecond, "2.79µs"},
		{45678 * time.Nanosecond, "45.7µs"},
		{2793256 * time.Nanosecond, "2.79ms"},
		{1234567890 * time.Nanosecond, "1.23s"},
	}
	for _, c := range cases {
		if got := roundDuration(c.in).String(); got != c.want {
			t.Errorf("roundDuration(%d ns) = %q, want %q", c.in.Nanoseconds(), got, c.want)
		}
	}
}

const (
	childSpecYAML = `$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-spec.json
name: child
commands:
  - name: greet
    arguments:
      - name: who
        schema: { type: string }
    flags:
      - name: loud
        identifiers: [--loud]
        schema: { type: bool }
`
	childConfYAML = `$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-conf.json
generate:
  packages:
    cmd:
      package: cmd/child/rth
      file: handlers.go
    cmdgen:
      package: cmd/child/rtg
      file: rotini.go
`
	parentSpecYAML = `$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-spec.json
name: parent
commands:
  - $ref: ../child/.rotini.spec.yaml
`
	parentConfYAML = `$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-conf.json
generate:
  packages:
    cmd:
      package: cmd/parent/rth
      file: handlers.go
    cmdgen:
      package: cmd/parent/rtg
      file: rotini.go
`
)

func TestGenerate_staticComposition(t *testing.T) {
	tmp := initTestModule(t) // module example.com/myclis, chdir'd
	writeTestFile(t, filepath.Join(tmp, "cmd/child/.rotini.spec.yaml"), childSpecYAML)
	writeTestFile(t, filepath.Join(tmp, "cmd/child/.rotini.conf.yaml"), childConfYAML)
	writeTestFile(t, filepath.Join(tmp, "cmd/parent/.rotini.spec.yaml"), parentSpecYAML)
	writeTestFile(t, filepath.Join(tmp, "cmd/parent/.rotini.conf.yaml"), parentConfYAML)

	// Children must be generated before parents (the parent imports the child rth).
	if err := Generate("cmd/child/.rotini.spec.yaml", "cmd/child/.rotini.conf.yaml", false, "", nil); err != nil {
		t.Fatalf("generate child: %v", err)
	}
	if err := Generate("cmd/parent/.rotini.spec.yaml", "cmd/parent/.rotini.conf.yaml", false, "", nil); err != nil {
		t.Fatalf("generate parent: %v", err)
	}

	// Parent rtg: composed commands appear in the interface and the Definition,
	// but their input structs are NOT redeclared (they live in the child's rtg).
	rtg := filepath.Join(tmp, "cmd/parent/rtg/rotini.go")
	mustContain(t, rtg,
		"ParentChild() rotini.Handlers",
		"ParentChildGreet() rotini.Handlers",
		`Name: "child"`, `Handler: "ParentChild"`,
		`Name: "greet"`, `Handler: "ParentChildGreet"`,
		`{Name: "who", Type: "string"}`,
	)
	mustNotContain(t, rtg, "ParentChildGreetInputs", "ParentChildGreetFlags")

	// Parent rollup: imports the child rth (aliased) and delegates composed
	// commands to it; own root returns a local stub.
	rollup := filepath.Join(tmp, "cmd/parent/rth/handlers.go")
	mustContain(t, rollup,
		`childcli "example.com/myclis/cmd/child/rth"`,
		"return childcli.Handlers().Child()",
		"return childcli.Handlers().ChildGreet()",
		"return &parentHandlers{}",
	)

	// No stub is created in the parent for a composed command.
	if _, err := os.Stat(filepath.Join(tmp, "cmd/parent/rth/parent_child_greet.go")); !os.IsNotExist(err) {
		t.Errorf("composed command should not get a parent stub: %v", err)
	}
	// The child remains a standalone CLI with its own typed inputs + Handlers().
	mustContain(t, filepath.Join(tmp, "cmd/child/rtg/rotini.go"), "type ChildGreetInputs struct")
	mustContain(t, filepath.Join(tmp, "cmd/child/rth/handlers.go"), "func Handlers() rtg.ProgramHandlers")
}

func TestGenerate_composeNameOverride(t *testing.T) {
	tmp := initTestModule(t)
	writeTestFile(t, filepath.Join(tmp, "cmd/child/.rotini.spec.yaml"), childSpecYAML)
	writeTestFile(t, filepath.Join(tmp, "cmd/child/.rotini.conf.yaml"), childConfYAML)
	// The parent grafts the child under a different name via `name:` on the $ref.
	parentSpec := `$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-spec.json
name: parent
commands:
  - $ref: ../child/.rotini.spec.yaml
    name: kid
`
	writeTestFile(t, filepath.Join(tmp, "cmd/parent/.rotini.spec.yaml"), parentSpec)
	writeTestFile(t, filepath.Join(tmp, "cmd/parent/.rotini.conf.yaml"), parentConfYAML)

	if err := Generate("cmd/child/.rotini.spec.yaml", "cmd/child/.rotini.conf.yaml", false, "", nil); err != nil {
		t.Fatalf("generate child: %v", err)
	}
	if err := Generate("cmd/parent/.rotini.spec.yaml", "cmd/parent/.rotini.conf.yaml", false, "", nil); err != nil {
		t.Fatalf("generate parent: %v", err)
	}

	// The grafted command and method use the override name "kid"…
	rtg := filepath.Join(tmp, "cmd/parent/rtg/rotini.go")
	mustContain(t, rtg,
		"ParentKid() rotini.Handlers",
		"ParentKidGreet() rotini.Handlers",
		`Name: "kid"`, `Handler: "ParentKid"`,
		`Name: "greet"`, `Handler: "ParentKidGreet"`,
	)
	// …but delegation still targets the child's real handler methods (Child/ChildGreet).
	mustContain(t, filepath.Join(tmp, "cmd/parent/rth/handlers.go"),
		"return childcli.Handlers().Child()",
		"return childcli.Handlers().ChildGreet()",
	)
}

func TestGenerate_transitiveRef(t *testing.T) {
	tmp := initTestModule(t)
	conf := func(dir string) string {
		return "$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-conf.json\n" +
			"generate:\n  packages:\n    cmd:\n      package: cmd/" + dir + "/rth\n      file: handlers.go\n" +
			"    cmdgen:\n      package: cmd/" + dir + "/rtg\n      file: rotini.go\n"
	}
	// grandchild (gc) has its own sub-command "ping"; child composes gc; parent
	// composes child — so the parent reaches gc transitively, through child.
	gcSpec := `$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-spec.json
name: gc
commands:
  - name: ping
    arguments:
      - name: host
        schema: { type: string }
`
	childSpec := `$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-spec.json
name: child
commands:
  - $ref: ../gc/.rotini.spec.yaml
`
	parentSpec := `$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-spec.json
name: parent
commands:
  - $ref: ../child/.rotini.spec.yaml
`
	for dir, spec := range map[string]string{"gc": gcSpec, "child": childSpec, "parent": parentSpec} {
		writeTestFile(t, filepath.Join(tmp, "cmd/"+dir+"/.rotini.spec.yaml"), spec)
		writeTestFile(t, filepath.Join(tmp, "cmd/"+dir+"/.rotini.conf.yaml"), conf(dir))
	}

	// Dependency order: grandchild, then child, then parent.
	for _, dir := range []string{"gc", "child", "parent"} {
		if err := Generate("cmd/"+dir+"/.rotini.spec.yaml", "cmd/"+dir+"/.rotini.conf.yaml", false, "", nil); err != nil {
			t.Fatalf("generate %s: %v", dir, err)
		}
	}

	// The child composed gc directly (one level).
	mustContain(t, filepath.Join(tmp, "cmd/child/rth/handlers.go"),
		`gccli "example.com/myclis/cmd/gc/rth"`,
		"return gccli.Handlers().Gc()",
		"return gccli.Handlers().GcPing()",
	)

	// The parent reaches gc transitively: its Definition + interface include the
	// grandchild commands…
	parentRtg := filepath.Join(tmp, "cmd/parent/rtg/rotini.go")
	mustContain(t, parentRtg,
		"ParentChildGc() rotini.Handlers",
		"ParentChildGcPing() rotini.Handlers",
		`Name: "gc"`, `Handler: "ParentChildGc"`,
		`Name: "ping"`, `Handler: "ParentChildGcPing"`,
		`{Name: "host", Type: "string"}`,
	)
	// …and its rollup delegates them to the *direct child* (which forwards to gc),
	// importing only the child's rth — never the grandchild's.
	parentRollup := filepath.Join(tmp, "cmd/parent/rth/handlers.go")
	mustContain(t, parentRollup,
		`childcli "example.com/myclis/cmd/child/rth"`,
		"return childcli.Handlers().Child()",
		"return childcli.Handlers().ChildGc()",
		"return childcli.Handlers().ChildGcPing()",
	)
	mustNotContain(t, parentRollup, "example.com/myclis/cmd/gc/rth", "gccli")
}

// TestGenerate_refNodeSiblingCommands pins W8 Slice 1: commands authored ALONGSIDE
// a $ref (the croot/c3 case) are composed, not silently dropped. A $ref node may
// carry its own `commands:` — an inline sibling (→ own stub) and a sibling that is
// itself a $ref (→ a new composition) — and both must graft onto the composed
// subtree next to the child's own commands.
func TestGenerate_refNodeSiblingCommands(t *testing.T) {
	tmp := initTestModule(t)
	conf := func(dir string) string {
		return "$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-conf.json\n" +
			"generate:\n  packages:\n    cmd:\n      package: cmd/" + dir + "/rth\n      file: handlers.go\n" +
			"    cmdgen:\n      package: cmd/" + dir + "/rtg\n      file: rotini.go\n"
	}
	// child (composed via $ref) has its own command "greet"; sub (composed as a
	// SIBLING $ref authored on child's $ref node) has "ping"; "local" is an inline
	// sibling authored on the same $ref node.
	subSpec := `$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-spec.json
name: sub
commands:
  - name: ping
    arguments:
      - name: host
        schema: { type: string }
`
	parentSpec := `$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-spec.json
name: parent
commands:
  - $ref: ../child/.rotini.spec.yaml
    name: child
    summary: parent's view of child
    commands:
      - name: local
        arguments:
          - name: target
            schema: { type: string }
      - $ref: ../sub/.rotini.spec.yaml
        name: sub
`
	writeTestFile(t, filepath.Join(tmp, "cmd/child/.rotini.spec.yaml"), childSpecYAML)
	writeTestFile(t, filepath.Join(tmp, "cmd/child/.rotini.conf.yaml"), conf("child"))
	writeTestFile(t, filepath.Join(tmp, "cmd/sub/.rotini.spec.yaml"), subSpec)
	writeTestFile(t, filepath.Join(tmp, "cmd/sub/.rotini.conf.yaml"), conf("sub"))
	writeTestFile(t, filepath.Join(tmp, "cmd/parent/.rotini.spec.yaml"), parentSpec)
	writeTestFile(t, filepath.Join(tmp, "cmd/parent/.rotini.conf.yaml"), conf("parent"))

	for _, dir := range []string{"child", "sub", "parent"} {
		if err := Generate("cmd/"+dir+"/.rotini.spec.yaml", "cmd/"+dir+"/.rotini.conf.yaml", false, "", nil); err != nil {
			t.Fatalf("generate %s: %v", dir, err)
		}
	}

	parentRtg := filepath.Join(tmp, "cmd/parent/rtg/rotini.go")
	mustContain(t, parentRtg,
		// the parent's summary OVERLAYS the child's own (D-W8.2)…
		`Summary: "parent's view of child"`,
		// the child's own command survives…
		"ParentChildGreet() rotini.Handlers", `Name: "greet"`,
		// …the inline authored sibling is grafted (own command — its inputs ARE redeclared here)…
		"ParentChildLocal() rotini.Handlers", `Name: "local"`,
		`{Name: "target", Type: "string"}`,
		// …and the $ref authored sibling composes (its ping subcommand reached too).
		"ParentChildSub() rotini.Handlers", `Name: "sub"`,
		"ParentChildSubPing() rotini.Handlers", `Name: "ping"`,
	)
	// the inline sibling is an OWN command → it gets a parent stub file…
	if _, err := os.Stat(filepath.Join(tmp, "cmd/parent/rth/parent_child_local.go")); err != nil {
		t.Errorf("inline authored sibling should get an own stub: %v", err)
	}
	// …while the $ref sibling delegates to its own composed cli (imported).
	parentRollup := filepath.Join(tmp, "cmd/parent/rth/handlers.go")
	mustContain(t, parentRollup,
		`subcli "example.com/myclis/cmd/sub/rth"`,
		"return subcli.Handlers().Sub()",
		"return subcli.Handlers().SubPing()",
	)
}

// TestGenerate_composedSchemaVersionGuard pins the cross-tree $schema guard (W8): a
// composed child whose $schema targets a different rotini version than the generating
// binary is an error, so the whole composed tree shares one version. Matching versions
// compose clean.
func TestGenerate_composedSchemaVersionGuard(t *testing.T) {
	tmp := initTestModule(t)
	// Confs target the running version (1.2.3) so only the composed SPEC's $schema is
	// under test (an unmatched conf $schema would trip the per-spec guard first).
	conf := func(dir string) string {
		return "$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/1.2.3/schema-conf.json\n" +
			"generate:\n  packages:\n    cmd:\n      package: cmd/" + dir + "/rth\n      file: handlers.go\n" +
			"    cmdgen:\n      package: cmd/" + dir + "/rtg\n      file: rotini.go\n"
	}
	childMismatch := `$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/9.9.9/schema-spec.json
name: child
commands:
  - name: greet
    arguments:
      - name: who
        schema: { type: string }
`
	parentSpec := `$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/1.2.3/schema-spec.json
name: parent
commands:
  - $ref: ../child/.rotini.spec.yaml
`
	writeTestFile(t, filepath.Join(tmp, "cmd/child/.rotini.spec.yaml"), childMismatch)
	writeTestFile(t, filepath.Join(tmp, "cmd/child/.rotini.conf.yaml"), conf("child"))
	writeTestFile(t, filepath.Join(tmp, "cmd/parent/.rotini.spec.yaml"), parentSpec)
	writeTestFile(t, filepath.Join(tmp, "cmd/parent/.rotini.conf.yaml"), conf("parent"))

	// Generate the parent at version 1.2.3: its own $schema matches, but composing the
	// child (9.9.9) trips the cross-tree guard.
	err := Generate("cmd/parent/.rotini.spec.yaml", "cmd/parent/.rotini.conf.yaml", false, "1.2.3", nil)
	if err == nil || !strings.Contains(err.Error(), "schema version") {
		t.Fatalf("generate(version-mismatched composed spec) = %v, want a $schema guard error", err)
	}

	// A composed spec with NO rotini $schema is rejected — mandatory (D-W8.7).
	writeTestFile(t, filepath.Join(tmp, "cmd/child/.rotini.spec.yaml"),
		"name: child\ncommands:\n  - name: greet\n    arguments:\n      - name: who\n        schema: { type: string }\n")
	err = Generate("cmd/parent/.rotini.spec.yaml", "cmd/parent/.rotini.conf.yaml", false, "1.2.3", nil)
	if err == nil || !strings.Contains(err.Error(), "must declare a rotini $schema") {
		t.Fatalf("generate(composed spec without $schema) = %v, want a mandatory-$schema error", err)
	}

	// Align the child to the running version → composes clean.
	writeTestFile(t, filepath.Join(tmp, "cmd/child/.rotini.spec.yaml"),
		strings.Replace(childMismatch, "9.9.9", "1.2.3", 1))
	if err := Generate("cmd/child/.rotini.spec.yaml", "cmd/child/.rotini.conf.yaml", false, "1.2.3", nil); err != nil {
		t.Fatalf("generate child (matching) = %v", err)
	}
	if err := Generate("cmd/parent/.rotini.spec.yaml", "cmd/parent/.rotini.conf.yaml", false, "1.2.3", nil); err != nil {
		t.Errorf("generate(matching versions) = %v, want nil", err)
	}
}

// TestGenerate_modRefCompose pins external `mod://` composition (W8/D-W8.4a): a parent
// composes a spec from another Go module, resolved through the module cache (stubbed
// here). The tree composes AND the handler delegation imports from the EXTERNAL module's
// cli package — the cross-module wiring that makes an external `$ref` executable.
func TestGenerate_modRefCompose(t *testing.T) {
	tmp := initTestModule(t) // module example.com/myclis, chdir'd
	conf := func(dir string) string {
		return "$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-conf.json\n" +
			"generate:\n  packages:\n    cmd:\n      package: cmd/" + dir + "/rth\n      file: handlers.go\n" +
			"    cmdgen:\n      package: cmd/" + dir + "/rtg\n      file: rotini.go\n"
	}

	// The "external module" stands in for what `go mod download` would extract into the
	// module cache: a generated rotini cli with its spec + conf.
	ext := t.TempDir()
	writeTestFile(t, filepath.Join(ext, "deploy", ".rotini.spec.yaml"),
		"$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-spec.json\n"+
			"name: deploy\ncommands:\n  - name: up\n    arguments:\n      - name: target\n        schema: { type: string }\n")
	writeTestFile(t, filepath.Join(ext, "deploy", ".rotini.conf.yaml"),
		"$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-conf.json\n"+
			"generate:\n  packages:\n    cmd:\n      package: deploy/rth\n      file: handlers.go\n")
	orig := moduleDirFunc
	moduleDirFunc = func(module, version string) (string, error) {
		if module == "ext.com/clis" && version == "v1.0.0" {
			return ext, nil
		}
		return "", fmt.Errorf("unexpected module %s@%s", module, version)
	}
	defer func() { moduleDirFunc = orig }()

	parentSpec := `$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-spec.json
name: app
commands:
  - $ref: mod://ext.com/clis@v1.0.0/deploy/.rotini.spec.yaml
`
	writeTestFile(t, filepath.Join(tmp, "cmd/app/.rotini.spec.yaml"), parentSpec)
	writeTestFile(t, filepath.Join(tmp, "cmd/app/.rotini.conf.yaml"), conf("app"))

	if err := Generate("cmd/app/.rotini.spec.yaml", "cmd/app/.rotini.conf.yaml", false, "0.0.0", nil); err != nil {
		t.Fatalf("generate (mod:// compose): %v", err)
	}

	// The external "deploy" subtree is composed into app's framework + Definition.
	rtg := filepath.Join(tmp, "cmd/app/rtg/rotini.go")
	mustContain(t, rtg,
		"AppDeploy() rotini.Handlers", `Name: "deploy"`,
		"AppDeployUp() rotini.Handlers", `Name: "up"`,
		`{Name: "target", Type: "string"}`,
	)
	// The rollup delegates to the EXTERNAL module's cli package (not the consumer's).
	mustContain(t, filepath.Join(tmp, "cmd/app/rth/handlers.go"),
		`"ext.com/clis/deploy/rth"`,
		"Handlers().Deploy()",
		"Handlers().DeployUp()",
	)
}

// TestGenerate_gitRefHandlerPassthrough pins the W8↔W9 join: a git:: spec is resolved
// hermetically from the lock+cache, and a `handler:` (W9 passthrough) supplies the
// handler source a fetched spec can't — so it composes and delegates to the package via
// alias.<Convention>() (no `.Handlers()`). Without a `handler:` it's an error pointing at
// the fix; unlocked points at `rotini mod`.
func TestGenerate_gitRefHandlerPassthrough(t *testing.T) {
	tmp := initTestModule(t) // module root = tmp, chdir'd
	locator := "git::https://github.com/acme/clis@v1/deploy/.rotini.spec.yaml"
	childSpec := "$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-spec.json\n" +
		"name: deploy\ncommands:\n  - name: up\n    arguments:\n      - name: target\n        schema: { type: string }\n"
	h := hashBytes([]byte(childSpec))
	conf := "$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-conf.json\n" +
		"generate:\n  packages:\n    cmd:\n      package: cmd/app/rth\n      file: handlers.go\n" +
		"    cmdgen:\n      package: cmd/app/rtg\n      file: rotini.go\n"
	writeTestFile(t, filepath.Join(tmp, "cmd/app/.rotini.conf.yaml"), conf)
	specPath := filepath.Join(tmp, "cmd/app/.rotini.spec.yaml")
	hdr := "$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-spec.json\nname: app\ncommands:\n"

	// Unlocked → the actionable "run rotini mod" guidance.
	writeTestFile(t, specPath, hdr+"  - $ref: \""+locator+"\"\n    handler: { import: deploycli github.com/acme/clis/deploy/rth, convention: Deploy }\n")
	if err := Generate("cmd/app/.rotini.spec.yaml", "cmd/app/.rotini.conf.yaml", false, "0.0.0", nil); err == nil ||
		!strings.Contains(err.Error(), "run `rotini mod`") {
		t.Fatalf("generate(unlocked git:: ref) = %v, want a run-rotini-mod error", err)
	}

	// Pin + cache it (as `rotini mod` will).
	if err := writeLockfile(tmp, map[string]lockEntry{locator: {revision: "abc", hash: h, format: formatYAML, schema: "0.0.0"}}); err != nil {
		t.Fatal(err)
	}
	if err := cacheWrite(tmp, h, []byte(childSpec)); err != nil {
		t.Fatal(err)
	}

	// Locked but NO handler: → a clear "needs a handler:" error (a fetched spec is not a package).
	writeTestFile(t, specPath, hdr+"  - $ref: \""+locator+"\"\n")
	if err := Generate("cmd/app/.rotini.spec.yaml", "cmd/app/.rotini.conf.yaml", false, "0.0.0", nil); err == nil ||
		!strings.Contains(err.Error(), "needs a `handler:`") {
		t.Fatalf("generate(git:: ref, no handler) = %v, want a needs-handler error", err)
	}

	// Locked + handler: → composes the fetched tree and delegates to the package.
	writeTestFile(t, specPath, hdr+"  - $ref: \""+locator+"\"\n    handler: { import: deploycli github.com/acme/clis/deploy/rth, convention: Deploy }\n")
	if err := Generate("cmd/app/.rotini.spec.yaml", "cmd/app/.rotini.conf.yaml", false, "0.0.0", nil); err != nil {
		t.Fatalf("generate(git:: ref + handler) = %v, want nil", err)
	}
	mustContain(t, filepath.Join(tmp, "cmd/app/rtg/rotini.go"),
		"AppDeploy() rotini.Handlers", `Name: "deploy"`,
		"AppDeployUp() rotini.Handlers", `Name: "up"`,
		`{Name: "target", Type: "string"}`,
	)
	// Passthrough delegation: alias.<Convention>() / alias.<Convention><Sub>() — no .Handlers().
	rollup := filepath.Join(tmp, "cmd/app/rth/handlers.go")
	mustContain(t, rollup,
		`deploycli "github.com/acme/clis/deploy/rth"`,
		"return deploycli.Deploy()",
		"return deploycli.DeployUp()",
	)
	mustNotContain(t, rollup, "deploycli.Handlers()")
}

// TestGenerate_inlineHandlerPassthrough pins inline-command passthrough (W9/D-W9.7/.9):
// `handler:` on an inline (non-$ref) command. The command's structure + typed inputs are
// still generated locally (own-types), but its handler delegates to the package via
// alias.<Convention>() — and no stub file is seeded. Per-command, no cascade (D-W9.9): a
// child WITHOUT its own `handler:` still gets a normal local stub.
func TestGenerate_inlineHandlerPassthrough(t *testing.T) {
	tmp := initTestModule(t) // module root = tmp, chdir'd
	conf := "$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-conf.json\n" +
		"generate:\n  packages:\n    cmd:\n      package: cmd/app/rth\n      file: handlers.go\n" +
		"    cmdgen:\n      package: cmd/app/rtg\n      file: rotini.go\n"
	writeTestFile(t, filepath.Join(tmp, "cmd/app/.rotini.conf.yaml"), conf)
	spec := "$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-spec.json\n" +
		"name: app\ncommands:\n" +
		"  - name: deploy\n" +
		"    arguments:\n      - name: region\n        schema: { type: string }\n" +
		"    handler: { import: deploycli github.com/acme/clis/deploy/rth, convention: Deploy }\n" +
		"    commands:\n      - name: status\n" +
		"        arguments:\n          - name: id\n            schema: { type: string }\n"
	writeTestFile(t, filepath.Join(tmp, "cmd/app/.rotini.spec.yaml"), spec)

	if err := Generate("cmd/app/.rotini.spec.yaml", "cmd/app/.rotini.conf.yaml", false, "0.0.0", nil); err != nil {
		t.Fatalf("generate(inline handler passthrough) = %v, want nil", err)
	}

	// Own-types: the inline command's structure + inputs are generated locally despite
	// the delegated handler (both deploy and its non-delegated child status).
	mustContain(t, filepath.Join(tmp, "cmd/app/rtg/rotini.go"),
		"AppDeploy() rotini.Handlers", `Name: "deploy"`, `{Name: "region", Type: "string"}`,
		"AppDeployStatus() rotini.Handlers", `Name: "status"`, `{Name: "id", Type: "string"}`,
	)

	// Rollup: deploy delegates to the package; status (no handler:) keeps a local stub.
	rollup := filepath.Join(tmp, "cmd/app/rth/handlers.go")
	mustContain(t, rollup,
		`deploycli "github.com/acme/clis/deploy/rth"`,
		"return deploycli.Deploy()",
		"return &appDeployStatusHandlers{}",
	)
	mustNotContain(t, rollup, "deploycli.Handlers()", "deploycli.DeployStatus()")

	// No stub for the passthrough command; the local-stub child gets one.
	if _, err := os.Stat(filepath.Join(tmp, "cmd/app/rth/app_deploy.go")); !os.IsNotExist(err) {
		t.Errorf("passthrough command app_deploy.go = %v, want not-exist (package owns the handler)", err)
	}
	if _, err := os.Stat(filepath.Join(tmp, "cmd/app/rth/app_deploy_status.go")); err != nil {
		t.Errorf("local-stub child app_deploy_status.go = %v, want it to exist", err)
	}
}

func TestGenerate_cyclicRefErrors(t *testing.T) {
	tmp := initTestModule(t)
	// A spec that composes itself — the simplest cycle.
	selfRef := `$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-spec.json
name: a
commands:
  - $ref: ../a/.rotini.spec.yaml
`
	conf := "$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-conf.json\n" +
		"generate:\n  packages:\n    cmd:\n      package: cmd/a/rth\n    cmdgen:\n      package: cmd/a/rtg\n"
	writeTestFile(t, filepath.Join(tmp, "cmd/a/.rotini.spec.yaml"), selfRef)
	writeTestFile(t, filepath.Join(tmp, "cmd/a/.rotini.conf.yaml"), conf)

	err := Generate("cmd/a/.rotini.spec.yaml", "cmd/a/.rotini.conf.yaml", false, "", nil)
	if err == nil || !strings.Contains(err.Error(), "cyclic") {
		t.Fatalf("expected cyclic $ref error, got %v", err)
	}
}

func TestGenerate_channelConstraintTags(t *testing.T) {
	tmp := initTestModule(t)
	spec := `$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-spec.json
name: app
env:
  - name: port
    schema: { type: int, minimum: 1, maximum: 65535 }
  - name: region
    schema: { type: string, minLength: 2, pattern: "^[a-z]+$" }
  - name: tags
    schema: { type: array, minItems: 1, maxItems: 3 }
config:
  - name: name
    schema: { type: string, key: app.name, maxLength: 5 }
`
	conf := "$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-conf.json\n" +
		"generate:\n  packages:\n    cmd:\n      package: cmd/app/rth\n    cmdgen:\n      package: cmd/app/rtg\n      file: rotini.go\n"
	writeTestFile(t, filepath.Join(tmp, "cmd/app/.rotini.spec.yaml"), spec)
	writeTestFile(t, filepath.Join(tmp, "cmd/app/.rotini.conf.yaml"), conf)

	if err := Generate("cmd/app/.rotini.spec.yaml", "cmd/app/.rotini.conf.yaml", false, "", nil); err != nil {
		t.Fatalf("generate: %v", err)
	}
	// The env/config struct fields carry the validation tags the binder reads.
	mustContain(t, filepath.Join(tmp, "cmd/app/rtg/rotini.go"),
		`min:"1"`, `max:"65535"`,
		`minlen:"2"`, `pattern:"^[a-z]+$"`,
		`minitems:"1"`, `maxitems:"3"`,
		`maxlen:"5"`,
	)
}

func TestGenerate_stdinSchemaInBindMeta(t *testing.T) {
	tmp := initTestModule(t)
	spec := `$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-spec.json
name: app
stdin:
  format: yaml
  schema:
    type: object
    properties:
      port: { type: integer, minimum: 1, maximum: 65535 }
`
	conf := "$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-conf.json\n" +
		"generate:\n  packages:\n    cmd:\n      package: cmd/app/rth\n    cmdgen:\n      package: cmd/app/rtg\n      file: rotini.go\n"
	writeTestFile(t, filepath.Join(tmp, "cmd/app/.rotini.spec.yaml"), spec)
	writeTestFile(t, filepath.Join(tmp, "cmd/app/.rotini.conf.yaml"), conf)

	if err := Generate("cmd/app/.rotini.spec.yaml", "cmd/app/.rotini.conf.yaml", false, "", nil); err != nil {
		t.Fatalf("generate: %v", err)
	}
	// BindMeta carries the stdin payload schema keyed by the <Prefix>Stdin type name.
	mustContain(t, filepath.Join(tmp, "cmd/app/rtg/rotini.go"),
		"var BindMeta", "StdinSchemas", `"AppStdin"`, "maximum", "65535",
	)
}

func TestGenerate_inputSchemaRef(t *testing.T) {
	tmp := initTestModule(t)
	// A document-level named schema referenced by an input via $ref: the input's Go
	// field takes the generated named type, and that type is emitted in the rtg.
	spec := `$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-spec.json
schemas:
  Endpoint:
    type: object
    properties:
      host: { type: string }
      port: { type: integer }
name: app
config:
  - name: server
    schema:
      $ref: "#/schemas/Endpoint"
      key: server
`
	conf := "$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-conf.json\n" +
		"generate:\n  packages:\n    cmd:\n      package: cmd/app/rth\n    cmdgen:\n      package: cmd/app/rtg\n      file: rotini.go\n"
	writeTestFile(t, filepath.Join(tmp, "cmd/app/.rotini.spec.yaml"), spec)
	writeTestFile(t, filepath.Join(tmp, "cmd/app/.rotini.conf.yaml"), conf)

	if err := Generate("cmd/app/.rotini.spec.yaml", "cmd/app/.rotini.conf.yaml", false, "", nil); err != nil {
		t.Fatalf("generate: %v", err)
	}
	rtg := filepath.Join(tmp, "cmd/app/rtg/rotini.go")
	// The named type is generated, and the config field is typed as it (not string).
	mustContain(t, rtg,
		"type Endpoint struct",
		"Server Endpoint `",
	)
	mustNotContain(t, rtg, "Server string")
}

func TestGenerate_secretFlagDef(t *testing.T) {
	tmp := initTestModule(t)
	spec := `$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-spec.json
name: app
flags:
  - name: token
    identifiers: [--token]
    schema: { type: string, secret: true }
`
	conf := "$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-conf.json\n" +
		"generate:\n  packages:\n    cmd:\n      package: cmd/app/rth\n    cmdgen:\n      package: cmd/app/rtg\n      file: rotini.go\n"
	writeTestFile(t, filepath.Join(tmp, "cmd/app/.rotini.spec.yaml"), spec)
	writeTestFile(t, filepath.Join(tmp, "cmd/app/.rotini.conf.yaml"), conf)

	if err := Generate("cmd/app/.rotini.spec.yaml", "cmd/app/.rotini.conf.yaml", false, "", nil); err != nil {
		t.Fatalf("generate: %v", err)
	}
	// The FlagDef carries Secret so the parser redacts the value in errors.
	mustContain(t, filepath.Join(tmp, "cmd/app/rtg/rotini.go"), `Name: "token"`, "Secret: true")
}

func TestGenerate_flagGroupsInDefinition(t *testing.T) {
	tmp := initTestModule(t)
	spec := `$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-spec.json
name: app
flags:
  - name: json
    identifiers: [--json]
    schema: { type: bool }
  - name: yaml
    identifiers: [--yaml]
    schema: { type: bool }
flag_groups:
  - kind: mutually_exclusive
    flags: [json, yaml]
`
	conf := "$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-conf.json\n" +
		"generate:\n  packages:\n    cmd:\n      package: cmd/app/rth\n    cmdgen:\n      package: cmd/app/rtg\n      file: rotini.go\n"
	writeTestFile(t, filepath.Join(tmp, "cmd/app/.rotini.spec.yaml"), spec)
	writeTestFile(t, filepath.Join(tmp, "cmd/app/.rotini.conf.yaml"), conf)

	if err := Generate("cmd/app/.rotini.spec.yaml", "cmd/app/.rotini.conf.yaml", false, "", nil); err != nil {
		t.Fatalf("generate: %v", err)
	}
	mustContain(t, filepath.Join(tmp, "cmd/app/rtg/rotini.go"),
		"FlagGroups:", `Kind: "mutually_exclusive"`, `Flags: []string{"json", "yaml"}`)
}

func TestGenerate_flagDependenciesInDefinition(t *testing.T) {
	tmp := initTestModule(t)
	spec := `$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-spec.json
name: app
flags:
  - name: tls
    identifiers: [--tls]
    schema: { type: bool }
  - name: cert
    identifiers: [--cert]
    schema: { type: string }
  - name: key
    identifiers: [--key]
    schema: { type: string }
flag_dependencies:
  - when: tls
    requires: [cert, key]
`
	conf := "$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-conf.json\n" +
		"generate:\n  packages:\n    cmd:\n      package: cmd/app/rth\n    cmdgen:\n      package: cmd/app/rtg\n      file: rotini.go\n"
	writeTestFile(t, filepath.Join(tmp, "cmd/app/.rotini.spec.yaml"), spec)
	writeTestFile(t, filepath.Join(tmp, "cmd/app/.rotini.conf.yaml"), conf)

	if err := Generate("cmd/app/.rotini.spec.yaml", "cmd/app/.rotini.conf.yaml", false, "", nil); err != nil {
		t.Fatalf("generate: %v", err)
	}
	mustContain(t, filepath.Join(tmp, "cmd/app/rtg/rotini.go"),
		"FlagDependencies:", `When: "tls"`, `Requires: []string{"cert", "key"}`)
}

func mustNotContain(t *testing.T, path string, subs ...string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	for _, s := range subs {
		if strings.Contains(string(data), s) {
			t.Errorf("%s unexpectedly contains %q", path, s)
		}
	}
}

// TestTemplateFuncMap asserts the template helper set is exactly the documented,
// deterministic allowlist — no clock/entropy functions (which would break
// byte-stable rendering) and no third-party dependency.
func TestTemplateFuncMap(t *testing.T) {
	fm := templateFuncMap()
	want := []string{
		"join", "upper", "lower", "title", "trim", "trimPrefix", "trimSuffix",
		"replace", "indent", "repeat", "default", "contains", "hasPrefix",
		"hasSuffix", "first", "last",
	}
	if len(fm) != len(want) {
		t.Errorf("templateFuncMap has %d funcs, want %d", len(fm), len(want))
	}
	for _, k := range want {
		if _, ok := fm[k]; !ok {
			t.Errorf("templateFuncMap missing %q", k)
		}
	}
	for _, bad := range []string{"now", "date", "uuidv4", "randAlpha", "randNumeric", "randBytes"} {
		if _, ok := fm[bad]; ok {
			t.Errorf("templateFuncMap unexpectedly exposes non-deterministic %q", bad)
		}
	}
	if got := titleASCII("foo-bar baz"); got != "Foo-Bar Baz" {
		t.Errorf("titleASCII(%q) = %q, want %q", "foo-bar baz", got, "Foo-Bar Baz")
	}
}

// TestGenerateHelpHiddenDeprecated verifies hidden commands/inputs are omitted
// from rendered help and deprecated ones are annotated.
func TestGenerateHelpHiddenDeprecated(t *testing.T) {
	tmp := t.TempDir()
	writeTestFile(t, filepath.Join(tmp, "go.mod"), minimalGoMod)
	writeTestFile(t, filepath.Join(tmp, ".rotini.spec.yaml"),
		"$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-spec.json\n"+
			""+
			"name: mycli\n"+
			"commands:\n"+
			"  - name: secret\n"+
			"    hidden: true\n"+
			"    summary: a hidden command\n"+
			"  - name: legacy\n"+
			"    deprecated: use modern instead\n"+
			"    summary: an old command\n"+
			"  - name: run\n"+
			"    summary: run it\n"+
			"    arguments:\n"+
			"      - name: target\n"+
			"        summary: the target\n"+
			"        deprecated: positional is going away\n"+
			"        schema: { type: string, required: true }\n"+
			"    flags:\n"+
			"      - name: secretflag\n"+
			"        summary: a hidden flag\n"+
			"        hidden: true\n"+
			"        identifiers: [--secret]\n"+
			"        schema: { type: bool }\n"+
			"      - name: verbose\n"+
			"        summary: chatty output\n"+
			"        identifiers: [-v]\n"+
			"        schema: { type: bool }\n")
	writeTestFile(t, filepath.Join(tmp, ".rotini.conf.yaml"), helpEnabledConf)

	t.Chdir(tmp)
	if err := Generate(".rotini.spec.yaml", ".rotini.conf.yaml", false, "", nil); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	root := filepath.Join(tmp, "internal", "cmd", "mycli", "renders", "help_mycli.txt")
	mustContain(t, root, "legacy", "(deprecated: use modern instead)", "an old command")
	mustNotContain(t, root, "secret", "a hidden command")

	run := filepath.Join(tmp, "internal", "cmd", "mycli", "renders", "help_mycli_run.txt")
	mustContain(t, run, "<target>", "(deprecated: positional is going away)", "-v", "chatty output")
	mustNotContain(t, run, "--secret", "a hidden flag")
}

// TestGenerateHelpComposition verifies a $ref-composed child's commands render
// through the COMPOSING parent's help template using the child's spec content,
// so a merged binary has one consistent help style.
func TestGenerateHelpComposition(t *testing.T) {
	tmp := initTestModule(t)

	childSpec := "$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-spec.json\n" +
		"name: child\n" +
		"summary: the child program\n" +
		"description: A composed child.\n" +
		"commands:\n" +
		"  - name: greet\n" +
		"    summary: say hello\n"
	helpConf := func(dir string) string {
		return "$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-conf.json\n" +
			"generate:\n" +
			"  packages:\n" +
			"    cmd: { package: cmd/" + dir + "/rth, file: handlers.go }\n" +
			"    cmdgen: { package: cmd/" + dir + "/rtg, file: rotini.go }\n" +
			"  features: { help: { enabled: true, embed: true, template: true } }\n"
	}
	parentSpec := "$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-spec.json\n" +
		"name: parent\n" +
		"commands:\n" +
		"  - $ref: ../child/.rotini.spec.yaml\n"

	writeTestFile(t, filepath.Join(tmp, "cmd/child/.rotini.spec.yaml"), childSpec)
	writeTestFile(t, filepath.Join(tmp, "cmd/child/.rotini.conf.yaml"), helpConf("child"))
	writeTestFile(t, filepath.Join(tmp, "cmd/parent/.rotini.spec.yaml"), parentSpec)
	writeTestFile(t, filepath.Join(tmp, "cmd/parent/.rotini.conf.yaml"), helpConf("parent"))

	if err := Generate("cmd/child/.rotini.spec.yaml", "cmd/child/.rotini.conf.yaml", false, "", nil); err != nil {
		t.Fatalf("generate child: %v", err)
	}
	if err := Generate("cmd/parent/.rotini.spec.yaml", "cmd/parent/.rotini.conf.yaml", false, "", nil); err != nil {
		t.Fatalf("generate parent: %v", err)
	}

	// The parent's command list shows the composed child via the child's summary.
	mustContain(t, filepath.Join(tmp, "cmd/parent/rtg/renders/help_parent.txt"),
		"child", "the child program")
	// The composed child's own page (rendered by the parent) carries the child's
	// content, including its sub-command.
	mustContain(t, filepath.Join(tmp, "cmd/parent/rtg/renders/help_parent_child.txt"),
		"A composed child.", "greet", "say hello")
	// And the grandchild command page exists with its content.
	mustContain(t, filepath.Join(tmp, "cmd/parent/rtg/renders/help_parent_child_greet.txt"),
		"parent child greet")
}

// updateHelpGolden rewrites the committed golden files under internal/testdata/help
// instead of comparing against them. Run: go test ./internal -run TestHelpGolden
// -update-help-golden — then review the diff before committing.
var updateHelpGolden = flag.Bool("update-help-golden", false, "rewrite internal/testdata/help golden files")

const goldenSpecSchema = "$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-spec.json\n"

// helpGoldenDir returns the absolute path to internal/testdata/help. It must be
// called before genHelp (which t.Chdir's into a temp module).
func helpGoldenDir(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	return filepath.Join(wd, "testdata", "help")
}

// assertHelpGolden compares got against testdata/help/<name>, or rewrites it under
// -update-help-golden.
func assertHelpGolden(t *testing.T, goldenDir, name, got string) {
	t.Helper()
	path := filepath.Join(goldenDir, name)
	if *updateHelpGolden {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir golden dir: %v", err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatalf("write golden %s: %v", name, err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v\n(create/update with: go test ./internal -run TestHelpGolden -update-help-golden)", path, err)
	}
	if got != string(want) {
		t.Errorf("golden %q mismatch:\n--- got (%d bytes) ---\n%s\n<<<EOF\n--- want (%d bytes) ---\n%s\n<<<EOF",
			name, len(got), got, len(want), string(want))
	}
}

// genHelp generates spec (with help enabled) into a fresh temp module and returns
// the absolute help dir. When preTmpl is non-empty it is written as the
// help.txt.tmpl before generation, simulating an end-user-edited template.
func genHelp(t *testing.T, spec, preTmpl string) string {
	t.Helper()
	tmp := t.TempDir()
	writeTestFile(t, filepath.Join(tmp, "go.mod"), minimalGoMod)
	writeTestFile(t, filepath.Join(tmp, ".rotini.spec.yaml"), spec)
	writeTestFile(t, filepath.Join(tmp, ".rotini.conf.yaml"), helpEnabledConf)
	if preTmpl != "" {
		writeTestFile(t, filepath.Join(tmp, "internal", "cmd", "app", "templates", "help.txt.tmpl"), preTmpl)
	}
	t.Chdir(tmp)
	if err := Generate(".rotini.spec.yaml", ".rotini.conf.yaml", false, "", nil); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	return filepath.Join(tmp, "internal", "cmd", "app", "renders")
}

// helpGoldenGeneratedSpec exercises the full generated-help surface: root header/
// description/usage-override/custom-heading/examples/footer; a rich sub-command with
// required/optional/variadic args and flags with type/default/enum/required/
// deprecated/hidden; a deprecated command; and a hidden command.
const helpGoldenGeneratedSpec = goldenSpecSchema +
	`name: app
summary: the app
description: |
  A demo application.

  A second paragraph.
usage: app <command> [flags]
header: '===== APP ====='
footer: Use "app help <command>" for details.
headings:
  commands: Subcommands
examples:
  - app build ./src
commands:
  - name: build
    aliases: [b, bld]
    summary: build things
    description: Build the project from sources.
    examples:
      - app build ./src
      - app build ./src --force
    arguments:
      - name: target
        summary: what to build
        schema: { type: string, required: true }
      - name: extra
        summary: extra targets
        schema: { type: array }
    flags:
      - name: output
        summary: output directory
        identifiers: [-o, --output]
        schema: { type: string, default: ./dist }
      - name: format
        summary: archive format
        identifiers: [--format]
        schema: { type: string, enum: [tar, zip], default: tar }
      - name: force
        summary: overwrite existing output
        identifiers: [-f, --force]
        schema: { type: bool, required: true }
      - name: legacy
        summary: legacy flag
        identifiers: [--legacy]
        deprecated: use --modern
        schema: { type: bool }
      - name: secret
        summary: hidden flag
        identifiers: [--secret]
        hidden: true
        schema: { type: bool }
  - name: oldcmd
    summary: an old command
    deprecated: use build instead
  - name: secretcmd
    summary: a hidden command
    hidden: true
`

// TestHelpGolden_Generated locks the default-template rendering across every
// structured-help feature against committed golden files.
func TestHelpGolden_Generated(t *testing.T) {
	goldenDir := helpGoldenDir(t)
	helpDir := genHelp(t, helpGoldenGeneratedSpec, "")
	for _, f := range []string{"app", "app_build", "app_oldcmd", "app_secretcmd"} {
		assertHelpGolden(t, goldenDir, "generated_"+f+".txt", readFileString(t, filepath.Join(helpDir, "help_"+f+".txt")))
	}
}

const helpGoldenCustomSpec = goldenSpecSchema +
	`name: app
summary: my app
footer: bye now
commands:
  - name: run
    summary: run it
    flags:
      - name: verbose
        summary: be loud
        identifiers: [-v, --verbose]
        schema: { type: bool }
`

// helpGoldenCustomTmpl is a deliberately non-default template: it reorders/omits
// sections, upper-cases the summary, draws a rule, and uses a custom flag layout —
// exercising the helper funcs and proving an end-user-edited template is honored.
const helpGoldenCustomTmpl = `{{- with .Summary}}{{upper .}}
{{end -}}
{{repeat 20 "="}}
{{- with .Flags}}

{{$.Headings.Flags}}
{{range .}}  {{join .Identifiers ", "}} :: {{.Summary}}
{{end}}
{{end -}}
{{- with .Footer}}
{{.}}
{{end -}}
`

// TestHelpGolden_CustomTemplate proves that editing help.txt.tmpl changes the
// rendered output (and that rotini does not overwrite the user's template).
func TestHelpGolden_CustomTemplate(t *testing.T) {
	goldenDir := helpGoldenDir(t)
	helpDir := genHelp(t, helpGoldenCustomSpec, helpGoldenCustomTmpl)
	assertHelpGolden(t, goldenDir, "custom_app_run.txt", readFileString(t, filepath.Join(helpDir, "help_app_run.txt")))
	// The user's template survives generation untouched.
	mustFileEqual(t, filepath.Join(filepath.Dir(helpDir), "templates", "help.txt.tmpl"), helpGoldenCustomTmpl)
}

const helpGoldenVerbatimSpec = goldenSpecSchema +
	`name: app
help: |-
  EXACT ROOT PAGE
    indented line kept as-is
  no trailing newline
commands:
  - name: run
    summary: run it
    help: |
      EXACT RUN PAGE
      keeps its trailing newline
`

// TestHelpGolden_Verbatim proves an explicit command.help string is written
// byte-for-byte (a |- block has no trailing newline; a | block keeps one), and that
// when every command is verbatim no template is seeded.
func TestHelpGolden_Verbatim(t *testing.T) {
	goldenDir := helpGoldenDir(t)
	helpDir := genHelp(t, helpGoldenVerbatimSpec, "")
	assertHelpGolden(t, goldenDir, "verbatim_app.txt", readFileString(t, filepath.Join(helpDir, "help_app.txt")))
	assertHelpGolden(t, goldenDir, "verbatim_app_run.txt", readFileString(t, filepath.Join(helpDir, "help_app_run.txt")))
	if _, err := os.Stat(filepath.Join(helpDir, "help.txt.tmpl")); !os.IsNotExist(err) {
		t.Errorf("template should not be seeded when every command is verbatim (err=%v)", err)
	}
}

// helpGoldenCascadingSpec: a root with one cascading flag (--verbose) and one
// non-cascading flag (--root-only), plus two children. 'run' uses the default
// cascading heading; 'deploy' overrides it with a colon-free value.
const helpGoldenCascadingSpec = goldenSpecSchema +
	`name: app
summary: the app
flags:
  - name: verbose
    summary: verbose logging
    identifiers: [-v, --verbose]
    cascading: true
    schema: { type: bool }
  - name: rootonly
    summary: root-only flag
    identifiers: [--root-only]
    schema: { type: bool }
commands:
  - name: run
    summary: run it
    flags:
      - name: jobs
        summary: parallelism
        identifiers: [-j, --jobs]
        schema: { type: int }
  - name: deploy
    summary: deploy it
    headings:
      cascading: Inherited Flags
    flags:
      - name: target
        summary: where to deploy
        identifiers: [--target]
        schema: { type: string }
`

// TestHelpGolden_Cascading locks the cascading-flags reporting: a cascading flag
// is advertised on descendants under the 'Global Flags:' section (default heading),
// the root itself shows no such section, a non-cascading root flag never leaks down,
// and headings.cascading overrides the heading verbatim (no forced colon).
func TestHelpGolden_Cascading(t *testing.T) {
	goldenDir := helpGoldenDir(t)
	helpDir := genHelp(t, helpGoldenCascadingSpec, "")
	for _, f := range []string{"app", "app_run", "app_deploy"} {
		assertHelpGolden(t, goldenDir, "cascading_"+f+".txt", readFileString(t, filepath.Join(helpDir, "help_"+f+".txt")))
	}
}

// helpGoldenEnvConfigSpec exercises the Environment + Configuration help sections: an
// env input with an explicit variable and one whose variable is derived (snake-upper),
// and config inputs with and without an explicit file/key location.
const helpGoldenEnvConfigSpec = goldenSpecSchema +
	`name: app
summary: the app
env:
  - name: token
    summary: API auth token
    schema: { type: string, required: true, variable: APP_TOKEN }
  - name: maxRetries
    summary: retry budget
    schema: { type: int, default: 3 }
config:
  - name: endpoint
    summary: API endpoint
    schema: { type: string, file: app, key: api.endpoint }
  - name: timeout
    summary: request timeout
    schema: { type: int, default: 30 }
config_files:
  - name: app
    path: ~/.app.yaml
    format: yaml
`

// TestHelpGolden_EnvConfig locks the rendering of env-var and config inputs in the
// generated help page (explicit vs derived env var, located vs bare config key).
func TestHelpGolden_EnvConfig(t *testing.T) {
	goldenDir := helpGoldenDir(t)
	helpDir := genHelp(t, helpGoldenEnvConfigSpec, "")
	assertHelpGolden(t, goldenDir, "envconfig_app.txt", readFileString(t, filepath.Join(helpDir, "help_app.txt")))
}

// helpGoldenGroupsSpec exercises command grouping: an ungrouped command (default
// heading), then two named groups, with groups appearing in first-declaration order.
const helpGoldenGroupsSpec = goldenSpecSchema +
	`name: app
summary: the app
commands:
  - name: version
    summary: print the version
  - name: get
    summary: display a resource
    group: Basic Commands
  - name: apply
    summary: apply a configuration
    group: Basic Commands
  - name: cluster-info
    summary: show cluster endpoints
    group: Cluster Management
`

// TestHelpGolden_CommandGroups locks command grouping: ungrouped commands fall under the
// default "Commands:" heading, grouped commands are bucketed under their group title (in
// first-appearance order), all in one Commands section.
func TestHelpGolden_CommandGroups(t *testing.T) {
	goldenDir := helpGoldenDir(t)
	helpDir := genHelp(t, helpGoldenGroupsSpec, "")
	assertHelpGolden(t, goldenDir, "groups_app.txt", readFileString(t, filepath.Join(helpDir, "help_app.txt")))
}

func TestCompletionScript(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish", "powershell"} {
		script, err := completionScript("myprog", shell)
		if err != nil {
			t.Errorf("%s: %v", shell, err)
			continue
		}
		if !strings.Contains(script, "myprog") || !strings.Contains(script, "__complete") {
			t.Errorf("%s script missing prog/__complete:\n%s", shell, script)
		}
	}
	if _, err := completionScript("p", "nushell"); err == nil {
		t.Error("nushell should be unsupported")
	}
	if _, err := completionScript("p", ""); err == nil {
		t.Error("empty shell should error")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Focused unit tests for the pure helpers — the spec→literal and spec→display
// derivations the end-to-end generate tests exercise only on their happy paths.
// ─────────────────────────────────────────────────────────────────────────────

func TestDefaultString(t *testing.T) {
	cases := []struct {
		in   any
		want string
	}{
		{nil, ""},
		{"text", "text"},
		{true, "true"},
		{float64(2.5), "2.5"},
		{int(7), "7"},
		{int64(9), "9"},
		{[]string{"x"}, "[x]"}, // anything else falls through to %v
	}
	for _, tc := range cases {
		if got := defaultString(tc.in); got != tc.want {
			t.Errorf("defaultString(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestGoRawString(t *testing.T) {
	if got := goRawString(`{"a":1}`); got != "`"+`{"a":1}`+"`" {
		t.Errorf("goRawString(plain) = %s, want a backtick literal", got)
	}
	if got := goRawString("has`tick"); got != `"has`+"`"+`tick"` {
		t.Errorf("goRawString(backtick) = %s, want a quoted literal", got)
	}
}

func TestJSONSchemaTypeToGo(t *testing.T) {
	cases := map[string]string{
		"boolean": "bool", "bool": "bool",
		"integer": "int", "int": "int",
		"number": "float64", "float64": "float64",
		"array": "[]string", "[]string": "[]string",
		"object": "map[string]any", "map": "map[string]any",
		"duration": "time.Duration",
		"time":     "time.Time", "datetime": "time.Time", "date": "time.Time",
		"uuid.UUID": "uuid.UUID", // unknown types pass through
	}
	for in, want := range cases {
		if got := jsonSchemaTypeToGo(in); got != want {
			t.Errorf("jsonSchemaTypeToGo(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestGoFieldType(t *testing.T) {
	if got := goFieldType(nil); got != "string" {
		t.Errorf("goFieldType(nil) = %q, want string", got)
	}
	nullable := &InputSchema{BaseSchema: BaseSchema{Type: "int"}, Required: false}
	nullable.Nullable = true
	if got := goFieldType(nullable); got != "*int" {
		t.Errorf("goFieldType(nullable int) = %q, want *int", got)
	}
}

func TestGetSchemaType_arrayItems(t *testing.T) {
	cases := []struct {
		name  string
		typ   string
		items *Schema
		want  string
	}{
		{"array without items stays []string", "array", nil, "[]string"},
		{"array of int", "array", &Schema{BaseSchema: BaseSchema{Type: "int"}}, "[]int"},
		{"array of integer (JSON Schema name)", "array", &Schema{BaseSchema: BaseSchema{Type: "integer"}}, "[]int"},
		{"array of duration", "array", &Schema{BaseSchema: BaseSchema{Type: "duration"}}, "[]time.Duration"},
		{"array of imported type", "array", &Schema{BaseSchema: BaseSchema{Type: "uuid.UUID"}}, "[]uuid.UUID"},
		{"array of named schema", "array", &Schema{BaseSchema: BaseSchema{Ref: "#/schemas/Widget"}}, "[]Widget"},
		{"[]string spelling honors items too", "[]string", &Schema{BaseSchema: BaseSchema{Type: "int"}}, "[]int"},
		{"items without a type default to string", "array", &Schema{}, "[]string"},
		{"non-array type ignores items", "int", &Schema{BaseSchema: BaseSchema{Type: "bool"}}, "int"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &InputSchema{BaseSchema: BaseSchema{Type: tc.typ, Items: tc.items}}
			if got := getSchemaType(s); got != tc.want {
				t.Errorf("getSchemaType(%s items=%+v) = %q, want %q", tc.typ, tc.items, got, tc.want)
			}
		})
	}
}

func TestKeyPaths(t *testing.T) {
	obj := func(props map[string]Schema) Schema {
		return Schema{BaseSchema: BaseSchema{Type: "object", Properties: props}}
	}
	nested := map[string]Schema{
		"image":    obj(map[string]Schema{"tag": {}, "pullPolicy": {}}),
		"replicas": {},
	}

	dotted := &InputSchema{BaseSchema: BaseSchema{Type: "map", Properties: nested}, DottedKeys: true}
	if got, want := keyPaths(dotted), []string{"image.pullPolicy", "image.tag", "replicas"}; !slices.Equal(got, want) {
		t.Errorf("keyPaths(dotted) = %v, want %v", got, want)
	}

	// Without dotted_keys only top-level names are keys ('.' would be literal).
	plain := &InputSchema{BaseSchema: BaseSchema{Type: "map", Properties: nested}}
	if got, want := keyPaths(plain), []string{"image", "replicas"}; !slices.Equal(got, want) {
		t.Errorf("keyPaths(plain) = %v, want %v", got, want)
	}

	// Non-map types and property-less maps offer no key vocabulary.
	if got := keyPaths(&InputSchema{BaseSchema: BaseSchema{Type: "string", Properties: nested}}); got != nil {
		t.Errorf("keyPaths(non-map) = %v, want nil", got)
	}
	if got := keyPaths(&InputSchema{BaseSchema: BaseSchema{Type: "map"}}); got != nil {
		t.Errorf("keyPaths(no properties) = %v, want nil", got)
	}
	if got := keyPaths(nil); got != nil {
		t.Errorf("keyPaths(nil) = %v, want nil", got)
	}
}

func TestFlagDefsLiteral_dottedKeys(t *testing.T) {
	in := &Inputs{Flags: []FlagInput{{
		Name: "set",
		Schema: &InputSchema{
			BaseSchema: BaseSchema{Type: "map", Properties: map[string]Schema{"replicas": {}}},
			DottedKeys: true,
		},
	}}}
	got := flagDefsLiteral(in)
	for _, want := range []string{"DottedKeys: true", `KeyPaths: []string{"replicas"}`, `Type: "map[string]any"`} {
		if !strings.Contains(got, want) {
			t.Errorf("flagDefsLiteral missing %q in:\n%s", want, got)
		}
	}
}

func TestFlagDefsLiteral_from(t *testing.T) {
	in := &Inputs{Flags: []FlagInput{{
		Name:   "token",
		Schema: &InputSchema{BaseSchema: BaseSchema{Type: "string"}, From: []string{"value", "file"}},
	}}}
	if got, want := flagDefsLiteral(in), `From: []string{"value", "file"}`; !strings.Contains(got, want) {
		t.Errorf("flagDefsLiteral missing %q in:\n%s", want, got)
	}
}

func TestFieldImport_arrayItems(t *testing.T) {
	cases := []struct {
		name   string
		schema *InputSchema
		want   string
	}{
		{"items duration implies time", &InputSchema{BaseSchema: BaseSchema{Type: "array", Items: &Schema{BaseSchema: BaseSchema{Type: "duration"}}}}, "time"},
		{"items explicit import", &InputSchema{BaseSchema: BaseSchema{Type: "array", Items: &Schema{BaseSchema: BaseSchema{Type: "uuid.UUID", Import: "github.com/google/uuid"}}}}, "github.com/google/uuid"},
		{"schema-level import wins", &InputSchema{BaseSchema: BaseSchema{Type: "array", Import: "example.com/x", Items: &Schema{BaseSchema: BaseSchema{Type: "duration"}}}}, "example.com/x"},
		{"plain array needs none", &InputSchema{BaseSchema: BaseSchema{Type: "array"}}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := fieldImport(tc.schema); got != tc.want {
				t.Errorf("fieldImport = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestEnvVarOf(t *testing.T) {
	if got := envVarOf(nil); got != "" {
		t.Errorf("envVarOf(nil) = %q, want empty", got)
	}
	if got := envVarOf(&InputSchema{Variable: "APP_TOKEN"}); got != "APP_TOKEN" {
		t.Errorf("envVarOf(explicit) = %q, want APP_TOKEN", got)
	}
}

func TestConfigLocation(t *testing.T) {
	cases := []struct {
		name  string
		input ConfigInput
		want  string
	}{
		{"nil schema", ConfigInput{Name: "x"}, ""},
		{"file and key", ConfigInput{Name: "x", Schema: &InputSchema{File: "app", Key: "a.b"}}, "app.a.b"},
		{"file without key uses the name", ConfigInput{Name: "x", Schema: &InputSchema{File: "app"}}, "app.x"},
		{"bare key", ConfigInput{Name: "x", Schema: &InputSchema{Key: "a.b"}}, "a.b"},
		{"neither", ConfigInput{Name: "x", Schema: &InputSchema{}}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := configLocation(tc.input); got != tc.want {
				t.Errorf("configLocation = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestFlagIdentifiers(t *testing.T) {
	explicit := FlagInput{Name: "out", Identifiers: []string{"-o"}}
	if got := flagIdentifiers(explicit); len(got) != 1 || got[0] != "-o" {
		t.Errorf("flagIdentifiers(explicit) = %v", got)
	}
	derived := FlagInput{Name: "dry_run"}
	if got := flagIdentifiers(derived); len(got) != 1 || got[0] != "--dry-run" {
		t.Errorf("flagIdentifiers(derived) = %v, want [--dry-run]", got)
	}
}

func TestSchemaAccessors_nil(t *testing.T) {
	if got := schemaDefaultString(nil); got != "" {
		t.Errorf("schemaDefaultString(nil) = %q, want empty", got)
	}
	if got := enumOf(nil); got != nil {
		t.Errorf("enumOf(nil) = %v, want nil", got)
	}
}

func ptrF(f float64) *float64 { return &f }

func TestConstraintsLiteral(t *testing.T) {
	full := &InputSchema{}
	full.Minimum, full.Maximum = ptrF(1), ptrF(65535)
	full.ExclusiveMinimum, full.MultipleOf = ptrF(0), ptrF(0.5)
	full.MinLength, full.MaxLength = 2, 5
	full.MinItems, full.MaxItems = 3, 4
	full.Pattern = "^x$"
	got := constraintsLiteral(full)
	for _, want := range []string{
		"Minimum: rotini.Ptr[float64](1)", "Maximum: rotini.Ptr[float64](65535)",
		"ExclusiveMinimum: rotini.Ptr[float64](0)", "MultipleOf: rotini.Ptr[float64](0.5)",
		"MinLength: 2", "MaxLength: 5",
		"MinItems: 3", "MaxItems: 4", `Pattern: "^x$"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("constraintsLiteral missing %q in %s", want, got)
		}
	}
	if got := constraintsLiteral(&InputSchema{}); got != "" {
		t.Errorf("constraintsLiteral(zero) = %q, want empty", got)
	}
}

func TestRemoteDefsLiteral(t *testing.T) {
	rcs := []RemoteCommandSpec{
		{Name: "plugin", Aliases: []string{"p"}, Timeout: "10s"},
		{Name: "noop", Timeout: "not-a-duration"}, // unparsable timeout is dropped
	}
	got := remoteDefsLiteral("acme", rcs)
	for _, want := range []string{
		`Name: "plugin"`, `Binary: "acme-plugin"`, `Aliases: []string{"p"}`,
		"Timeout: 10000000000", `Binary: "acme-noop"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("remoteDefsLiteral missing %q in %s", want, got)
		}
	}
	if strings.Contains(got, `Name: "noop", Binary: "acme-noop", Timeout`) {
		t.Errorf("unparsable timeout should be omitted: %s", got)
	}
	if got := remoteDefsLiteral("acme", nil); got != "" {
		t.Errorf("remoteDefsLiteral(none) = %q, want empty", got)
	}

	// Opt-in verify (W9): a version handshake and/or a pinned sha256 emit a Verify literal.
	verified := remoteDefsLiteral("acme", []RemoteCommandSpec{
		{Name: "deploy", Verify: &RemoteVerifySpec{Version: true, Sha256: "sha256:abc"}},
		{Name: "plain"}, // no verify → no Verify field
	})
	for _, want := range []string{
		`Verify: &rotini.RemoteVerify{Version: true, SHA256: "sha256:abc"}`,
		`Name: "plain", Binary: "acme-plain"}`,
	} {
		if !strings.Contains(verified, want) {
			t.Errorf("remoteDefsLiteral missing %q in %s", want, verified)
		}
	}
	if strings.Contains(verified, `Name: "plain", Binary: "acme-plain", Verify`) {
		t.Errorf("a remote without verify should emit no Verify field: %s", verified)
	}

	// Keyless signature rung (D-W9.10): a signature identity emits a nested Signature literal,
	// composing with the other verify rungs.
	signed := remoteDefsLiteral("acme", []RemoteCommandSpec{
		{Name: "deploy", Verify: &RemoteVerifySpec{
			Sha256:    "sha256:abc",
			Signature: &RemoteSignatureSpec{Issuer: "https://oidc.example", Subject: "repo:acme/clis"},
		}},
	})
	if want := `Verify: &rotini.RemoteVerify{SHA256: "sha256:abc", Signature: &rotini.RemoteSignatureVerify{Issuer: "https://oidc.example", Subject: "repo:acme/clis"}}`; !strings.Contains(signed, want) {
		t.Errorf("remoteDefsLiteral missing %q in %s", want, signed)
	}
}

func TestResolveHeadings(t *testing.T) {
	defaults := resolveHeadings(cmdHelp{})
	if defaults.Usage != "Usage:" || defaults.Cascading != "Global Flags:" {
		t.Errorf("default headings wrong: %+v", defaults)
	}

	overridden := resolveHeadings(cmdHelp{Headings: &HelpHeadings{
		Usage: "USAGE", Commands: "CMDS", Arguments: "ARGS", Flags: "OPTS",
		Environment: "ENV", Configuration: "CONF", Cascading: "INHERITED", Examples: "EG",
	}})
	want := templateDocHeadings{
		Usage: "USAGE", Commands: "CMDS", Arguments: "ARGS", Flags: "OPTS",
		Environment: "ENV", Configuration: "CONF", Cascading: "INHERITED", Examples: "EG",
	}
	if overridden != want {
		t.Errorf("overridden headings = %+v, want %+v", overridden, want)
	}
}

func TestAddImport_dedupes(t *testing.T) {
	gp := &genProgram{}
	gp.addImport("childcli", "example.com/child")
	gp.addImport("othercli", "example.com/child") // same path: dropped
	gp.addImport("othercli", "example.com/other")
	if len(gp.childImports) != 2 {
		t.Fatalf("childImports = %v, want 2 unique paths", gp.childImports)
	}
	if gp.childImports[0].Alias != "childcli" || gp.childImports[1].Path != "example.com/other" {
		t.Errorf("childImports = %v", gp.childImports)
	}
}

func TestChildCliImport(t *testing.T) {
	// A child conf naming the cmd package wins.
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, ".rotini.conf.yaml"),
		"generate:\n  packages:\n    cmd:\n      package: custom/handlers\n")
	if got := childCliImport(dir, "example.com/mod"); got != "example.com/mod/custom/handlers" {
		t.Errorf("childCliImport(with conf) = %q", got)
	}

	// A conf without a cmd package falls through to the convention.
	dir2 := t.TempDir()
	writeTestFile(t, filepath.Join(dir2, ".rotini.conf.yaml"), "validate:\n  fail: collect\n")
	if got := childCliImport(dir2, "example.com/mod"); got != "example.com/mod/internal/cmd/"+filepath.Base(dir2) {
		t.Errorf("childCliImport(conf without cmd) = %q", got)
	}

	// No conf at all also falls through.
	dir3 := t.TempDir()
	if got := childCliImport(dir3, "example.com/mod"); got != "example.com/mod/internal/cmd/"+filepath.Base(dir3) {
		t.Errorf("childCliImport(no conf) = %q", got)
	}

	// Every conf serialization is honored — including TOML.
	dir4 := t.TempDir()
	writeTestFile(t, filepath.Join(dir4, ".rotini.conf.toml"),
		"[generate.packages.cmd]\npackage = \"toml/handlers\"\n")
	if got := childCliImport(dir4, "example.com/mod"); got != "example.com/mod/toml/handlers" {
		t.Errorf("childCliImport(toml conf) = %q", got)
	}
}

// TestGoPkgName: a hyphenated CLI name yields a valid (hyphen-free) Go package
// name while the import PATH keeps the original segment. Regression for
// `rotini init agentic-cooking` producing `package agentic-cooking` (invalid Go).
func TestGoPkgName(t *testing.T) {
	cases := map[string]string{
		"cmd/agentic-cooking": "agenticcooking",
		"internal/cmd/my-cli": "mycli",
		"internal/cmd/mycli":  "mycli",
		"a/b/foo_bar":         "foo_bar", // underscores are valid identifier runes — kept
		"x/a-b-c":             "abc",
	}
	for in, want := range cases {
		if got := goPkgName(in); got != want {
			t.Errorf("goPkgName(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestGenerateFeatureDirEqualsCmdgen covers the feature dir resolving to the
// cmdgen package dir itself: the embed path must be the bare file name — a
// "./file" pattern is invalid //go:embed syntax.
func TestGenerateFeatureDirEqualsCmdgen(t *testing.T) {
	tmp := t.TempDir()
	writeTestFile(t, filepath.Join(tmp, "go.mod"), minimalGoMod)
	writeTestFile(t, filepath.Join(tmp, ".rotini.spec.yaml"),
		"$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-spec.json\nname: mycli\n")
	writeTestFile(t, filepath.Join(tmp, ".rotini.conf.yaml"),
		confSchemaHeader+"generate:\n  features:\n    help:\n      enabled: true\n      embed: true\n      embed_dir: internal/cmd/mycli\n")
	t.Chdir(tmp)

	if err := Generate(".rotini.spec.yaml", ".rotini.conf.yaml", false, "", nil); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	gen := filepath.Join(tmp, "internal", "cmd", "mycli", "zz_rotini.gen.go")
	mustContain(t, gen, "//go:embed help_mycli.txt")
	mustNotContain(t, gen, "//go:embed ./")
}

func TestCompletionContents_unsupportedShell(t *testing.T) {
	badShell := []helpNode{{name: "nushell", file: "completion_nushell.txt"}}
	if _, err := completionContents("app", badShell); err == nil {
		t.Error("completionContents(unsupported shell) = nil, want an error")
	}
}

func TestWriteFeatureOutputs_emptyDir(t *testing.T) {
	if err := writeFeatureOutputs("", nil, nil, helpFeatureDesc); err == nil {
		t.Error("writeFeatureOutputs(empty dir) = nil, want an error")
	}
}

// TestLoadFeatureTemplate_badUserTemplate confirms a user-edited template that
// no longer parses fails loudly (naming the feature and path) instead of
// silently falling back to the embedded default.
func TestLoadFeatureTemplate_badUserTemplate(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, helpTemplateName), "{{")
	if _, err := loadFeatureTemplate(dir, helpFeatureDesc); err == nil || !strings.Contains(err.Error(), "help template") {
		t.Errorf("loadFeatureTemplate(bad template) = %v, want a parse error naming the template", err)
	}
}

// TestGenerateFeatureDirOutsideCmdgen confirms a feature dir that does not
// resolve under the cmdgen package is rejected IN EMBED MODE — //go:embed could
// not reach it. (Inline features have no such constraint; this conf sets
// embed: true to exercise the check.)
func TestGenerateFeatureDirOutsideCmdgen(t *testing.T) {
	tmp := t.TempDir()
	writeTestFile(t, filepath.Join(tmp, "go.mod"), minimalGoMod)
	writeTestFile(t, filepath.Join(tmp, ".rotini.spec.yaml"),
		"$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-spec.json\nname: mycli\n")
	writeTestFile(t, filepath.Join(tmp, ".rotini.conf.yaml"),
		confSchemaHeader+"generate:\n  features:\n    help:\n      enabled: true\n      embed: true\n      embed_dir: docs/help\n")
	t.Chdir(tmp)

	err := Generate(".rotini.spec.yaml", ".rotini.conf.yaml", false, "", nil)
	if err == nil || !strings.Contains(err.Error(), "must resolve under the cmdgen package") {
		t.Errorf("Generate(feature dir outside cmdgen) = %v, want a must-resolve-under error", err)
	}
}

// TestProcessorValidate_nilCallback confirms a nil onValidate is tolerated (the
// handlers may pass nil when they have no per-pass reporting).
func TestProcessorValidate_nilCallback(t *testing.T) {
	spec := writeTemp(t, "spec.yaml", validSpecHeader+"name: demo\n")
	if err := NewProcessor("").Validate(spec, "", false, "", nil, nil); err != nil {
		t.Errorf("Validate(nil callback) = %v, want nil", err)
	}
}

func TestStubFilename(t *testing.T) {
	cases := map[string]string{
		"app":         "app.go",
		"app_test":    "app_test_.go",    // _test.go escape
		"app_windows": "app_windows_.go", // GOOS escape
		"app_wasm":    "app_wasm_.go",    // GOARCH escape
		"app_build":   "app_build.go",
	}
	for in, want := range cases {
		if got := stubFilename(in); got != want {
			t.Errorf("stubFilename(%q) = %q, want %q", in, got, want)
		}
	}
	if got := commandStubFilename("app", "build", "custom.go"); got != "custom.go" {
		t.Errorf("commandStubFilename(override) = %q, want custom.go", got)
	}
}

func TestSnakeUpper(t *testing.T) {
	cases := map[string]string{
		"apiKey":      "API_KEY",
		"max-retries": "MAX_RETRIES",
		"a b":         "A_B",
		"v2Beta":      "V2_BETA",
	}
	for in, want := range cases {
		if got := snakeUpper(in); got != want {
			t.Errorf("snakeUpper(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestWriteEntrypoint_createOnce confirms the entrypoint main.go is never
// overwritten, and that an undeclared entrypoint writes nothing.
func TestWriteEntrypoint_createOnce(t *testing.T) {
	dir := t.TempDir()
	lay := layout{
		handlerImport:  "example.com/mod/internal/cmd/app",
		entrypointDir:  dir,
		entrypointFile: "main.go",
	}
	if err := writeEntrypoint(lay, "jsonc"); err != nil {
		t.Fatalf("writeEntrypoint: %v", err)
	}
	path := filepath.Join(dir, "main.go")
	// The //go:generate directive points at the seeded spec/conf via the extension.
	mustContain(t, path, "rotini generate ./.rotini.spec.jsonc --config ./.rotini.conf.jsonc")
	writeTestFile(t, path, "package main // EDITED\n")
	if err := writeEntrypoint(lay, "jsonc"); err != nil {
		t.Fatalf("writeEntrypoint (second): %v", err)
	}
	mustContain(t, path, "EDITED")

	if err := writeEntrypoint(layout{}, "yaml"); err != nil {
		t.Errorf("writeEntrypoint(no entrypoint) = %v, want nil", err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("unexpected files written: %v", entries)
	}
}

// TestGenerateHiddenInDefinition confirms hidden commands/flags/arguments are
// recorded in the generated Definition — the runtime excludes them from shell
// completion (they still parse and dispatch).
func TestGenerateHiddenInDefinition(t *testing.T) {
	tmp := t.TempDir()
	writeTestFile(t, filepath.Join(tmp, "go.mod"), minimalGoMod)
	writeTestFile(t, filepath.Join(tmp, ".rotini.spec.yaml"),
		"$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-spec.json\n"+
			""+
			"name: app\n"+
			"flags:\n"+
			"  - name: secret\n"+
			"    hidden: true\n"+
			"    identifiers: [--secret]\n"+
			"    schema: { type: bool }\n"+
			"  - name: loud\n"+
			"    identifiers: [--loud]\n"+
			"    schema: { type: bool }\n"+
			"arguments:\n"+
			"  - name: ghostarg\n"+
			"    hidden: true\n"+
			"    schema: { type: string }\n"+
			"commands:\n"+
			"  - name: ghost\n"+
			"    hidden: true\n"+
			"  - name: run\n")
	t.Chdir(tmp)
	if err := Generate(".rotini.spec.yaml", "", false, "", nil); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	gen := filepath.Join(tmp, "internal", "cmd", "app", "zz_rotini.gen.go")
	mustContain(t, gen,
		`{Name: "secret", Identifiers: []string{"--secret"}, Type: "bool", Hidden: true}`,
		`{Name: "ghostarg", Type: "string", Hidden: true}`,
	)
	src := readFileString(t, gen)
	ghost := src[strings.Index(src, `Name: "ghost"`):]
	if !strings.Contains(ghost[:200], "Hidden:") {
		t.Errorf("ghost command literal missing Hidden:\n%s", ghost[:200])
	}
	if strings.Contains(src, `Name: "loud", Identifiers: []string{"--loud"}, Type: "bool", Hidden`) {
		t.Error("visible flag must not carry Hidden")
	}
}

// TestGenerateNestedRemoteCommands verifies remote_commands declared on a
// sub-command (not just the root) reach the Definition — the runtime can only
// dispatch/complete what the literal carries — and that remotes appear in the
// parent command's help Commands list as the schema promises.
func TestGenerateNestedRemoteCommands(t *testing.T) {
	tmp := t.TempDir()
	writeTestFile(t, filepath.Join(tmp, "go.mod"), minimalGoMod)
	writeTestFile(t, filepath.Join(tmp, ".rotini.spec.yaml"),
		"$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-spec.json\n"+
			""+
			"name: acme\n"+
			"commands:\n"+
			"  - name: cluster\n"+
			"    summary: manage clusters\n"+
			"    remote_commands:\n"+
			"      - name: scan\n"+
			"        aliases: [sc]\n"+
			"        summary: scan the cluster\n"+
			"        timeout: 10s\n")
	writeTestFile(t, filepath.Join(tmp, ".rotini.conf.yaml"), helpEnabledConf)
	t.Chdir(tmp)

	if err := Generate(".rotini.spec.yaml", ".rotini.conf.yaml", false, "", nil); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	gen := filepath.Join(tmp, "internal", "cmd", "acme", "zz_rotini.gen.go")
	mustContain(t, gen,
		`Name: "cluster"`,
		"Remotes: []rotini.RemoteDef{",
		`{Name: "scan", Summary: "scan the cluster", Binary: "acme-scan", Aliases: []string{"sc"}, Timeout: 10000000000}`,
	)
	// The remote joins the cluster page's Commands list, and the usage line gains
	// the <command> slot remotes warrant.
	cluster := filepath.Join(tmp, "internal", "cmd", "acme", "renders", "help_acme_cluster.txt")
	mustContain(t, cluster, "acme cluster <command>", "scan;sc", "scan the cluster")
}

// TestGenerateStdinRequiredTag verifies a required stdin payload rides the
// generated struct tag — the binder rejects an empty stdin only when the spec
// declared required: true.
func TestGenerateStdinRequiredTag(t *testing.T) {
	tmp := t.TempDir()
	writeTestFile(t, filepath.Join(tmp, "go.mod"), minimalGoMod)
	writeTestFile(t, filepath.Join(tmp, ".rotini.spec.yaml"),
		"$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-spec.json\n"+
			""+
			"name: app\n"+
			"stdin:\n"+
			"  format: yaml\n"+
			"  schema: { type: object, required: true }\n")
	t.Chdir(tmp)

	if err := Generate(".rotini.spec.yaml", "", false, "", nil); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	mustContain(t, filepath.Join(tmp, "internal", "cmd", "app", "zz_rotini.gen.go"),
		"`stdin:\"yaml,required\"`")
}
