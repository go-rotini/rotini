package codegen

import (
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"
)

// Lint messages are user-facing output, so each rule's exact report is pinned by a fixture:
//
//	internal/codegen/testdata/lint/<ruleName>/
//	    spec.yaml     the documents (conf.yaml is optional; a minimal valid one is supplied)
//	    conf.yaml
//	    want.txt      every problem reported, verbatim
//
// When -update-lint rewrites a want.txt, review the message itself: it should name the
// subject, its position, and the fix.

// updateLintFixtures rewrites every want.txt:
// `go test ./internal/codegen -run LintFixtures -update-lint`.
var updateLintFixtures = flag.Bool("update-lint", false, "rewrite the lint fixture want.txt files")

const lintFixtureDir = "testdata/lint"

// lintFixtureConf is the minimal valid conf used when a fixture supplies no conf.yaml.
const lintFixtureConf = `version: 0.0.0
generate:
  packages:
    - type: cmd
      file: internal/cmd/demo/zz_demo.go
      package: demo
`

// ruleNames returns the registered lint rules' function names, which are also their fixture
// directory names.
func ruleNames() []string {
	var names []string
	add := func(fn any) {
		full := runtime.FuncForPC(reflect.ValueOf(fn).Pointer()).Name()
		names = append(names, full[strings.LastIndex(full, ".")+1:])
	}
	for _, fn := range specLints {
		add(fn)
	}
	for _, fn := range crossLints {
		add(fn)
	}
	for _, fn := range confLints {
		add(fn)
	}
	sort.Strings(names)
	return names
}

// TestLintFixturesComplete pins a one-to-one match between registered rules and fixture
// directories.
func TestLintFixturesComplete(t *testing.T) {
	entries, err := os.ReadDir(lintFixtureDir)
	if err != nil {
		t.Fatalf("read %s (run with -update-lint to create fixtures): %v", lintFixtureDir, err)
	}
	have := map[string]bool{}
	for _, e := range entries {
		if e.IsDir() {
			have[e.Name()] = true
		}
	}

	for _, rule := range ruleNames() {
		if !have[rule] {
			t.Errorf("lint rule %s has no fixture — add %s/%s/{spec.yaml,want.txt}",
				rule, lintFixtureDir, rule)
		}
		delete(have, rule)
	}
	for dir := range have {
		t.Errorf("fixture directory %s/%s names no registered lint rule (renamed or removed?)",
			lintFixtureDir, dir)
	}
}

// TestLintFixtures runs each fixture through the real `rotini validate` workflow and compares
// the whole report, byte for byte, with the recorded one.
func TestLintFixtures(t *testing.T) {
	entries, err := os.ReadDir(lintFixtureDir)
	if err != nil {
		t.Fatalf("read %s: %v", lintFixtureDir, err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		t.Run(e.Name(), func(t *testing.T) { runLintFixture(t, e.Name()) })
	}
}

func runLintFixture(t *testing.T, rule string) {
	t.Helper()
	repoDir, err := os.Getwd() // t.Chdir below moves us into the fixture's module
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(lintFixtureDir, rule)

	// A fixture is a small module: spec.yaml and conf.yaml become the validated documents,
	// and any other file (a composed child spec, a pinned config file) is copied alongside.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("fixture %s: %v", rule, err)
	}
	mod := t.TempDir()
	writeTestFile(t, mod, "go.mod", "module example.com/demo\n\ngo 1.26\n")

	var haveSpec, haveConf bool
	for _, e := range entries {
		if e.IsDir() || e.Name() == "want.txt" {
			continue
		}
		body, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		switch e.Name() {
		case "spec.yaml":
			haveSpec = true
			writeTestFile(t, mod, ".rotini.spec.yaml", string(body))
		case "conf.yaml":
			haveConf = true
			writeTestFile(t, mod, ".rotini.conf.yaml", string(body))
		default:
			writeTestFile(t, mod, e.Name(), string(body))
		}
	}
	if !haveSpec {
		t.Fatalf("fixture %s has no spec.yaml", rule)
	}
	if !haveConf {
		writeTestFile(t, mod, ".rotini.conf.yaml", lintFixtureConf)
	}

	t.Chdir(mod)

	// "collect" records the whole report, so a fixture tripping extra rules shows in the diff.
	var warnings []error
	failure := NewProcessor("0.0.0").Validate(".rotini.spec.yaml", ".rotini.conf.yaml", false, "collect", "",
		func(string, error) {},
		func(w []error) { warnings = append(warnings, w...) })
	got := lintReport(failure, warnings)

	goldenPath := filepath.Join(repoDir, dir, "want.txt")
	if *updateLintFixtures {
		if err := os.WriteFile(goldenPath, []byte(got), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Logf("recorded %s — READ IT: does the message name the thing, the place, and the fix?", goldenPath)
		return
	}

	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("fixture %s has no want.txt (run with -update-lint): %v", rule, err)
	}
	if got != string(want) {
		t.Errorf("fixture %s: report changed.\n--- want ---\n%s\n--- got ---\n%s", rule, want, got)
	}

	if strings.TrimSpace(got) == "" {
		t.Errorf("fixture %s produced no problems — it no longer trips its rule", rule)
	}
}

// lintReport renders a validation outcome as the stable text a want.txt holds.
func lintReport(failure error, warnings []error) string {
	var b strings.Builder
	if failure != nil {
		b.WriteString(failure.Error())
		if !strings.HasSuffix(failure.Error(), "\n") {
			b.WriteString("\n")
		}
	}
	for _, w := range warnings {
		b.WriteString("warning: ")
		b.WriteString(w.Error())
		b.WriteString("\n")
	}
	return b.String()
}

// lineColRe matches a file:line:col position. A composed child's problem is placed in its own
// file (kid.yaml:7:7), so any YAML document counts.
var lineColRe = regexp.MustCompile(`\.ya?ml:\d+:\d+`)

// TestLintProblemsArePositioned pins that every fixture problem names its file and a
// line:col. A rule that cannot be positioned must be listed in unpositionable; none is.
func TestLintProblemsArePositioned(t *testing.T) {
	unpositionable := map[string]string{}

	entries, err := os.ReadDir(lintFixtureDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		rule := e.Name()
		t.Run(rule, func(t *testing.T) {
			body, err := os.ReadFile(filepath.Join(lintFixtureDir, rule, "want.txt"))
			if err != nil {
				t.Fatal(err)
			}
			for line := range strings.SplitSeq(strings.TrimSpace(string(body)), "\n") {
				if line == "" {
					continue
				}
				if !strings.Contains(line, ".yaml") {
					t.Errorf("%s names no file at all:\n%s", rule, line)
				}
				positioned := lineColRe.MatchString(line)
				if why, ok := unpositionable[rule]; ok {
					if positioned {
						t.Errorf("%s is listed as unpositionable (%s) but now reports a line:col — remove it from the list:\n%s", rule, why, line)
					}
					continue
				}
				if !positioned {
					t.Errorf("%s reports without a file:line:col, which README.md and doc.go both promise:\n%s", rule, line)
				}
			}
		})
	}
}
