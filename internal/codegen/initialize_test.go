package codegen

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// TestInitialize_endToEnd is the guard the `rotini init` breakage slipped past: every
// other test stops at "the files were written and validate", which a scaffold that
// cannot COMPILE still passes. This one runs the real initialize into a fresh module
// and then builds the result, so a bad seed conf, a stale template, a missing require,
// or a wrong runtime import path fails here rather than in a new user's terminal.
//
// It shells out to `go build`, so it is the slowest test in the package — but it covers
// the one path every single user walks first.
func TestInitialize_endToEnd(t *testing.T) {
	skipUnlessCompiling(t)

	// The scaffold imports github.com/go-rotini/rotini, which is THIS repo — two levels
	// up from internal/codegen. Resolve it before chdir'ing into the temp module.
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	writeTestFile(t, dir, "go.mod", "module example.com/demo\n\ngo 1.26\n\nrequire github.com/go-rotini/rotini v0.0.0\n\nreplace github.com/go-rotini/rotini => "+filepath.ToSlash(repoRoot)+"\n")
	t.Chdir(dir)

	if err := NewProcessor("0.0.0").Initialize("demo", "", false); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	// The scaffold's shape: the seed files, the entrypoint, the editable stub, and the
	// generated framework file. A missing one means a codegen step silently no-op'd.
	for _, want := range []string{
		"cmd/demo/.rotini.spec.yaml",
		"cmd/demo/.rotini.conf.yaml",
		"cmd/demo/main.go",
		"internal/cmd/demo/demo.go",
		"internal/cmd/demo/zz_rotini.go",
	} {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(want))); err != nil {
			t.Errorf("scaffold missing %s: %v", want, err)
		}
	}

	// The runtime is IMPORTED, never emitted — no copy of it may appear in the module.
	for _, gone := range []string{"internal/cmd/demo/rotini", "internal/demo"} {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(gone))); err == nil {
			t.Errorf("%s exists — the runtime must be imported, not emitted", gone)
		}
	}

	// `go mod tidy` resolves the runtime's own requirements (recon/fs/…) from the module
	// cache; the build is the real assertion.
	for _, args := range [][]string{{"mod", "tidy"}, {"build", "./..."}} {
		cmd := exec.Command("go", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("go %s in the scaffolded module: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
}

// TestDocumentedSeedSizeMatchesReality keeps the size claim on the home page and the generated
// page true — the same failure "29 lint rules" had, in a different sentence.
//
// The number is load-bearing: "rotini generates N lines" is the claim a reader weighs the
// codegen step against, so a stale one is not a typo, it is a misrepresentation. It is also
// exactly the kind of fact nobody re-measures by hand.
func TestDocumentedSeedSizeMatchesReality(t *testing.T) {
	skipUnlessCompiling(t)
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	writeTestFile(t, dir, "go.mod", "module example.com/todo\n\ngo 1.26\n")
	t.Chdir(dir)
	if err := NewProcessor("0.0.0").Initialize("todo", "", false); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	files, lines := 0, 0
	err = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".go") {
			return err
		}
		body, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		files++
		lines += strings.Count(string(body), "\n")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// A claim of the shape "N lines across M files".
	//
	// The document is NORMALIZED first — emphasis characters removed, whitespace collapsed —
	// so the pattern does not have to anticipate where someone put the asterisks or the line
	// break. The first version did, and README.md's "**108 lines**\nacross three files" slipped
	// through it while the test passed: a guard that only catches the phrasing you thought of
	// is not a guard.
	claim := regexp.MustCompile(`(\d+) lines across (\d+|one|two|three|four|five|six|seven|eight|nine|ten) files`)
	spelled := map[string]int{
		"one": 1, "two": 2, "three": 3, "four": 4, "five": 5,
		"six": 6, "seven": 7, "eight": 8, "nine": 9, "ten": 10,
	}
	normalize := func(body []byte) string {
		return strings.Join(strings.Fields(strings.ReplaceAll(string(body), "*", "")), " ")
	}

	checked := 0
	for _, path := range publishedMarkdown(t, repoRoot) {
		body, err := os.ReadFile(path)
		if err != nil {
			continue // publishedMarkdown's own test reports a missing file
		}
		for _, m := range claim.FindAllStringSubmatch(normalize(body), -1) {
			wantLines, _ := strconv.Atoi(m[1])
			wantFiles, err := strconv.Atoi(m[2])
			if err != nil {
				wantFiles = spelled[strings.ToLower(m[2])]
			}
			checked++
			rel, _ := filepath.Rel(repoRoot, path)
			if wantLines != lines || wantFiles != files {
				t.Errorf("%s says %d lines across %d files; `rotini init` writes %d across %d",
					rel, wantLines, wantFiles, lines, files)
			}
		}
	}
	if checked == 0 {
		t.Error("no document quotes the seed's size — if that is deliberate, delete this test")
	}
}
