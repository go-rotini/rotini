package internal

import (
	"os"
	"path/filepath"
	"testing"
)

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

// TestHelpFuncMap asserts the template helper set is exactly the documented,
// deterministic allowlist — no clock/entropy functions (which would break
// byte-stable rendering) and no third-party dependency.
func TestHelpFuncMap(t *testing.T) {
	fm := helpFuncMap()
	want := []string{
		"join", "upper", "lower", "title", "trim", "trimPrefix", "trimSuffix",
		"replace", "indent", "repeat", "default", "contains", "hasPrefix",
		"hasSuffix", "first", "last",
	}
	if len(fm) != len(want) {
		t.Errorf("helpFuncMap has %d funcs, want %d", len(fm), len(want))
	}
	for _, k := range want {
		if _, ok := fm[k]; !ok {
			t.Errorf("helpFuncMap missing %q", k)
		}
	}
	for _, bad := range []string{"now", "date", "uuidv4", "randAlpha", "randNumeric", "randBytes"} {
		if _, ok := fm[bad]; ok {
			t.Errorf("helpFuncMap unexpectedly exposes non-deterministic %q", bad)
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

	root := filepath.Join(tmp, "cmd", "mycli", "cli", "embed", "help", "mycli.txt")
	mustContain(t, root, "legacy", "(deprecated: use modern instead)", "an old command")
	mustNotContain(t, root, "secret", "a hidden command")

	run := filepath.Join(tmp, "cmd", "mycli", "cli", "embed", "help", "mycli_run.txt")
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
			"    cli: { package: cmd/" + dir + "/rth, file: handlers.go }\n" +
			"    cligen: { package: cmd/" + dir + "/rtg, file: rotini.go }\n" +
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
	mustContain(t, filepath.Join(tmp, "cmd/parent/rtg/embed/help/parent.txt"),
		"child", "the child program")
	// The composed child's own page (rendered by the parent) carries the child's
	// content, including its sub-command.
	mustContain(t, filepath.Join(tmp, "cmd/parent/rtg/embed/help/parent_child.txt"),
		"A composed child.", "greet", "say hello")
	// And the grandchild command page exists with its content.
	mustContain(t, filepath.Join(tmp, "cmd/parent/rtg/embed/help/parent_child_greet.txt"),
		"parent child greet")
}
