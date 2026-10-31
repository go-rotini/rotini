package codegen

import (
	"cmp"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// fakeGeneratorEnv, when set, makes the test binary act as a generator (see TestMain). Its value
// is a directory: the binary saves its stdin there as stdin.got, then writes the directory's
// stdout and stderr files to its own and exits with the number in its exit file (0 without one).
const fakeGeneratorEnv = "ROTINI_TEST_FAKE_GENERATOR"

func fakeGenerator() int {
	dir := os.Getenv(fakeGeneratorEnv)
	in, _ := io.ReadAll(os.Stdin)
	_ = os.WriteFile(filepath.Join(dir, "stdin.got"), in, 0o644)
	if b, err := os.ReadFile(filepath.Join(dir, "stdout")); err == nil {
		os.Stdout.Write(b)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "stderr")); err == nil {
		os.Stderr.Write(b)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "exit")); err == nil {
		if n, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil {
			return n
		}
	}
	return 0
}

// fakeGeneratorModule writes a module whose conf runs the test binary as generator "docs"
// writing under docs/gen, and returns the module dir and the fake generator's control dir.
func fakeGeneratorModule(t *testing.T, keep string) (mod, ctl string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	mod, ctl = t.TempDir(), t.TempDir()
	t.Setenv(fakeGeneratorEnv, ctl)
	run, _ := json.Marshal([]string{exe})
	conf := "version: 0.0.0\ngenerate:\n  packages:\n    - type: cmd\n      file: internal/cmd/demo/zz_demo.go\n      package: demo\n" +
		"  generators:\n    - name: docs\n      run: " + string(run) + "\n      dir: docs/gen\n" + keep
	writeTestFile(t, mod, "go.mod", "module example.com/demo\n\ngo 1.26\n")
	writeTestFile(t, mod, ".rotini.spec.yaml", "version: 0.0.0\ncommand:\n  name: demo\n  commands:\n    - name: add\n      summary: add one\n")
	writeTestFile(t, mod, ".rotini.conf.yaml", conf)
	t.Chdir(mod)
	return mod, ctl
}

