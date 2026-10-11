package codegen

import (
	"reflect"
	"strings"
	"testing"
)

const flagSetSpec = `version: 0.0.0
command:
  name: acme
  summary: flag sets, value rules and hidden spellings
  flag_sets:
    Output:
      group: Output
      flags:
        - name: format
          summary: how to print
          schema: { type: string, enum: [text, csv], default: text }
        - name: delimiter
          summary: the csv separator
          group: CSV
          schema: { type: string }
      flag_dependencies:
        - when: format
          equals: [csv]
          requires: [delimiter]
  use: [Output]
  commands:
    - name: list
      summary: list things
      use: [Output]
      hidden_aliases: [ls-all]
      flags:
        - name: all
          summary: every thing
          hidden_identifiers: [--everything]
          schema: { type: bool }
        - name: old-all
          summary: every thing
          deprecated: renamed
          replaced_by: --all
          schema: { type: bool }
      flag_dependencies:
        - unless: [all]
          forbids: [old-all]
    - name: show
      summary: show a thing
      use: [Output]
    - name: get
      summary: show a thing
      deprecated: renamed
      replaced_by: show
`

const flagSetConf = `version: 0.0.0
generate:
  packages:
    - type: cmd
      file: internal/cmd/acme/zz_acme.go
      package: acme
  features:
    - type: help
      enabled: true
`

// A flag set used by the root and two commands generates one struct, embedded in each flags
// struct, while the Definition lists every flag flat. Help groups the set's members, and the
// module builds.
func TestFlagSets_generate(t *testing.T) {
	dir, _ := emitModule(t, flagSetSpec, flagSetConf)
	src := readEmitted(t, dir, "internal/cmd/acme/zz_acme.go")
	if n := strings.Count(src, "type AcmeOutputFlagSet struct {"); n != 1 {
		t.Errorf("set struct declared %d times, want 1", n)
	}
	if n := strings.Count(src, "\tAcmeOutputFlagSet\n"); n != 3 {
		t.Errorf("set struct embedded %d times, want 3", n)
	}
	for _, want := range []string{
		"type AcmeListFlags struct {\n\tAll    bool `rotini:\"all\"`\n\tOldAll bool `rotini:\"old-all\"`\n\tAcmeOutputFlagSet\n}",
		"type AcmeShowFlags struct {\n\tAcmeOutputFlagSet\n}",
		`Name: "format", Identifiers: []string{"--format"}`,
		`When: "format", Requires: []string{"delimiter"}, Equals: []string{"csv"}`,
		`Unless: []string{"all"}, Forbids: []string{"old-all"}`,
		`HiddenAliases: []string{"ls-all"}`,
		`ReplacedBy: "--all"`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("generated file is missing\n%s", want)
		}
	}
	for _, want := range []string{`HiddenIdentifiers: []string{"--everything"}`, `ReplacedBy: "acme show"`, `case "list", "ls-all":`} {
		if !strings.Contains(src, want) {
			t.Errorf("generated file is missing %s", want)
		}
	}
	help := src // the help pages are inline in the cmd file
	for _, want := range []string{"Output:", "CSV:", "use --all instead", "use acme show instead"} {
		if !strings.Contains(help, want) {
			t.Errorf("help pages are missing %q", want)
		}
	}
	if strings.Contains(help, "ls-all\\t") || strings.Contains(help, "--everything ") {
		t.Errorf("a hidden spelling is listed in help")
	}
	buildEmitted(t, dir)
}

// The models layout declares the set struct in the models package and aliases it in the cmd
// package, so handler code reads it either way.
func TestFlagSets_models(t *testing.T) {
	dir, _ := emitModule(t, flagSetSpec, `version: 0.0.0
generate:
  packages:
    - type: cmd
      file: internal/cmd/acme/zz_acme.go
      package: acme
    - type: models
      file: internal/models/zz_models.go
      package: models
`)
	if models := readEmitted(t, dir, "internal/models/zz_models.go"); !strings.Contains(models, "type AcmeOutputFlagSet struct {") {
		t.Errorf("models file has no set struct")
	}
	if cmd := readEmitted(t, dir, "internal/cmd/acme/zz_acme.go"); !strings.Contains(cmd, "type AcmeOutputFlagSet = models.AcmeOutputFlagSet") {
		t.Errorf("cmd file has no alias for the set struct")
	}
	buildEmitted(t, dir)
}

// The contract records each flag's set, the value rules, hidden spellings and replacements, in
// the shape schema-contract.json describes.
func TestContract_parserFacts(t *testing.T) {
	_, cmds := contractJSON(t, flagSetSpec)
	list := cmds["acme list"]
	if got := list["hidden_aliases"]; !reflect.DeepEqual(got, []any{"ls-all"}) {
		t.Errorf("hidden_aliases = %v", got)
	}
	if got := entry(t, list, "flags", "format")["flag_set"]; got != "Output" {
		t.Errorf("format flag_set = %v", got)
	}
	if got := entry(t, list, "flags", "all")["flag_set"]; got != nil {
		t.Errorf("an own flag has flag_set %v", got)
	}
	if got := entry(t, list, "flags", "all")["hidden_identifiers"]; !reflect.DeepEqual(got, []any{"--everything"}) {
		t.Errorf("hidden_identifiers = %v", got)
	}
	if got := entry(t, list, "flags", "old-all")["replaced_by"]; got != "--all" {
		t.Errorf("flag replaced_by = %v", got)
	}
	if got := cmds["acme get"]["replaced_by"]; got != "show" {
		t.Errorf("command replaced_by = %v, want the path below the root", got)
	}
	deps, _ := list["flag_dependencies"].([]any)
	want := []any{
		map[string]any{"unless": []any{"all"}, "forbids": []any{"old-all"}},
		map[string]any{"when": "format", "requires": []any{"delimiter"}, "equals": []any{"csv"}},
	}
	if !reflect.DeepEqual(deps, want) {
		t.Errorf("flag_dependencies = %v, want %v", deps, want)
	}
}

// A composed child's replaced_by is a path below its own root, rendered under the path the
// parent mounts it at.
func TestReplacedBy_composed(t *testing.T) {
	emitted := composeModuleStaged(t, map[string]string{
		"cmd/child/.rotini.spec.yaml": `version: 0.0.0
command:
  name: child
  commands:
    - name: migrate
    - name: upgrade
      deprecated: renamed
      replaced_by: migrate
`,
		"cmd/child/.rotini.conf.yaml": "version: 0.0.0\ngenerate:\n  packages:\n    - type: cmd\n      file: internal/cmd/child/zz_child.go\n      package: child\n",
		"cmd/root/.rotini.spec.yaml": `version: 0.0.0
command:
  name: root
  display_name: acme
  commands:
    - name: db
      $ref: ../child/.rotini.spec.yaml
`,
		"cmd/root/.rotini.conf.yaml": "version: 0.0.0\ngenerate:\n  packages:\n    - type: cmd\n      file: internal/cmd/root/zz_root.go\n      package: root\n",
	})
	if src := emitted["internal/cmd/root/zz_root.go"]; !strings.Contains(src, `ReplacedBy: "acme db migrate"`) {
		t.Errorf("parent literal lacks the mounted replacement:\n%s", src)
	}
	if src := emitted["internal/cmd/child/zz_child.go"]; !strings.Contains(src, `ReplacedBy: "child migrate"`) {
		t.Errorf("child literal lacks its own replacement")
	}
}
