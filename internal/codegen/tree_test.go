package codegen

import (
	"strings"
	"testing"
)

// TestTree pins the tree's lines: names and listed aliases, the markers in order, a composed
// child resolved under its $ref, plugins and discovery as the last children, and a hidden
// command's children unmarked.
func TestTree(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "go.mod", "module example.com/musak\n\ngo 1.26\n")
	writeTestFile(t, dir, "artists/.rotini.spec.yaml", `version: 0.0.0
command:
  name: artists
  commands:
    - name: list
      aliases: [ls]
    - name: show
`)
	writeTestFile(t, dir, "musak/.rotini.spec.yaml", `version: 0.0.0
command:
  name: musak
  commands:
    - $ref: ../artists/.rotini.spec.yaml
    - name: debug
      hidden: true
      commands:
        - name: dump
    - name: remove
      aliases: [rm, delete]
      deprecated_identifiers: [delete]
      deprecated: use rm
    - name: purge
      deprecated: gone soon
    - name: exec
      passthrough: true
      arguments:
        - name: args
          schema: { type: '[]string' }
  plugins:
    - name: cloud
      aliases: [c]
  plugin_discovery: {}
`)
	t.Chdir(dir)
	got, err := NewProcessor("0.0.0").Tree("musak/.rotini.spec.yaml")
	if err != nil {
		t.Fatal(err)
	}
	want := `musak
  artists  [$ref ../artists/.rotini.spec.yaml]
    list, ls
    show
  debug  [hidden]
    dump
  remove, rm, delete (deprecated alias)  [deprecated]
  purge  [deprecated]
  exec  [passthrough]
  cloud, c  [plugin]
  *  [discovers musak-*]
`
	if got != want {
		t.Errorf("tree =\n%s\nwant\n%s", got, want)
	}
}

// TestTreeRefusesAnInvalidSpec pins that a spec with errors prints no tree.
func TestTreeRefusesAnInvalidSpec(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, ".rotini.spec.yaml", "version: 0.0.0\ncommand: {name: demo, flags: [{name: x, schema: {type: nosuchtype}}]}\n")
	t.Chdir(dir)
	got, err := NewProcessor("0.0.0").Tree("")
	if err == nil || got != "" {
		t.Fatalf("Tree = %q, %v; want no tree and an error", got, err)
	}
	if !strings.Contains(err.Error(), "nosuchtype") {
		t.Errorf("error = %v, want validate's problem", err)
	}
}
