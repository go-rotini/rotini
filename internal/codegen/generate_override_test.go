package codegen

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// overrideTree is three layers: root mounts db, which mounts mig as `migrate`. Each layer sets
// some keys of the commands below it.
func overrideTree(rootCommands string) map[string]string {
	return map[string]string{
		"cmd/mig/.rotini.spec.yaml": `version: 0.0.0
command:
  name: mig
  summary: mig's summary
  footer: mig's footer
  examples: ["mig --steps 2"]
  see_also: [mig-notes]
  flags:
    - name: steps
      identifiers: [--steps]
      schema: { type: int }
  commands:
    - name: up
      summary: mig's up
`,
		"cmd/db/.rotini.spec.yaml": `version: 0.0.0
command:
  name: db
  summary: db's summary
  footer: Run 'db help' for more.
  commands:
    - name: status
      summary: db's status
      deprecated: use migrate
      examples: ["db status"]
    - $ref: ../mig/.rotini.spec.yaml
      name: migrate
      aliases: [m]
      summary: db's migrate
      examples: ["db migrate --steps 2"]
`,
		"cmd/root/.rotini.spec.yaml": `version: 0.0.0
command:
  name: acme
  summary: acme
  commands:
    - $ref: ../db/.rotini.spec.yaml
` + rootCommands,
	}
}

// resolveOverrideTree resolves the root of files in a module, from a temporary directory.
func resolveOverrideTree(t *testing.T, files map[string]string) (*program, error) {
	t.Helper()
	dir := t.TempDir()
	writeTestFile(t, dir, "go.mod", "module example.com/compose\n\ngo 1.26\n")
	for path, body := range files {
		writeTestFile(t, dir, path, body)
	}
	t.Chdir(dir)
	spec, err := readSpec("cmd/root/.rotini.spec.yaml")
	if err != nil {
		t.Fatal(err)
	}
	return resolveTree(spec, "cmd/root/.rotini.spec.yaml", "example.com/compose")
}

// nodeAt returns the node of the resolved tree at path.
func nodeAt(t *testing.T, gp *program, path ...string) rnode {
	t.Helper()
	nodes := gp.tree
	var n rnode
	for _, name := range path {
		i := slices.IndexFunc(nodes, func(c rnode) bool { return c.name == name })
		if i < 0 {
			t.Fatalf("no command %q on the path %v", name, path)
		}
		n, nodes = nodes[i], nodes[i].children
	}
	return n
}

// TestOverride_layers pins that a parent overrides the commands it mounts, at every depth, and
// that the spec nearest the root wins: root's examples for `db migrate` replace db's, which
// replace mig's; a key root doesn't set keeps db's, then mig's.
func TestOverride_layers(t *testing.T) {
	gp, err := resolveOverrideTree(t, overrideTree(`      footer: Run 'acme db help' for more.
      effects: { kind: read }
      commands:
        - name: status
          examples: ["acme db status"]
          replaced_by: db migrate
        - name: migrate
          examples: ["acme db migrate --steps 2"]
          see_also: []
          commands:
            - name: up
              summary: acme's up
              hidden: true
        - name: extra
          summary: added beside the $ref
`))
	if err != nil {
		t.Fatal(err)
	}
	db := nodeAt(t, gp, "db")
	if db.help.Footer != "Run 'acme db help' for more." || db.help.Effects == nil || db.help.Effects.Kind != "read" {
		t.Errorf("db: footer %q, effects %v; want root's", db.help.Footer, db.help.Effects)
	}
	status := nodeAt(t, gp, "db", "status")
	if !slices.Equal(status.help.Examples, []string{"acme db status"}) || status.deprecated != "use migrate" {
		t.Errorf("db status: examples %q, deprecated %q; want root's examples, db's deprecation", status.help.Examples, status.deprecated)
	}
	if status.replacedBy.text != "acme db migrate" {
		t.Errorf("db status replaced by %q; want root's, relative to root", status.replacedBy.text)
	}
	migrate := nodeAt(t, gp, "db", "migrate")
	if !slices.Equal(migrate.help.Examples, []string{"acme db migrate --steps 2"}) {
		t.Errorf("db migrate examples = %q; want root's", migrate.help.Examples)
	}
	if migrate.help.Summary != "db's migrate" || migrate.help.Footer != "mig's footer" || !slices.Equal(migrate.aliases, []string{"m"}) {
		t.Errorf("db migrate: summary %q, footer %q, aliases %q; want db's, mig's, db's", migrate.help.Summary, migrate.help.Footer, migrate.aliases)
	}
	if migrate.help.SeeAlso == nil || len(migrate.help.SeeAlso) != 0 {
		t.Errorf("db migrate see_also = %#v; want it cleared by root's empty list", migrate.help.SeeAlso)
	}
	up := nodeAt(t, gp, "db", "migrate", "up")
	if up.help.Summary != "acme's up" || !up.hidden {
		t.Errorf("db migrate up: summary %q, hidden %v; want root's", up.help.Summary, up.hidden)
	}
	nodeAt(t, gp, "db", "extra")
}

