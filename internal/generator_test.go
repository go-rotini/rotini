package internal

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// companionConf points generation at the same package layout as the committed
// rotini companion CLI so the output can be compared against it.
const companionConf = `$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-conf.json
generate:
  packages:
    entrypoint:
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
      dir: internal/cmd/rotini/embed
    man:
      enabled: false
      dir: internal/cmd/rotini/embed
    completion:
      enabled: false
      dir: internal/cmd/rotini/embed
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

	// The companion's generated feature files (help, plus the seeded template) are
	// golden: the dogfooded output is reproduced byte-for-byte from the committed
	// spec. Only help is enabled in the committed companion conf.
	for _, feat := range []string{"help"} {
		_ = feat // only help is enabled; every feature shares the one embed dir
		featRel := "internal/cmd/rotini/embed"
		entries, err := os.ReadDir(filepath.Join(repoRoot, featRel))
		if err != nil {
			t.Fatalf("read companion %s dir: %v", feat, err)
		}
		for _, e := range entries {
			if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
				continue
			}
			got := readFileString(t, filepath.Join(tmp, featRel, e.Name()))
			want := readFileString(t, filepath.Join(repoRoot, featRel, e.Name()))
			if got != want {
				t.Errorf("companion %s/%s not reproduced:\n--- generated ---\n%s\n--- committed ---\n%s", feat, e.Name(), got, want)
			}
		}
	}

	// Handler stubs are create-if-missing user code, so the committed copies are
	// edited (wired to internal funcs) and intentionally diverge from a fresh
	// stub. Assert the generator produced each with the expected stub shape.
	for _, name := range []string{
		"rotini", "rotini_generate",
		"rotini_help", "rotini_initialize", "rotini_validate", "rotini_version",
	} {
		mustContain(t, filepath.Join(tmp, "internal/cmd/rotini", name+".go"),
			"package rotini", "rotini.CommandHandlers", "*rotini.Context")
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
		"$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-spec.json\ncommand:\n  name: rotini\n  commands:\n    - name: generate\n")

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
			"command:\n  name: app\n  commands:\n    - name: test\n    - name: windows\n    - name: build\n")

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
			"command:\n  name: app\n  commands:\n    - name: build\n      filename: build_handlers.go\n")

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
		"$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-spec.json\ncommand:\n  name: mycli\n  commands:\n    - name: build\n")
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
			"command:\n  name: mycli\n  inputs:\n    flags:\n      - name: a\n        identifiers: [\"-x\"]\n      - name: b\n        identifiers: [\"-x\"]\n")
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
		"$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-spec.json\ncommand:\n  name: mycli\n")
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
		"$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-spec.json\ncommand:\n  name: mycli\n")
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
		"$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-spec.json\ncommand:\n  name: mycli\n")
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
const helpKeepConf = confSchemaHeader + "generate:\n  packages:\n    cmdgen:\n      keep:\n        - embed/legacy_help.txt\n  features:\n    help:\n      enabled: true\n"

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

	helpDir := filepath.Join(tmp, "internal", "cmd", "mycli", "embed")
	orphan := filepath.Join(helpDir, "mycli_obsolete_help.txt")
	legacy := filepath.Join(helpDir, "legacy_help.txt")
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
	if _, err := os.Stat(filepath.Join(helpDir, helpTemplateName)); err != nil {
		t.Errorf("editable help template was removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(helpDir, "mycli_help.txt")); err != nil {
		t.Errorf("current command help page missing: %v", err)
	}
}

// completionConf enables only the completion feature.
const completionConf = confSchemaHeader + "generate:\n  features:\n    completion:\n      enabled: true\n"

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

	compDir := filepath.Join(tmp, "internal", "cmd", "mycli", "embed")
	mustContain(t, filepath.Join(tmp, "internal", "cmd", "mycli", "zz_rotini.gen.go"),
		`_ "embed"`,
		"//go:embed embed/zz_completion_bash.txt", "var CompletionBash string",
		"var CompletionZsh string", "var CompletionFish string",
		"var CompletionPowershell string",
		"func Completion(shell string) (string, error)",
		`case "bash":`,
	)
	// Scripts written per shell, program name substituted.
	mustContain(t, filepath.Join(compDir, "zz_completion_bash.txt"),
		"mycli __complete", "complete -o default -F _mycli_complete mycli")
	for _, sh := range []string{"zz_completion_bash.txt", "zz_completion_zsh.txt", "zz_completion_fish.txt", "zz_completion_powershell.txt"} {
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
		"command:\n" +
		"  name: mycli\n" +
		"  output:\n" +
		"    type: object\n" +
		"    properties:\n" +
		"      count: { type: integer }\n" +
		"      items: { type: array, items: { $ref: \"#/schemas/Widget\" } }\n" +
		"  commands:\n" +
		"    - name: get\n" +
		"      output: { $ref: \"#/schemas/Widget\" }\n" +
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
		"command:\n" +
		"  name: app\n" +
		"  commands:\n" +
		"    - name: compile\n" +
		"      aliases: [build]\n" +
		"      deprecated_identifiers: [build]\n" +
		"      inputs:\n" +
		"        flags:\n" +
		"          - name: config\n" +
		"            identifiers: [--config, --conf]\n" +
		"            deprecated_identifiers: [--conf]\n" +
		"            schema: { type: string }\n"
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

func TestGenerateMapFlag(t *testing.T) {
	tmp := t.TempDir()
	writeTestFile(t, filepath.Join(tmp, "go.mod"), minimalGoMod)
	spec := "$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-spec.json\n" +
		"command:\n" +
		"  name: widget\n" +
		"  inputs:\n" +
		"    flags:\n" +
		"      - name: label\n" +
		"        identifiers: [--label]\n" +
		"        schema: { type: 'map[string]string' }\n" +
		"      - name: meta\n" +
		"        identifiers: [--meta]\n" +
		"        schema: { type: map }\n" // the rotini alias → map[string]any
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
		"command:\n" +
		"  name: widget\n" +
		"  inputs:\n" +
		"    flags:\n" +
		"      - name: since\n" +
		"        identifiers: [--since]\n" +
		"        schema: { type: time.Time, import: time }\n" +
		"      - name: ttl\n" +
		"        identifiers: [--ttl]\n" +
		"        schema: { type: duration }\n" + // rotini alias → auto "time", and dedupes with the above
		"      - name: id\n" +
		"        identifiers: [--id]\n" +
		"        schema: { type: uuid.UUID, import: github.com/google/uuid }\n" +
		"      - name: home\n" +
		"        identifiers: [--home]\n" +
		"        schema: { type: urlx.URL, import: urlx net/url }\n" // aliased import
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
	)
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
		"command:\n" +
		"  name: acme\n" +
		"  remote_discovery:\n" +
		"    path: /opt/acme/plugins\n" +
		"  commands:\n" +
		"    - name: cluster\n" +
		"      remote_discovery:\n" +
		"        prefix: acme-plugin-\n" +
		"        hidden: true\n"
	writeTestFile(t, filepath.Join(tmp, ".rotini.spec.yaml"), spec)

	t.Chdir(tmp)
	if err := Generate(".rotini.spec.yaml", "", false, "", nil); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	rotiniGo := filepath.Join(tmp, "internal", "cmd", "acme", "zz_rotini.gen.go")
	mustContain(t, rotiniGo,
		"RemoteDiscoveryDef{Prefix: \"acme-\"", // root: default prefix <host>-
		"Path: \"/opt/acme/plugins\"",
		"RemoteDiscoveryDef{Prefix: \"acme-plugin-\"", // sub: explicit prefix
		"Hidden: true",
	)
}

// TestGenerateInputChannels verifies the typed input channels (Phase 1, types only):
// pure env/config inputs get their own <Prefix>Env/<Prefix>Config structs (env is NOT
// folded into Flags), stdin gets a typed payload type + a *Stdin field, and
// CommandInputs gains the new fields only when the channel is declared.
func TestGenerateInputChannels(t *testing.T) {
	tmp := t.TempDir()
	writeTestFile(t, filepath.Join(tmp, "go.mod"), minimalGoMod)
	spec := "$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-spec.json\n" +
		"command:\n" +
		"  name: widget\n" +
		"  inputs:\n" +
		"    flags:\n" +
		"      - name: color\n" +
		"        identifiers: [--color]\n" +
		"        schema: { type: string, key: create.color }\n" + // config-fallback flag
		"      - name: quiet\n" +
		"        identifiers: [--quiet]\n" +
		"        schema: { type: bool }\n" + // argv-only flag (no recon tag)

		"    env:\n" +
		"      - name: region\n" +
		"        schema: { type: string, variable: WIDGET_REGION }\n" +
		"    config:\n" +
		"      - name: endpoint\n" +
		"        schema: { type: string, file: app, key: api.endpoint }\n" +
		"      - name: token\n" +
		"        schema: { type: string, key: api.token, secret: true, required: true }\n" +
		"    stdin:\n" +
		"      format: yaml\n" +
		"      schema: { $ref: \"#/schemas/Manifest\" }\n" +
		"configuration_files:\n" +
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
		"`rotini:\"endpoint\" recon:\"api.endpoint\"`",
		"`rotini:\"token\" recon:\"api.token,required,secret\"`",
		// the BindMeta descriptor carries the config-file sources.
		"var BindMeta = rotini.BindMeta{",
		"{Name: \"app\", Path: \"~/.config/widget.yaml\", Format: \"yaml\"}",
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
	"command:\n" +
	"  name: mycli\n" +
	"  summary: my cli\n" +
	"  description: A demo CLI.\n" +
	"  footer: run 'mycli help <command>' for details\n" +
	"  commands:\n" +
	"    - name: build\n" +
	"      aliases: [b]\n" +
	"      summary: build the project\n" +
	"      inputs:\n" +
	"        arguments:\n" +
	"          - name: target\n" +
	"            summary: thing to build\n" +
	"            schema:\n" +
	"              type: string\n" +
	"              required: true\n" +
	"        flags:\n" +
	"          - name: verbose\n" +
	"            summary: chattier output\n" +
	"            identifiers: [-v, --verbose]\n" +
	"            schema:\n" +
	"              type: bool\n"

