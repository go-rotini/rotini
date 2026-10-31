package codegen

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The generated definition carries each command's exit_status as ExitStatus, including the
// entries a `$ref` overlay declares for a composed command.
func TestGenerate_exitStatusLiteral(t *testing.T) {
	emitted := composeModuleStaged(t, map[string]string{
		"cmd/child/.rotini.spec.yaml": `version: 0.0.0
command:
  name: child
  summary: the child
  exit_status:
    - { code: 4, name: own, summary: the child's own code }
`,
		"cmd/child/.rotini.conf.yaml": `version: 0.0.0
generate:
  packages:
    - type: cmd
      file: internal/cmd/child/zz_child.go
      package: child
`,
		"cmd/root/.rotini.spec.yaml": `version: 0.0.0
command:
  name: root
  summary: the root
  commands:
    - name: sync
      summary: sync
      exit_status:
        - { code: 75, name: busy, summary: the list is locked, retryable: true, docs_url: 'https://example.com/busy' }
    - $ref: ../child/.rotini.spec.yaml
      exit_status:
        - { code: 3, name: overlaid, summary: as the parent documents it }
`,
		"cmd/root/.rotini.conf.yaml": `version: 0.0.0
generate:
  packages:
    - type: cmd
      file: internal/cmd/root/zz_root.go
      package: root
`,
	})
	root := emitted["internal/cmd/root/zz_root.go"]
	for _, want := range []string{
		`{Code: 75, Name: "busy", Summary: "the list is locked", Retryable: true, DocsURL: "https://example.com/busy"}`,
		`{Code: 3, Name: "overlaid", Summary: "as the parent documents it"}`,
	} {
		if !strings.Contains(root, "ExitStatus: []rotini.ExitStatusDef{") || !strings.Contains(root, want) {
			t.Errorf("the root's definition lacks %s:\n%s", want, root)
		}
	}
	if strings.Contains(root, `Name: "own"`) {
		t.Errorf("the overlay should replace the child's own exit_status:\n%s", root)
	}
	if child := emitted["internal/cmd/child/zz_child.go"]; !strings.Contains(child, `{Code: 4, Name: "own", Summary: "the child's own code"}`) {
		t.Errorf("the child's definition lacks its exit_status:\n%s", child)
	}
}

// A command without its own exit_status documents the root's, in the definition and the
// contract alike, and the contract marks it inherited; a command's own list replaces the
// root's. A composed command inherits the root's too, outputs and all.
func TestGenerate_exitStatusInherited(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "child/.rotini.spec.yaml", `version: 0.0.0
command:
  name: child
  commands:
    - name: show
`)
	writeTestFile(t, dir, ".rotini.spec.yaml", `version: 0.0.0
command:
  name: app
  schemas:
    Failure:
      type: object
      properties:
        reason: {type: string}
  exit_status:
    - {code: 1, name: failed, summary: it failed, output: {$ref: "#/schemas/Failure"}}
    - {code: 2, summary: usage}
  commands:
    - name: list
      commands:
        - name: all
    - name: sync
      exit_status:
        - {code: 4, name: busy, summary: locked}
    - $ref: ./child/.rotini.spec.yaml
`)
	gp, err := resolveTree(decodeSpecYAML(t, readTestFile(t, filepath.Join(dir, ".rotini.spec.yaml"))), filepath.Join(dir, ".rotini.spec.yaml"), "example.com/app")
	if err != nil {
		t.Fatal(err)
	}

	def := renderDefinition(gp)
	rootCodes := "{Code: 1, Name: \"failed\", Summary: \"it failed\"},\n{Code: 2, Summary: \"usage\"},\n"
	// The root, list, list all, child and child show each carry the root's codes; sync its own.
	if got := strings.Count(def, rootCodes); got != 5 {
		t.Errorf("the root's codes appear %d times, want 5 (the root and four inheriting commands):\n%s", got, def)
	}
	if !strings.Contains(def, "ExitStatus: []rotini.ExitStatusDef{\n{Code: 4, Name: \"busy\", Summary: \"locked\"},\n},\nUsage: \"app sync\"") {
		t.Errorf("sync lacks its own exit_status:\n%s", def)
	}

	raw, err := gp.contract(gp.contractNodes())
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Commands []contractCommand `json:"commands"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	want := map[string]struct {
		codes     []int
		inherited bool
	}{
		"app":            {[]int{1, 2}, false},
		"app list":       {[]int{1, 2}, true},
		"app list all":   {[]int{1, 2}, true},
		"app sync":       {[]int{4}, false},
		"app child":      {[]int{1, 2}, true},
		"app child show": {[]int{1, 2}, true},
	}
	for _, c := range doc.Commands {
		w, ok := want[c.Name]
		if !ok {
			t.Errorf("unexpected command %s", c.Name)
			continue
		}
		delete(want, c.Name)
		var codes []int
		for _, e := range c.ExitStatus {
			codes = append(codes, e.Code)
		}
		if !slices.Equal(codes, w.codes) || c.ExitStatusInherited != w.inherited {
			t.Errorf("%s: exit codes %v inherited %v, want %v inherited %v", c.Name, codes, c.ExitStatusInherited, w.codes, w.inherited)
		}
		if w.inherited {
			out, _ := json.Marshal(c.ExitStatus[0].Output)
			if !strings.Contains(string(out), `"$ref":"#/definitions/Failure"`) {
				t.Errorf("%s: the inherited exit 1 output = %s, want the root's Failure", c.Name, out)
			}
		}
	}
	for name := range want {
		t.Errorf("the contract lacks %s", name)
	}
}
