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

// The lint layer is rotini's clearest advantage over frameworks that declare a CLI in Go: 35
// rules that reject a mistake before any code exists, each naming the offending thing, its
// file:line:col, and what to do instead. Every one had coverage; NOT ONE had a test that
// asserted what it SAYS. A refactor that turned lintConfigSource's sentence into
// "invalid config_source" would have passed the entire suite.
//
// So the messages are a shipped artifact and get the same freeze testdata/surface.txt gives
// the exported API: one fixture directory per rule, holding the minimal documents that trip
// exactly that rule and the exact output they produce.
//
//	internal/codegen/testdata/lint/<ruleName>/
//	    spec.yaml     the documents (conf.yaml is optional; a minimal valid one is supplied)
//	    conf.yaml
//	    want.txt      every problem reported, verbatim
//
// Recording a fixture is not the assertion — READING it is. When -update writes a want.txt,
// judge the message as a stranger would: does it name the thing, say where, and say what to
// do instead? A correct rejection with a useless message is a bug, and this is where it is
// caught.

// updateLintFixtures rewrites every want.txt:
// `go test ./internal/codegen -run LintFixtures -update-lint`.
var updateLintFixtures = flag.Bool("update-lint", false, "rewrite the lint fixture want.txt files")

const lintFixtureDir = "testdata/lint"

// lintFixtureConf is the conf used when a fixture supplies no conf.yaml of its own: minimal,
// valid, and irrelevant to every spec rule, so a spec fixture stays about its own rule.
const lintFixtureConf = `version: 0.0.0
generate:
  packages:
    - type: cmd
      file: internal/cmd/demo/zz_demo.go
      package: demo
`

// ruleNames returns the registered lint rules' function names, which are also their fixture
// directory names. Taking them from the registries rather than a hand-kept list is what makes
// TestLintFixturesComplete able to fail on a rule someone added without a fixture.
func ruleNames() []string {
	var names []string
	add := func(fn any) {
		full := runtime.FuncForPC(reflect.ValueOf(fn).Pointer()).Name()
		names = append(names, full[strings.LastIndex(full, ".")+1:])
	}
	for _, fn := range specLints {
		add(fn)
	}
	for _, fn := range confLints {
		add(fn)
	}
	sort.Strings(names)
	return names
}

// TestLintFixturesComplete fails when a rule has no fixture directory, or a directory names no
// rule. Modeled on TestConformance_matrixComplete, which keeps the input matrix honest the
// same way: adding a rule without a fixture breaks the build, so the corpus cannot rot.
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

	// A fixture is a whole little module: spec.yaml and conf.yaml become the two documents
	// validate is pointed at, and any other file (a child spec a $ref composes, a config
	// file an input pins) is copied in beside them under its own name.
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

	// "collect" so the whole report is recorded, not just the first problem: a fixture that
	// trips more rules than it meant to is itself worth seeing in the diff.
	var warnings []error
	failure := NewProcessor("0.0.0").Validate(".rotini.spec.yaml", ".rotini.conf.yaml", false, "collect",
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

	// A fixture exists to make a rule fire. One that reports nothing is silently useless.
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
		b.WriteString("warning: " + w.Error() + "\n")
	}
	return b.String()
}

// TestLintProblemsArePositioned is the claim README.md and doc.go both make about validation:
// a problem is reported at a file:line:col, not just named. Before the fixture corpus existed,
// NOT ONE of the 35 rules produced a position — locateProblems only placed JSON Schema
// violations, whose loc happens to be a pointer, and every lint rule's loc is a human label.
//
// One rule legitimately has nowhere to point, and it is named here rather than left to erode
// the promise silently: lintImportConsistency reports a type declared with conflicting
// imports in two or more places, so there is no single node — the message names both imports
// instead. Anything else reporting without a position is a regression.
// lineColRe matches the full position promise: file:line:col.
var lineColRe = regexp.MustCompile(`\.rotini\.(spec|conf)\.yaml:\d+:\d+`)

func TestLintProblemsArePositioned(t *testing.T) {
	unpositionable := map[string]string{
		"lintImportConsistency": "reports a conflict across two or more declarations",
	}

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
			for _, line := range strings.Split(strings.TrimSpace(string(body)), "\n") {
				if line == "" {
					continue
				}
				// EVERY problem names its file — a message without one is unusable
				// from a Makefile or against several documents.
				if !strings.Contains(line, ".rotini.spec.yaml") && !strings.Contains(line, ".rotini.conf.yaml") {
					t.Errorf("%s names no file at all:\n%s", rule, line)
				}
				// A line:col is the stronger promise, and a few rules genuinely cannot
				// make it: a conflict ACROSS declarations has no single node to point at.
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