const helpEnabledConf = confSchemaHeader + "generate:\n  features:\n    help:\n      enabled: true\n"

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
		"//go:embed embed/mycli_help.txt",
		"var HelpMycli string",
		"var HelpMycliBuild string",
		"func Help(path ...string) (string, error)",
		`case "":`,
		`case "build", "b":`,
	)

	// The editable default template was seeded.
	if _, err := os.Stat(filepath.Join(tmp, "internal", "cmd", "mycli", "embed", "help.txt.tmpl")); err != nil {
		t.Errorf("default help template not seeded: %v", err)
	}

	// The root page renders the description, a derived usage line, the commands
	// list (with the alias and summary), and the footer.
	root := filepath.Join(tmp, "internal", "cmd", "mycli", "embed", "mycli_help.txt")
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
	build := filepath.Join(tmp, "internal", "cmd", "mycli", "embed", "mycli_build_help.txt")
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
	build := filepath.Join(tmp, "internal", "cmd", "mycli", "embed", "mycli_build_help.txt")
	writeTestFile(t, build, "hand-written help for build\n")
	if err := Generate(".rotini.spec.yaml", ".rotini.conf.yaml", false, "", nil); err != nil {
		t.Fatalf("Generate (regenerate): %v", err)
	}
	mustContain(t, build, "mycli build <target> [flags]")
	mustNotContain(t, build, "hand-written help for build")
}

