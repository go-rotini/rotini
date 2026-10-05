package codegen

import (
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// emittedPages reads every emitted page (.txt, .md) under the working directory (composeModuleStaged
// leaves it at the module root), keyed by slash path.
func emittedPages(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || (!strings.HasSuffix(p, ".txt") && !strings.HasSuffix(p, ".md") && !strings.HasSuffix(p, ".1")) {
			return err
		}
		b, err := os.ReadFile(p)
		out[filepath.ToSlash(p)] = string(b)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// pageEnding returns the one page whose path ends with suffix.
func pageEnding(pages map[string]string, suffix string) (string, bool) {
	for p, body := range pages {
		if strings.HasSuffix(p, "/"+suffix) {
			return body, true
		}
	}
	return "", false
}

// Verbatim page tests (`help:`, `man:`, `markdown:`) cover embed mode and composition; the
// e2e script r2_verbatim_pages covers inline mode.

const verbatimConfEmbed = `version: 0.0.0
generate:
  packages:
    - type: cmd
      file: internal/cmd/%s/zz_%s.go
      package: %s
  features:
    - type: help
      enabled: true
      embed: true
    - type: man
      enabled: true
      embed: true
    - type: markdown
      enabled: true
      embed: true
`

func verbatimConf(name string) string {
	return strings.ReplaceAll(verbatimConfEmbed, "%s", name)
}

// TestVerbatimPages_embedWritesExactBytes pins that an embedded verbatim page is written
// byte-for-byte, while a sibling without one is rendered.
func TestVerbatimPages_embedWritesExactBytes(t *testing.T) {
	const page = "APP\tPAGE  \n\n\n  kept `as` {{is}}\n"
	composeModuleStaged(t, map[string]string{
		"cmd/root/.rotini.spec.yaml": `version: 0.0.0
command:
  name: root
  summary: the root
  commands:
    - name: exact
      summary: has verbatim pages
      help: ` + strconv.Quote(page) + `
      man: ` + strconv.Quote(page) + `
      markdown: ` + strconv.Quote(page) + `
    - name: rendered
      summary: has none
`,
		"cmd/root/.rotini.conf.yaml": verbatimConf("root"),
	})
	pages := emittedPages(t)
	for _, f := range []string{"help_root_exact.txt", "root-exact.1", "markdown_root_exact.md"} {
		got, ok := pageEnding(pages, f)
		if !ok {
			t.Fatalf("no emitted file ending %s; have %v", f, keysOf(pages))
		}
		if got != page {
			t.Errorf("%s = %q, want the verbatim page %q", f, got, page)
		}
	}
	if got, ok := pageEnding(pages, "help_root_rendered.txt"); !ok || !strings.Contains(got, "Usage:") {
		t.Errorf("the command without a verbatim page was not rendered: %q", got)
	}
}

// TestVerbatimPages_refOverlay pins that a verbatim page on a `$ref` node replaces that node's
// page only; the child's sub-commands keep theirs.
func TestVerbatimPages_refOverlay(t *testing.T) {
	composeModuleStaged(t, map[string]string{
		"cmd/child/.rotini.spec.yaml": `version: 0.0.0
command:
  name: child
  summary: the child
  help: "CHILD PAGE\n"
  commands:
    - name: leaf
      summary: a leaf
      help: "LEAF PAGE\n"
`,
		"cmd/child/.rotini.conf.yaml": verbatimConf("child"),
		"cmd/root/.rotini.spec.yaml": `version: 0.0.0
command:
  name: root
  summary: the root
  commands:
    - $ref: ../child/.rotini.spec.yaml
      help: "PARENT PAGE FOR CHILD\n"
`,
		"cmd/root/.rotini.conf.yaml": verbatimConf("root"),
	})
	pages := emittedPages(t)
	for suffix, want := range map[string]string{
		"root/renders/help_root_child.txt":      "PARENT PAGE FOR CHILD\n", // the $ref node: the parent's page wins
		"root/renders/help_root_child_leaf.txt": "LEAF PAGE\n",             // the child's own sub-command keeps its page
		"child/renders/help_child.txt":          "CHILD PAGE\n",            // the child, built on its own, is untouched
	} {
		if got, ok := pageEnding(pages, suffix); !ok || got != want {
			t.Errorf("%s = %q (present %v), want %q", suffix, got, ok, want)
		}
	}
}