// TestOverride_twoLayers pins an override of a mounted spec's own sub-command, the case where
// `acme db migrate --help` showed the child's `db migrate --steps 2`.
func TestOverride_twoLayers(t *testing.T) {
	files := overrideTree(`      commands:
        - name: status
          examples: ["acme db status"]
`)
	files["cmd/db/.rotini.spec.yaml"] = strings.Replace(files["cmd/db/.rotini.spec.yaml"], "    - $ref: ../mig/.rotini.spec.yaml\n      name: migrate\n      aliases: [m]\n      summary: db's migrate\n      examples: [\"db migrate --steps 2\"]\n", "", 1)
	gp, err := resolveOverrideTree(t, files)
	if err != nil {
		t.Fatal(err)
	}
	if got := nodeAt(t, gp, "db", "status").help.Examples; !slices.Equal(got, []string{"acme db status"}) {
		t.Errorf("db status examples = %q", got)
	}
	if got := nodeAt(t, gp, "db").help.Footer; got != "Run 'db help' for more." {
		t.Errorf("db footer = %q; a key the parent doesn't write keeps the child's", got)
	}
}

// TestOverride_rejected pins the composer's refusals: an override that sets a key the handler
// depends on, names no command, or mounts a spec.
func TestOverride_rejected(t *testing.T) {
	for name, tc := range map[string]struct{ commands, want string }{
		"flags": {`      commands:
        - name: status
          flags:
            - { name: x, identifiers: [--x], schema: { type: bool } }
`, "sets `flags`"},
		"handler": {`      commands:
        - name: status
          handler: { import: example.com/x, convention: Status }
`, "sets `handler`"},
		"unknown nested": {`      commands:
        - name: migrate
          commands:
            - name: down
`, `names the command "down" under "db migrate"`},
		"nested ref": {`      commands:
        - name: migrate
          commands:
            - $ref: ../../mig/.rotini.spec.yaml
`, "mounts"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := resolveOverrideTree(t, overrideTree(tc.commands))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

// TestOverride_validatePositions pins where validate reports: an override's problem in the
// parent's file at the entry, an example that doesn't parse against the mounted command in the
// parent's file, and a child's own problem in the child's file only.
func TestOverride_validatePositions(t *testing.T) {
	files := overrideTree(`      examples: ["acme db migrate --bogus"]
      commands:
        - name: status
          stdin: { format: text }
        - name: migrate
          deprecated_identifiers: [m]
          commands:
            - name: down
`)
	files["cmd/mig/.rotini.spec.yaml"] += "  deprecated_since: 1.0.0\n"
	dir := t.TempDir()
	writeTestFile(t, dir, "go.mod", "module example.com/compose\n\ngo 1.26\n")
	for path, body := range files {
		writeTestFile(t, dir, path, body)
	}
	t.Chdir(dir)
	err := NewProcessor("0.0.0").Validate("cmd/root/.rotini.spec.yaml", "", false, "collect", "", nil, nil)
	if err == nil {
		t.Fatal("validated clean")
	}
	root, mig := "cmd/root/.rotini.spec.yaml", filepath.FromSlash("cmd/mig/.rotini.spec.yaml") // the root as given, a $ref as resolved
	for _, want := range []string{
		root + ":10:18: command acme/db/status: overrides the mounted command `acme db status`, so it can't set `stdin`",
		root + ":14:15: command acme/db/migrate: overrides `acme db migrate`, which declares no command \"down\"",
		root + `:7:18: command acme: $ref "../db/.rotini.spec.yaml": example "acme db migrate --bogus" does not parse: acme db migrate: unknown flag "--bogus"`,
		mig + ":15:21: command mig: sets `deprecated_since` without `deprecated`",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q in:\n%v", want, err)
		}
	}
	// The override's deprecated_identifiers name an alias db gives the command, and mig's own
	// problem isn't repeated at the override.
	for _, unwanted := range []string{"is not one of its aliases", root + ":12:"} {
		if strings.Contains(err.Error(), unwanted) {
			t.Errorf("unexpected %q in:\n%v", unwanted, err)
		}
	}
	if strings.Count(err.Error(), "`deprecated_since` without `deprecated`") != 1 {
		t.Errorf("mig's problem reported more than once:\n%v", err)
	}
}

// TestStub_helpAnswersForPlugins pins that the seeded help command asks PluginHelp first, so
// `help <plugin>` runs the plugin's own help, even for plugins declared after it is seeded.
func TestStub_helpAnswersForPlugins(t *testing.T) {
	emitted := composeModuleStaged(t, map[string]string{
		"cmd/root/.rotini.spec.yaml": "version: 0.0.0\ncommand:\n  name: root\n" +
			"  commands:\n    - name: help\n      arguments:\n        - name: command\n          schema: { type: '[]string' }\n",
		"cmd/root/.rotini.conf.yaml": "version: 0.0.0\ngenerate:\n  packages:\n    - type: cmd\n      file: internal/cmd/root/zz_root.go\n  features:\n    - type: help\n      enabled: true\n",
	})
	stub := emitted["internal/cmd/root/root_help.go"]
	want := "\tif rtx.PluginHelp(inputs.RootHelp.Arguments.Command...) {\n\t\treturn\n\t}\n\n\tpage, err := rtx.HelpFor("
	if !strings.Contains(stub, want) {
		t.Errorf("the help stub doesn't ask PluginHelp first:\n%s", stub)
	}
}