// manConf enables man (but not help).
const manConf = confSchemaHeader + "generate:\n  features:\n    man:\n      enabled: true\n"

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
		"//go:embed embed/mycli_man.txt", "var ManMycli string", "var ManMycliBuild string",
		"func Man(path ...string) (string, error)",
		`case "build", "b":`,
	)
	// Help was not enabled — no Help resolver, no help dir.
	mustNotContain(t, genFile, "func Help(")
	if _, err := os.Stat(filepath.Join(tmp, "internal", "cmd", "mycli", "embed", "mycli_help.txt")); !os.IsNotExist(err) {
		t.Errorf("help page should not exist when help is off (err=%v)", err)
	}

	for _, p := range []string{
		"internal/cmd/mycli/embed/man.txt.tmpl", "internal/cmd/mycli/embed/mycli_man.txt", "internal/cmd/mycli/embed/mycli_build_man.txt",
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
			"command:\n"+
			"  name: mycli\n"+
			"  man: |-\n"+
			"    MYCLI(1)\n"+
			"    exact man page\n")

	t.Chdir(tmp)
	if err := Generate(".rotini.spec.yaml", ".rotini.conf.yaml", false, "", nil); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	mustFileEqual(t, filepath.Join(tmp, "internal", "cmd", "mycli", "embed", "mycli_man.txt"), "MYCLI(1)\nexact man page")

	// Nothing renders (root supplies verbatim, no sub-commands) → no template seeded.
	if _, err := os.Stat(filepath.Join(tmp, "internal", "cmd", "mycli", "embed", "man.txt.tmpl")); !os.IsNotExist(err) {
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
			"command:\n"+
			"  name: mycli\n"+
			"  summary: a tool\n"+
			"  exit_status:\n"+
			"    - { code: 0, summary: success }\n"+
			"    - { code: 2, summary: a usage error }\n"+
			"  see_also:\n"+
			"    - mycli-build(1)\n"+
			"    - https://example.com/docs\n")

	t.Chdir(tmp)
	if err := Generate(".rotini.spec.yaml", ".rotini.conf.yaml", false, "", nil); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	manPath := filepath.Join(tmp, "internal", "cmd", "mycli", "embed", "mycli_man.txt")
	mustContain(t, manPath,
		"EXIT STATUS", "0", "success", "2", "a usage error",
		"SEE ALSO", "mycli-build(1), https://example.com/docs",
	)
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
			"command:\n"+
			"  name: mycli\n"+
			"  help: |-\n"+ // strip: no trailing newline
			"    my exact help page\n"+
			"    line two\n")
	t.Chdir(tmp)
	if err := Generate(".rotini.spec.yaml", ".rotini.conf.yaml", false, "", nil); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	// Exactly the supplied bytes — no trailing newline added.
	mustFileEqual(t, filepath.Join(tmp, "internal", "cmd", "mycli", "embed", "mycli_help.txt"), "my exact help page\nline two")

	// Nothing renders (root supplies verbatim help, no sub-commands) → no template.
	if _, err := os.Stat(filepath.Join(tmp, "internal", "cmd", "mycli", "embed", "help.txt.tmpl")); !os.IsNotExist(err) {
		t.Errorf("help.txt.tmpl should not be seeded when no command renders (err=%v)", err)
	}

	// The verbatim string wins; a hand edit is overwritten back to the spec value.
	root := filepath.Join(tmp, "internal", "cmd", "mycli", "embed", "mycli_help.txt")
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
	b.WriteString("$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-spec.json\ncommand:\n  name: mycli\n  commands:\n")
	for _, c := range commands {
		b.WriteString("    - name: " + c + "\n")
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
command:
  name: child
  commands:
    - name: greet
      inputs:
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
command:
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
		"ParentChild() rotini.CommandHandlers",
		"ParentChildGreet() rotini.CommandHandlers",
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
command:
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
		"ParentKid() rotini.CommandHandlers",
		"ParentKidGreet() rotini.CommandHandlers",
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
command:
  name: gc
  commands:
    - name: ping
      inputs:
        arguments:
          - name: host
            schema: { type: string }
`
	childSpec := `$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-spec.json
command:
  name: child
  commands:
    - $ref: ../gc/.rotini.spec.yaml
`
	parentSpec := `$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-spec.json
command:
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
		"ParentChildGc() rotini.CommandHandlers",
		"ParentChildGcPing() rotini.CommandHandlers",
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

func TestGenerate_cyclicRefErrors(t *testing.T) {
	tmp := initTestModule(t)
	// A spec that composes itself — the simplest cycle.
	selfRef := `$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-spec.json
command:
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
command:
  name: app
  inputs:
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
command:
  name: app
  inputs:
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
command:
  name: app
  inputs:
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
command:
  name: app
  inputs:
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
command:
  name: app
  inputs:
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
command:
  name: app
  inputs:
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

// writeFileBytes writes atomically and creates the parent directory: a non-Go output goes
// to a nested missing dir with the exact content, and the atomic temp file is renamed away
// (the target dir holds only the final file, never a torn or leftover temp).
func TestWriteFileBytes_atomicAndMkdir(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rtg", "help", "app.txt") // none of these dirs exist yet

	if err := writeFileBytes(path, "the help page\n"); err != nil {
		t.Fatalf("writeFileBytes: %v", err)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != "the help page\n" {
		t.Fatalf("content = %q, err = %v; want the help page", got, err)
	}
	// Atomic temp-then-rename leaves no residue: only the final file in the target dir.
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatalf("read target dir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "app.txt" {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("target dir = %v, want only [app.txt] (no leftover temp file)", names)
	}

	// Overwriting (re-generate) replaces the content atomically, still no residue.
	if err := writeFileBytes(path, "updated\n"); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if got, _ := os.ReadFile(path); string(got) != "updated\n" {
		t.Errorf("rewrite content = %q, want updated", got)
	}
	if entries, _ := os.ReadDir(filepath.Dir(path)); len(entries) != 1 {
		t.Errorf("after rewrite, target dir has %d entries, want 1", len(entries))
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
			"command:\n"+
			"  name: mycli\n"+
			"  commands:\n"+
			"    - name: secret\n"+
			"      hidden: true\n"+
			"      summary: a hidden command\n"+
			"    - name: legacy\n"+
			"      deprecated: use modern instead\n"+
			"      summary: an old command\n"+
			"    - name: run\n"+
			"      summary: run it\n"+
			"      inputs:\n"+
			"        arguments:\n"+
			"          - name: target\n"+
			"            summary: the target\n"+
			"            deprecated: positional is going away\n"+
			"            schema: { type: string, required: true }\n"+
			"        flags:\n"+
			"          - name: secretflag\n"+
			"            summary: a hidden flag\n"+
			"            hidden: true\n"+
			"            identifiers: [--secret]\n"+
			"            schema: { type: bool }\n"+
			"          - name: verbose\n"+
			"            summary: chatty output\n"+
			"            identifiers: [-v]\n"+
			"            schema: { type: bool }\n")
	writeTestFile(t, filepath.Join(tmp, ".rotini.conf.yaml"), helpEnabledConf)

	t.Chdir(tmp)
	if err := Generate(".rotini.spec.yaml", ".rotini.conf.yaml", false, "", nil); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	root := filepath.Join(tmp, "internal", "cmd", "mycli", "embed", "mycli_help.txt")
	mustContain(t, root, "legacy", "(deprecated: use modern instead)", "an old command")
	mustNotContain(t, root, "secret", "a hidden command")

	run := filepath.Join(tmp, "internal", "cmd", "mycli", "embed", "mycli_run_help.txt")
	mustContain(t, run, "<target>", "(deprecated: positional is going away)", "-v", "chatty output")
	mustNotContain(t, run, "--secret", "a hidden flag")
}

// TestGenerateHelpComposition verifies a $ref-composed child's commands render
// through the COMPOSING parent's help template using the child's spec content,
// so a merged binary has one consistent help style.
func TestGenerateHelpComposition(t *testing.T) {
	tmp := initTestModule(t)

	childSpec := "$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-spec.json\n" +
		"command:\n" +
		"  name: child\n" +
		"  summary: the child program\n" +
		"  description: A composed child.\n" +
		"  commands:\n" +
		"    - name: greet\n" +
		"      summary: say hello\n"
	helpConf := func(dir string) string {
		return "$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-conf.json\n" +
			"generate:\n" +
			"  packages:\n" +
			"    cmd: { package: cmd/" + dir + "/rth, file: handlers.go }\n" +
			"    cmdgen: { package: cmd/" + dir + "/rtg, file: rotini.go }\n" +
			"  features: { help: { enabled: true } }\n"
	}
	parentSpec := "$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-spec.json\n" +
		"command:\n" +
		"  name: parent\n" +
		"  commands:\n" +
		"    - $ref: ../child/.rotini.spec.yaml\n"

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
	mustContain(t, filepath.Join(tmp, "cmd/parent/rtg/embed/parent_help.txt"),
		"child", "the child program")
	// The composed child's own page (rendered by the parent) carries the child's
	// content, including its sub-command.
	mustContain(t, filepath.Join(tmp, "cmd/parent/rtg/embed/parent_child_help.txt"),
		"A composed child.", "greet", "say hello")
	// And the grandchild command page exists with its content.
	mustContain(t, filepath.Join(tmp, "cmd/parent/rtg/embed/parent_child_greet_help.txt"),
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
		writeTestFile(t, filepath.Join(tmp, "internal", "cmd", "app", "embed", "help.txt.tmpl"), preTmpl)
	}
	t.Chdir(tmp)
	if err := Generate(".rotini.spec.yaml", ".rotini.conf.yaml", false, "", nil); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	return filepath.Join(tmp, "internal", "cmd", "app", "embed")
}

// helpGoldenGeneratedSpec exercises the full generated-help surface: root header/
// description/usage-override/custom-heading/examples/footer; a rich sub-command with
// required/optional/variadic args and flags with type/default/enum/required/
// deprecated/hidden; a deprecated command; and a hidden command.
const helpGoldenGeneratedSpec = goldenSpecSchema +
	`command:
  name: app
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
      inputs:
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
		assertHelpGolden(t, goldenDir, "generated_"+f+".txt", readFileString(t, filepath.Join(helpDir, f+"_help.txt")))
	}
}

