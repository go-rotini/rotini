package codegen

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const stalePagesSpec = `version: 0.0.0
command:
  name: demo
  summary: the demo
  commands:
    - name: add
      summary: add one
`

// stalePagesConf turns each feature on or off by its value in on; every feature embeds its
// pages into one directory that also holds the editable templates.
func stalePagesConf(help, man, markdown, completion bool) string {
	b := func(v bool) string {
		if v {
			return "true"
		}
		return "false"
	}
	feature := func(typ string, on bool, extra string) string {
		return "    - type: " + typ + "\n      enabled: " + b(on) + "\n      embed: true\n      embed_dir: internal/cmd/demo/pages\n" + extra
	}
	tmpl := "      template: true\n      template_dir: internal/cmd/demo/pages\n"
	return `version: 0.0.0
generate:
  packages:
    - type: cmd
      file: internal/cmd/demo/zz_demo.go
      package: demo
  features:
` + feature("help", help, tmpl) + feature("man", man, tmpl+"      install_dir: share\n") + feature("markdown", markdown, tmpl) +
		feature("completion", completion, "      install_dir: share\n")
}

func assertGone(t *testing.T, dir string, paths ...string) {
	t.Helper()
	for _, p := range paths {
		if _, err := os.Stat(filepath.Join(dir, p)); !os.IsNotExist(err) {
			t.Errorf("%s survived; want it removed", p)
		}
	}
}

func assertKept(t *testing.T, dir string, paths ...string) {
	t.Helper()
	for _, p := range paths {
		if _, err := os.Stat(filepath.Join(dir, p)); err != nil {
			t.Errorf("%s: %v; want it kept", p, err)
		}
	}
}

// TestStalePages_featureOff checks that turning a feature off removes every page it wrote,
// embedded and installed, while its seeded template, another feature's pages and files that
// aren't pages stay.
func TestStalePages_featureOff(t *testing.T) {
	dir := generateInModule(t, stalePagesSpec, stalePagesConf(true, true, true, true))
	pages := "internal/cmd/demo/pages/"
	assertKept(t, dir, pages+"demo.1", pages+"demo-add.1", pages+"markdown_demo_add.md", pages+"man.txt.tmpl",
		pages+"markdown.md.tmpl", "share/man/man1/demo-add.1", "share/completions/demo.bash")
	writeTestFile(t, dir, pages+"notes.txt", "the author's")
	writeTestFile(t, dir, pages+"other.1", "the author's page")
	writeTestFile(t, dir, "share/man/man1/other.1", "the author's page")

	writeTestFile(t, dir, ".rotini.conf.yaml", stalePagesConf(true, false, false, false))
	runGenerate(t, dir)
	assertGone(t, dir, pages+"demo.1", pages+"demo-add.1", pages+"markdown_demo.md", pages+"markdown_demo_add.md",
		pages+"completion_bash.txt", "share/man/man1/demo.1", "share/man/man1/demo-add.1",
		"share/completions/demo.bash", "share/completions/_demo")
	assertKept(t, dir, pages+"help_demo_add.txt", pages+"help.txt.tmpl", pages+"man.txt.tmpl", pages+"markdown.md.tmpl",
		pages+"notes.txt", pages+"other.1", "share/man/man1/other.1")
}

// TestStalePages_rootRenamed checks that renaming the root command removes the man pages
// named after the old root, embedded and installed, as the help and markdown pages already
// are, and the completion scripts installed under its name, and leaves a page that isn't one
// the previous generate wrote.
func TestStalePages_rootRenamed(t *testing.T) {
	dir := generateInModule(t, stalePagesSpec, stalePagesConf(true, true, true, true))
	pages := "internal/cmd/demo/pages/"
	writeTestFile(t, dir, pages+"demo-extra.7", "the author's page")

	writeTestFile(t, dir, ".rotini.spec.yaml", strings.Replace(stalePagesSpec, "name: demo", "name: tool", 1))
	runGenerate(t, dir)
	assertGone(t, dir, pages+"demo.1", pages+"demo-add.1", pages+"help_demo.txt", pages+"markdown_demo_add.md",
		"share/man/man1/demo.1", "share/man/man1/demo-add.1", "share/completions/demo.bash", "share/completions/_demo")
	assertKept(t, dir, pages+"tool.1", pages+"tool-add.1", pages+"help_tool_add.txt", pages+"demo-extra.7",
		pages+"man.txt.tmpl", "share/man/man1/tool-add.1", "share/completions/tool.bash", pages+"completion_bash.txt")
}

// TestStalePages_caseOnlyRename checks that a root renamed only in case keeps every page and
// script it now writes, on a file system that ignores case as on one that doesn't.
func TestStalePages_caseOnlyRename(t *testing.T) {
	dir := generateInModule(t, stalePagesSpec, stalePagesConf(true, true, false, true))
	writeTestFile(t, dir, ".rotini.spec.yaml", strings.Replace(stalePagesSpec, "name: demo", "name: Demo", 1))
	runGenerate(t, dir)
	pages := "internal/cmd/demo/pages/"
	assertKept(t, dir, pages+"help_Demo_add.txt", pages+"demo-add.1", "share/man/man1/demo-add.1", "share/completions/Demo.bash")
}

