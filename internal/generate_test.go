package internal

import (
	"bytes"
	"go/format"
	"os"
	"path/filepath"
	"testing"
)

// companionConf points generation at the same package layout as the committed
// rotini companion CLI so the output can be compared against it.
const companionConf = `$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-conf.json
generate:
  cmd:
    package: cmd/rotini/rth
    gen_file: handlers.go
  framework:
    package: cmd/rotini/rtg
    gen_file: rotini.go
  help:
    enabled: true
`

// minimalGoMod uses the same module path the committed handlers rollup imports,
// so the generated framework import path matches the golden file byte-for-byte.
const minimalGoMod = "module github.com/go-rotini/rotini\n\ngo 1.26\n"

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
	if err := Generate(specPath, confPath); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	// The always-(re)generated files are reproduced byte-for-byte.
	for _, rel := range []string{
		"cmd/rotini/rtg/rotini.go",
		"cmd/rotini/rth/handlers.go",
	} {
		assertGoEqual(t, filepath.Join(tmp, rel), filepath.Join(repoRoot, rel))
	}

	// Handler stubs are create-if-missing user code, so the committed copies are
	// edited (wired to internal funcs) and intentionally diverge from a fresh
	// stub. Assert the generator produced each with the expected stub shape.
	for _, name := range []string{
		"rotini", "rotini_completion", "rotini_generate",
		"rotini_help", "rotini_initialize", "rotini_validate", "rotini_version",
	} {
		mustContain(t, filepath.Join(tmp, "cmd/rotini/rth", name+".go"),
			"package rth", "rotini.CommandHandlers", "*rotini.Context")
	}
}

// TestGenerateDefaultLayout verifies that, with no conf alongside the spec and
// none supplied, the sane defaults place the framework file at rtg/rotini.go
// and the rollup at rth/handlers.go (relative to the module root).
func TestGenerateDefaultLayout(t *testing.T) {
	tmp := t.TempDir()
	writeTestFile(t, filepath.Join(tmp, "go.mod"), minimalGoMod)
	// A spec with NO adjacent conf, so generation falls back to the defaults.
	writeTestFile(t, filepath.Join(tmp, ".rotini.spec.yaml"),
		"$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-spec.json\nname: rotini\ncommands:\n  - name: generate\n")

	t.Chdir(tmp)
	if err := Generate(".rotini.spec.yaml", ""); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	mustContain(t, filepath.Join(tmp, "rtg", "rotini.go"), "package rtg", "type ProgramHandlers interface")
	mustContain(t, filepath.Join(tmp, "rth", "handlers.go"), "package rth", "var Program = rotini.NewProgram(rtg.Definition, &handlers{})")
	mustContain(t, filepath.Join(tmp, "rth", "rotini_generate.go"), "type rotiniGenerateHandlers struct{}")
}

// TestGeneratePrunesOrphanStubs verifies that, with prune enabled, a stub that
// no longer maps to a command is removed while kept files survive and existing
// command stubs are left untouched.
func TestGeneratePrunesOrphanStubs(t *testing.T) {
	repoRoot := repoRoot(t)
	specPath := filepath.Join(repoRoot, "cmd", "rotini", ".rotini.spec.yaml")

	tmp := t.TempDir()
	writeTestFile(t, filepath.Join(tmp, "go.mod"), minimalGoMod)
	handlersDir := filepath.Join(tmp, "rth")
	orphan := filepath.Join(handlersDir, "rotini_obsolete.go")
	keep := filepath.Join(handlersDir, "help.go")
	writeTestFile(t, orphan, "package rth\n")
	writeTestFile(t, keep, "package rth\n")

	conf := "generate:\n  cmd:\n    prune:\n      enabled: true\n      keep:\n        - help.go\n"
	confPath := filepath.Join(tmp, ".rotini.conf.yaml")
	writeTestFile(t, confPath, conf)

	t.Chdir(tmp)
	if err := Generate(specPath, confPath); err != nil {
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

// TestGenerateHelpEnabled verifies that, with generate.help enabled, the
// framework file gains the embedded Help<Prefix> vars + an alias-aware Help
// resolver, and that the rendered help .txt payloads are written with the
// expected (authored + auto-derived) sections.
func TestGenerateHelpEnabled(t *testing.T) {
	tmp := t.TempDir()
	writeTestFile(t, filepath.Join(tmp, "go.mod"), minimalGoMod)
	writeTestFile(t, filepath.Join(tmp, ".rotini.spec.yaml"),
		"$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-spec.json\n"+
			"name: mycli\n"+
			"short_description: My CLI.\n"+
			"long_description: My CLI does things.\n"+
			"homepage: https://mycli.example\n"+
			"commands:\n"+
			"  - name: build\n"+
			"    short_description: Build it.\n"+
			"    aliases: [b]\n"+
			"    examples:\n"+
			"      - mycli build ./x\n"+
			"    inputs:\n"+
			"      arguments:\n"+
			"        - name: target\n"+
			"          schema:\n"+
			"            type: string\n"+
			"            required: true\n"+
			"            description: thing to build\n")
	writeTestFile(t, filepath.Join(tmp, ".rotini.conf.yaml"),
		"generate:\n  help:\n    enabled: true\n")

	t.Chdir(tmp)
	if err := Generate(".rotini.spec.yaml", ".rotini.conf.yaml"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	mustContain(t, filepath.Join(tmp, "rtg", "rotini.go"),
		`_ "embed"`,
		"//go:embed help/mycli.txt",
		"var HelpMycli string",
		"var HelpMycliBuild string",
		"func Help(path ...string) (string, error)",
		`case "":`,
		`case "build", "b":`,
	)
	mustContain(t, filepath.Join(tmp, "rtg", "help", "mycli.txt"),
		"My CLI does things.",
		"Find more information at: https://mycli.example",
		"Usage:",
		"Commands:",
		"build;b",
		`Use "mycli help <command>" for more information about a command.`,
	)
	mustContain(t, filepath.Join(tmp, "rtg", "help", "mycli_build.txt"),
		"Build it.",
		"mycli build <target>", // auto-derived usage: required arg
		"Arguments:",
		"thing to build",
		"Examples:",
		"mycli build ./x",
	)

	// Regenerating produces identical output (idempotent).
	before := readAndFormat(t, filepath.Join(tmp, "rtg", "rotini.go"))
	if err := Generate(".rotini.spec.yaml", ".rotini.conf.yaml"); err != nil {
		t.Fatalf("Generate (second pass): %v", err)
	}
	after := readAndFormat(t, filepath.Join(tmp, "rtg", "rotini.go"))
	if !bytes.Equal(before, after) {
		t.Errorf("help generation is not idempotent:\n--- before ---\n%s\n--- after ---\n%s", before, after)
	}
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