const helpGoldenCustomSpec = goldenSpecSchema +
	`command:
  name: app
  summary: my app
  footer: bye now
  commands:
    - name: run
      summary: run it
      inputs:
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
	assertHelpGolden(t, goldenDir, "custom_app_run.txt", readFileString(t, filepath.Join(helpDir, "app_run_help.txt")))
	// The user's template survives generation untouched.
	mustFileEqual(t, filepath.Join(helpDir, "help.txt.tmpl"), helpGoldenCustomTmpl)
}

const helpGoldenVerbatimSpec = goldenSpecSchema +
	`command:
  name: app
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
	assertHelpGolden(t, goldenDir, "verbatim_app.txt", readFileString(t, filepath.Join(helpDir, "app_help.txt")))
	assertHelpGolden(t, goldenDir, "verbatim_app_run.txt", readFileString(t, filepath.Join(helpDir, "app_run_help.txt")))
	if _, err := os.Stat(filepath.Join(helpDir, "help.txt.tmpl")); !os.IsNotExist(err) {
		t.Errorf("template should not be seeded when every command is verbatim (err=%v)", err)
	}
}

// helpGoldenCascadingSpec: a root with one cascading flag (--verbose) and one
// non-cascading flag (--root-only), plus two children. 'run' uses the default
// cascading heading; 'deploy' overrides it with a colon-free value.
const helpGoldenCascadingSpec = goldenSpecSchema +
	`command:
  name: app
  summary: the app
  inputs:
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
      inputs:
        flags:
          - name: jobs
            summary: parallelism
            identifiers: [-j, --jobs]
            schema: { type: int }
    - name: deploy
      summary: deploy it
      headings:
        cascading: Inherited Flags
      inputs:
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
		assertHelpGolden(t, goldenDir, "cascading_"+f+".txt", readFileString(t, filepath.Join(helpDir, f+"_help.txt")))
	}
}

// helpGoldenEnvConfigSpec exercises the Environment + Configuration help sections: an
// env input with an explicit variable and one whose variable is derived (snake-upper),
// and config inputs with and without an explicit file/key location.
const helpGoldenEnvConfigSpec = goldenSpecSchema +
	`command:
  name: app
  summary: the app
  inputs:
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
`

// TestHelpGolden_EnvConfig locks the rendering of env-var and config inputs in the
// generated help page (explicit vs derived env var, located vs bare config key).
func TestHelpGolden_EnvConfig(t *testing.T) {
	goldenDir := helpGoldenDir(t)
	helpDir := genHelp(t, helpGoldenEnvConfigSpec, "")
	assertHelpGolden(t, goldenDir, "envconfig_app.txt", readFileString(t, filepath.Join(helpDir, "app_help.txt")))
}

// helpGoldenGroupsSpec exercises command grouping: an ungrouped command (default
// heading), then two named groups, with groups appearing in first-declaration order.
const helpGoldenGroupsSpec = goldenSpecSchema +
	`command:
  name: app
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
	assertHelpGolden(t, goldenDir, "groups_app.txt", readFileString(t, filepath.Join(helpDir, "app_help.txt")))
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