// TestStalePages_embedDirMoved checks that pages left in a feature's old embed_dir are removed
// once it moves, and that a keep-listed one stays.
func TestStalePages_embedDirMoved(t *testing.T) {
	dir := generateInModule(t, stalePagesSpec, stalePagesConf(false, true, false, false))
	conf := strings.ReplaceAll(stalePagesConf(false, true, false, false), "embed_dir: internal/cmd/demo/pages", "embed_dir: internal/cmd/demo/man")
	conf = strings.Replace(conf, "      package: demo\n", "      package: demo\n      keep: [pages/demo.1]\n", 1)
	writeTestFile(t, dir, ".rotini.conf.yaml", conf)
	runGenerate(t, dir)
	assertGone(t, dir, "internal/cmd/demo/pages/demo-add.1")
	assertKept(t, dir, "internal/cmd/demo/pages/demo.1", "internal/cmd/demo/man/demo-add.1")
}

// TestStalePages_dryRun checks that a dry run lists each stale page as a deletion and removes
// none.
func TestStalePages_dryRun(t *testing.T) {
	dir := generateInModule(t, stalePagesSpec, stalePagesConf(true, true, false, false))
	writeTestFile(t, dir, ".rotini.spec.yaml", strings.Replace(stalePagesSpec, "name: demo", "name: tool", 1))
	planned, err := NewProcessor("0.0.0").GenerateDryRun(".rotini.spec.yaml", ".rotini.conf.yaml", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"delete internal/cmd/demo/pages/demo-add.1", "delete share/man/man1/demo-add.1", "delete internal/cmd/demo/pages/help_demo.txt"} {
		if !slices.ContainsFunc(planned.Changes, func(c string) bool { return strings.HasSuffix(c, ". "+want) }) {
			t.Errorf("changes =\n%s\nwant the line %q", strings.Join(planned.Changes, "\n"), want)
		}
	}
	if slices.ContainsFunc(planned.Changes, func(c string) bool { return strings.HasSuffix(c, ".tmpl") && strings.Contains(c, ". delete ") }) {
		t.Errorf("a dry run would delete a template:\n%s", strings.Join(planned.Changes, "\n"))
	}
	assertKept(t, dir, "internal/cmd/demo/pages/demo-add.1", "share/man/man1/demo-add.1")
}

// TestStalePages_skipPrune checks that init, which never deletes a file, leaves a turned
// off feature's pages alone.
func TestStalePages_skipPrune(t *testing.T) {
	dir := generateInModule(t, stalePagesSpec, stalePagesConf(true, true, false, false))
	writeTestFile(t, dir, ".rotini.conf.yaml", stalePagesConf(true, false, false, false))
	rs, err := reconcileSpec(filepath.Join(dir, ".rotini.spec.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	rc, err := reconcileConf(filepath.Join(dir, ".rotini.spec.yaml"), filepath.Join(dir, ".rotini.conf.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewProcessor("0.0.0").validateAndEmit(rs, rc, false, newPlanner(false)); err != nil {
		t.Fatal(err)
	}
	assertKept(t, dir, "internal/cmd/demo/pages/demo-add.1", "share/man/man1/demo-add.1")
}

// TestParsePriorPages checks what counts as a page the previous generate wrote.
func TestParsePriorPages(t *testing.T) {
	cmdDir := filepath.FromSlash("/m/internal/cmd/demo")
	src := generatedLine + `

package demo

var definition = rotini.Definition{
	Name:    "demo",
	Handler: "Demo",
}

//go:embed pages/help_demo.txt
var HelpDemo string

//go:embed pages/demo-add.1
var ManDemoAdd string

//go:embed examples/config.yaml
var ConfigExample string

//go:embed pages/notes.md
var MarkdownDemo string

//go:embed ../elsewhere/help_x.txt
var HelpX string

const ManSection = "1"

func ManPages() []rotini.Page {
	return []rotini.Page{
		{Name: "demo", Path: nil, Content: ManDemo},
		{Name: "demo-add", Path: []string{"add"}, Content: ManDemoAdd},
		{Name: "../x", Path: nil, Content: ManX},
	}
}
`
	got := parsePriorPages(src, cmdDir)
	want := []string{filepath.Join(cmdDir, "pages", "help_demo.txt"), filepath.Join(cmdDir, "pages", "demo-add.1")}
	if !slices.Equal(got.embedded, want) {
		t.Errorf("embedded = %q, want %q", got.embedded, want)
	}
	if !slices.Equal(got.manPages, []string{"demo", "demo-add"}) || got.manSection != "1" {
		t.Errorf("man pages = %q in section %q, want demo and demo-add in 1", got.manPages, got.manSection)
	}
	if got.root != "demo" {
		t.Errorf("root = %q, want demo", got.root)
	}

	if got := parsePriorPages(strings.TrimPrefix(src, generatedLine), cmdDir); got.embedded != nil || got.manPages != nil {
		t.Errorf("a file without the generated line recorded %+v; want nothing", got)
	}
}