// generatorDoc renders a generator document returning files (path → content).
func generatorDoc(t *testing.T, files map[string]string) string {
	t.Helper()
	doc := map[string]any{"rotini_generator": 1, "files": []map[string]string{}}
	var list []map[string]string
	for _, p := range slices.Sorted(func(yield func(string) bool) {
		for k := range files {
			if !yield(k) {
				return
			}
		}
	}) {
		list = append(list, map[string]string{"path": p, "content": files[p]})
	}
	if list != nil {
		doc["files"] = list
	}
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// dryRunChanges runs a dry generate in the current module and returns its planned changes.
func dryRunChanges(t *testing.T) []string {
	t.Helper()
	planned, err := NewProcessor("0.0.0").GenerateDryRun(".rotini.spec.yaml", ".rotini.conf.yaml", nil)
	if err != nil {
		t.Fatalf("GenerateDryRun: %v", err)
	}
	return planned.Changes
}

// TestGeneratorFiles checks a generator's whole cycle: it receives the contract on stdin, its
// files and the manifest are written, a regenerate changes nothing, a dry run lists an update
// and a removal without making them, and a real run then removes the file it stopped
// returning while a file it never returned, and a kept one, stay.
func TestGeneratorFiles(t *testing.T) {
	mod, ctl := fakeGeneratorModule(t, "      keep: [kept.md]\n")
	gen := filepath.Join(mod, "docs", "gen")
	writeTestFile(t, ctl, "stdout", generatorDoc(t, map[string]string{"demo.md": "# demo\n", "cmd/demo-add.md": "# add\n", "kept.md": "kept\n"}))
	writeTestFile(t, gen, "notes.md", "mine\n")
	runGenerate(t, mod)

	contract, err := os.ReadFile(filepath.Join(ctl, "stdin.got"))
	if err != nil {
		t.Fatal(err)
	}
	gp := resolveInModule(t, mod)
	gp.module.root = mod
	want, err := gp.contract(gp.contractNodes())
	if err != nil {
		t.Fatal(err)
	}
	if string(contract) != string(want) {
		t.Errorf("the generator's stdin isn't the contract document:\n%s", contract)
	}
	if got := readTestFile(t, filepath.Join(gen, "cmd", "demo-add.md")); got != "# add\n" {
		t.Errorf("cmd/demo-add.md = %q", got)
	}
	wantManifest := generatorManifestHeader + `"docs", and removes one it stops writing. Do not edit.` + "\ncmd/demo-add.md\ndemo.md\nkept.md\n"
	if got := readTestFile(t, filepath.Join(gen, generatorManifest)); got != wantManifest {
		t.Errorf("manifest =\n%s\nwant\n%s", got, wantManifest)
	}
	if changes := dryRunChanges(t); len(changes) != 0 {
		t.Errorf("a regenerate would change %q; want nothing", changes)
	}

	writeTestFile(t, ctl, "stdout", generatorDoc(t, map[string]string{"demo.md": "# demo, again\n"}))
	changes := strings.Join(dryRunChanges(t), "\n")
	for _, want := range []string{"update docs/gen/demo.md", "delete docs/gen/cmd/demo-add.md", "update docs/gen/" + generatorManifest} {
		if !strings.Contains(changes, want) {
			t.Errorf("dry run changes lack %q:\n%s", want, changes)
		}
	}
	if strings.Contains(changes, "kept.md") || strings.Contains(changes, "notes.md") {
		t.Errorf("dry run touches a kept or hand-written file:\n%s", changes)
	}
	if _, err := os.Stat(filepath.Join(gen, "cmd", "demo-add.md")); err != nil {
		t.Errorf("the dry run removed a file: %v", err)
	}

	runGenerate(t, mod)
	if _, err := os.Stat(filepath.Join(gen, "cmd", "demo-add.md")); !os.IsNotExist(err) {
		t.Errorf("cmd/demo-add.md, no longer returned, is still there: %v", err)
	}
	for _, name := range []string{"kept.md", "notes.md"} {
		if _, err := os.Stat(filepath.Join(gen, name)); err != nil {
			t.Errorf("%s was removed: %v", name, err)
		}
	}
	wantManifest = generatorManifestHeader + `"docs", and removes one it stops writing. Do not edit.` + "\ndemo.md\n"
	if got := readTestFile(t, filepath.Join(gen, generatorManifest)); got != wantManifest {
		t.Errorf("manifest =\n%s\nwant\n%s", got, wantManifest)
	}
}

// TestGeneratorFailures checks that a generator that exits non-zero, or returns a document
// rotini can't use, fails generate with a message naming the generator and the cause, before
// anything is written.
func TestGeneratorFailures(t *testing.T) {
	tests := []struct {
		name, stdout, stderr, exit string
		want                       []string
	}{
		{name: "exit", stderr: "boom: no template\n", exit: "3", want: []string{`generator "docs": run `, "exit status 3", "boom: no template"}},
		{name: "empty", want: []string{`generator "docs": it wrote nothing to stdout`}},
		{name: "escape", stdout: `{"rotini_generator": 1, "files": [{"path": "../x.md", "content": ""}]}`, want: []string{`file path "../x.md" must stay inside the generator's dir`}},
		{name: "version", stdout: `{"rotini_generator": 2, "files": []}`, want: []string{`"rotini_generator": 2, and this rotini reads version 1`}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mod, ctl := fakeGeneratorModule(t, "")
			writeTestFile(t, ctl, "stdout", tt.stdout)
			if tt.stderr != "" {
				writeTestFile(t, ctl, "stderr", tt.stderr)
			}
			if tt.exit != "" {
				writeTestFile(t, ctl, "exit", tt.exit)
			}
			var reported error
			if err := NewProcessor("0.0.0").Generate(".rotini.spec.yaml", ".rotini.conf.yaml", false, func(_ string, e error) { reported = cmp.Or(reported, e) }, nil); err != nil {
				reported = err
			}
			if reported == nil {
				t.Fatal("generate succeeded; want an error")
			}
			for _, w := range tt.want {
				if !strings.Contains(reported.Error(), w) {
					t.Errorf("error %q lacks %q", reported, w)
				}
			}
			if _, err := os.Stat(filepath.Join(mod, "docs", "gen")); !os.IsNotExist(err) {
				t.Errorf("a failed generator left docs/gen: %v", err)
			}
			if _, err := os.Stat(filepath.Join(mod, "internal")); !os.IsNotExist(err) {
				t.Errorf("generate wrote the cmd package although a generator failed: %v", err)
			}
		})
	}
}

