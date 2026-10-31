package codegen

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// seeAlsoSpec has every kind of see_also entry on one command.
const seeAlsoSpec = `version: 0.0.0
command:
  name: taskr
  display_name: Taskr
  topics:
    - name: filters
      summary: how filters work
      body: Filters narrow a list.
  commands:
    - name: list
      summary: list tasks
      see_also:
        - git(1)
        - { command: remote add }
        - { topic: filters }
        - { url: https://example.com/docs }
    - name: remote
      summary: manage remotes
      commands:
        - name: add
          summary: add a remote
`

// TestSeeAlso_pages pins how each page shows each kind of entry: help the command line or the
// URL, man the page and section, markdown a link to the page or the URL; text is shown as
// written everywhere.
func TestSeeAlso_pages(t *testing.T) {
	t.Parallel()
	gp, err := resolveTree(decodeSpecYAML(t, seeAlsoSpec), filepath.Join(t.TempDir(), ".rotini.spec.yaml"), "example.com/taskr")
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		feat docFeature
		text string
		want string
	}{
		{helpFeatureDesc, templateHelp, "See Also:\n  git(1)\n  Taskr remote add\n  Taskr help filters\n  https://example.com/docs"},
		{manFeatureDesc, templateMan, `.SH "SEE ALSO"` + "\n" + `\fBtaskr\fR(1), git(1), \fBtaskr\-remote\-add\fR(1), \fBtaskr\-filters\fR(1), https://example.com/docs`},
		{markdownFeatureDesc, templateMarkdown, "## See Also\n\n- [taskr](taskr.md)\n- git(1)\n- [Taskr remote add](taskr-remote-add.md)\n- [Taskr help filters](taskr-filters.md)\n- <https://example.com/docs>"},
	} {
		tmpl, err := parseDocTemplate(tt.feat.name, tt.text)
		if err != nil {
			t.Fatal(err)
		}
		i := slices.IndexFunc(flattenFeature(gp, tt.feat), func(n helpNode) bool { return slices.Equal(n.path, []string{"list"}) })
		page, err := renderDocText(tmpl, flattenFeature(gp, tt.feat)[i].data)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(page, tt.want) {
			t.Errorf("%s page lacks\n%s\n--- page\n%s", tt.feat.name, tt.want, page)
		}
	}
}

// TestSeeAlso_composedCommandsNameTheirOwnSpec pins that a composed spec's `{command: …}`
// names a command of its own tree, wherever it is mounted, and that an entry written beside
// the `$ref` or in an override names one of the referring spec's.
func TestSeeAlso_composedCommandsNameTheirOwnSpec(t *testing.T) {
	files := overrideTree(`      see_also: [{ command: db status }]
      commands:
        - name: status
          see_also: [{ command: db migrate }]
`)
	files["cmd/db/.rotini.spec.yaml"] = strings.Replace(files["cmd/db/.rotini.spec.yaml"], `      examples: ["db status"]
`, `      examples: ["db status"]
    - name: backup
      see_also: [{ command: status }]
`, 1)
	files["cmd/mig/.rotini.spec.yaml"] = strings.Replace(files["cmd/mig/.rotini.spec.yaml"], `      summary: mig's up
`, `      summary: mig's up
      see_also: [{ command: up }]
`, 1)
	gp, err := resolveOverrideTree(t, files)
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		path []string
		want string
	}{
		{[]string{"db"}, "acme db status"},                      // beside the $ref: the root's own path
		{[]string{"db", "status"}, "acme db migrate"},           // an override: the root's own path
		{[]string{"db", "backup"}, "acme db status"},            // db's own: below where db is mounted
		{[]string{"db", "migrate", "up"}, "acme db migrate up"}, // mig's own, two mounts down
	} {
		rows := seeAlsoRows(nodeAt(t, gp, tt.path...).help.SeeAlso, gp.rootName, gp.rootDisplay)
		if len(rows) != 1 || rows[0].Text != tt.want {
			t.Errorf("%v see_also = %+v, want %q", tt.path, rows, tt.want)
		}
	}
}