// TestGeneratorStderrIsANotice checks that what a successful generator writes to stderr is
// reported as a notice, naming the generator.
func TestGeneratorStderrIsANotice(t *testing.T) {
	_, ctl := fakeGeneratorModule(t, "")
	writeTestFile(t, ctl, "stdout", generatorDoc(t, nil))
	writeTestFile(t, ctl, "stderr", "skipped a hidden command\n")
	var notices []error
	if err := NewProcessor("0.0.0").Generate(".rotini.spec.yaml", ".rotini.conf.yaml", false, func(_ string, e error) {
		if e != nil {
			t.Errorf("generate: %v", e)
		}
	}, func(n []error) { notices = n }); err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(notices, func(e error) bool { return e.Error() == `generator "docs": skipped a hidden command` }) {
		t.Errorf("notices %v lack the generator's stderr", notices)
	}
}

func TestParseGeneratorOutput(t *testing.T) {
	tests := []struct {
		name, stdout, want string // want: "" for success, else part of the error
	}{
		{name: "ok", stdout: `{"rotini_generator": 1, "files": [{"path": "b.md", "content": "b"}, {"path": "a/c.md", "content": ""}]}`},
		{name: "no files", stdout: `{"rotini_generator": 1}`},
		{name: "empty", stdout: ``, want: "wrote nothing to stdout"},
		{name: "not json", stdout: `-- a.md --`, want: "isn't a generator document"},
		{name: "unknown field", stdout: `{"rotini_generator": 1, "files": [], "extra": true}`, want: `unknown field "extra"`},
		{name: "unknown file field", stdout: `{"rotini_generator": 1, "files": [{"path": "a", "content": "", "mode": 493}]}`, want: `unknown field "mode"`},
		{name: "two documents", stdout: `{"rotini_generator": 1} {"rotini_generator": 1}`, want: "more than one JSON document"},
		{name: "no version", stdout: `{"files": []}`, want: `no "rotini_generator" version`},
		{name: "no content", stdout: `{"rotini_generator": 1, "files": [{"path": "a.md"}]}`, want: `file "a.md" has no "content"`},
		{name: "twice", stdout: `{"rotini_generator": 1, "files": [{"path": "a.md", "content": ""}, {"path": "a.md", "content": ""}]}`, want: `"a.md" is listed twice`},
		{name: "case", stdout: `{"rotini_generator": 1, "files": [{"path": "A.md", "content": ""}, {"path": "a.md", "content": ""}]}`, want: "differ only in case"},
		{name: "file and dir", stdout: `{"rotini_generator": 1, "files": [{"path": "a", "content": ""}, {"path": "a/b.md", "content": ""}]}`, want: `"a/b.md" is inside "a", which is also a file`},
		{name: "bad path", stdout: `{"rotini_generator": 1, "files": [{"path": "/etc/x", "content": ""}]}`, want: "not absolute"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			files, err := parseGeneratorOutput([]byte(tt.stdout))
			switch {
			case tt.want == "" && err != nil:
				t.Fatalf("error %v", err)
			case tt.want != "" && (err == nil || !strings.Contains(err.Error(), tt.want)):
				t.Fatalf("error %v, want one containing %q", err, tt.want)
			}
			if tt.name == "ok" && (len(files) != 2 || files[0].Path != "a/c.md") {
				t.Errorf("files = %+v, want two sorted by path", files)
			}
		})
	}
}

func TestGeneratorPathProblem(t *testing.T) {
	ok := []string{"a.md", "commands/todo-add.md", ".hidden", "a..b"}
	bad := []string{"", ".", "..", "../a", "a/../b", "./a", "a//b", "a/", "/a", `a\b`, "c:a", "a\x00", generatorManifest, strings.ToUpper(generatorManifest)}
	for _, p := range ok {
		if why := generatorPathProblem(p); why != "" {
			t.Errorf("%q: %s; want accepted", p, why)
		}
	}
	for _, p := range bad {
		if generatorPathProblem(p) == "" {
			t.Errorf("%q accepted; want a problem", p)
		}
	}
}

// TestGeneratorManifestIgnoresUnsafeLines checks that a manifest line naming a path outside the
// generator's dir is never a removal candidate, and a file without the header records nothing.
func TestGeneratorManifestIgnoresUnsafeLines(t *testing.T) {
	dir := t.TempDir()
	p := &program{plan: newPlanner(true)}
	writeTestFile(t, dir, generatorManifest, generatorManifestHeader+"\"x\"\nok.md\n../../etc/passwd\n/abs\n# note\n")
	got, err := p.readGeneratorManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(got) != "[ok.md]" {
		t.Errorf("paths = %q, want [ok.md]", got)
	}
	writeTestFile(t, dir, generatorManifest, "ok.md\n")
	if got, _ := p.readGeneratorManifest(dir); len(got) != 0 {
		t.Errorf("a file without the header listed %q", got)
	}
}
