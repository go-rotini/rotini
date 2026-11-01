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

// documentedExits is a command's own list, else the root's; `inherit: true` puts the root's
// entries in its place, less the codes the command lists itself.
func TestDocumentedExits(t *testing.T) {
	root := []ExitStatusEntry{{Code: 1, Summary: "failed"}, {Code: 2, Summary: "usage"}, {Code: 3, Summary: "not found"}}
	type exit struct {
		code      int
		summary   string
		inherited bool
	}
	for _, tc := range []struct {
		name string
		own  []ExitStatusEntry
		want []exit
	}{
		{"none documents the root's", nil, []exit{{1, "failed", true}, {2, "usage", true}, {3, "not found", true}}},
		{"an own list replaces it", []ExitStatusEntry{{Code: 4, Summary: "busy"}}, []exit{{4, "busy", false}}},
		{"inherit adds to it", []ExitStatusEntry{{Inherit: true}, {Code: 4, Summary: "busy"}},
			[]exit{{1, "failed", true}, {2, "usage", true}, {3, "not found", true}, {4, "busy", false}}},
		{"in its place", []ExitStatusEntry{{Code: 4, Summary: "busy"}, {Inherit: true}},
			[]exit{{4, "busy", false}, {1, "failed", true}, {2, "usage", true}, {3, "not found", true}}},
		{"an own code wins", []ExitStatusEntry{{Inherit: true}, {Code: 3, Summary: "no such task"}},
			[]exit{{1, "failed", true}, {2, "usage", true}, {3, "no such task", false}}},
		{"once", []ExitStatusEntry{{Inherit: true}, {Inherit: true}}, []exit{{1, "failed", true}, {2, "usage", true}, {3, "not found", true}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got []exit
			for _, e := range documentedExits(tc.own, root) {
				got = append(got, exit{e.Code, e.Summary, e.inherited})
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("documentedExits = %v, want %v", got, tc.want)
			}
		})
	}
	if got := documentedExits([]ExitStatusEntry{{Inherit: true}}, nil); got != nil {
		t.Errorf("inheriting an empty root = %v, want nothing", got)
	}
}

// A `$ref` entry becomes the named exit status, named by its key, in either spelling; one that
// names nothing, or sets a key beside its $ref, keeps the $ref for the lint.
func TestResolveExitStatusRefs(t *testing.T) {
	spec := decodeSpecYAML(t, `version: 0.0.0
command:
  name: app
  exit_statuses:
    not_found: {code: 3, summary: no such task, retryable: true, docs_url: 'https://example.com/3', output: {type: object}}
  exit_status:
    - $ref: not_found
  commands:
    - name: get
      exit_status:
        - $ref: '#/exit_statuses/not_found'
        - $ref: missing
        - {$ref: not_found, summary: other}
`)
	want := ExitStatusEntry{Code: 3, Name: "not_found", Summary: "no such task", Retryable: true, DocsUrl: "https://example.com/3"}
	for _, e := range []ExitStatusEntry{spec.Command.ExitStatus[0], spec.Command.Commands[0].ExitStatus[0]} {
		if e.Output == nil || e.Output.Type != "object" {
			t.Errorf("resolved entry output = %v, want the named one's", e.Output)
		}
		e.Output = nil
		if e != want {
			t.Errorf("resolved entry = %+v, want %+v", e, want)
		}
	}
	if spec.Command.ExitStatus[0].Output == spec.Command.Commands[0].ExitStatus[0].Output {
		t.Error("two references share one output schema; each needs its own copy")
	}
	get := spec.Command.Commands[0].ExitStatus
	if get[1].Ref != "missing" || get[1].Code != 0 || get[2].Ref == "" || get[2].Code != 0 {
		t.Errorf("unresolvable entries = %+v, %+v, want their $ref kept and nothing filled in", get[1], get[2])
	}
}

// A command with `inherit: true` documents the root's codes and its own, in the definition and
// the contract alike, marked inherited; a composed child's sub-command adds to the program's
// root, and its `$ref` names its own spec's exit_statuses.
func TestGenerate_exitStatusAddsToTheRoot(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "child/.rotini.spec.yaml", `version: 0.0.0
command:
  name: child
  exit_statuses:
    gone: {code: 6, summary: the child's own}
  commands:
    - name: show
      exit_status:
        - inherit: true
        - $ref: gone
`)
	writeTestFile(t, dir, ".rotini.spec.yaml", `version: 0.0.0
command:
  name: app
  schemas:
    Failure:
      type: object
      properties:
        reason: {type: string}
  exit_statuses:
    busy: {code: 4, summary: locked, retryable: true}
  exit_status:
    - {code: 1, name: failed, summary: it failed, output: {$ref: "#/schemas/Failure"}}
    - {code: 2, summary: usage}
  commands:
    - name: sync
      exit_status:
        - inherit: true
        - $ref: busy
        - {code: 2, summary: bad sync flags}
    - $ref: ./child/.rotini.spec.yaml
`)
	gp, err := resolveTree(decodeSpecYAML(t, readTestFile(t, filepath.Join(dir, ".rotini.spec.yaml"))), filepath.Join(dir, ".rotini.spec.yaml"), "example.com/app")
	if err != nil {
		t.Fatal(err)
	}

	def := renderDefinition(gp)
	for _, want := range []string{
		"ExitStatus: []rotini.ExitStatusDef{\n{Code: 1, Name: \"failed\", Summary: \"it failed\"},\n{Code: 4, Name: \"busy\", Summary: \"locked\", Retryable: true},\n{Code: 2, Summary: \"bad sync flags\"},\n},\nUsage: \"app sync\"",
		"ExitStatus: []rotini.ExitStatusDef{\n{Code: 1, Name: \"failed\", Summary: \"it failed\"},\n{Code: 2, Summary: \"usage\"},\n{Code: 6, Name: \"gone\", Summary: \"the child's own\"},\n},\nUsage: \"app child show\"",
	} {
		if !strings.Contains(def, want) {
			t.Errorf("the definition lacks\n%s\nin:\n%s", want, def)
		}
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
	want := map[string][]int{"app": {1, 2}, "app sync": {1, 4, 2}, "app child": {1, 2}, "app child show": {1, 2, 6}}
	for _, c := range doc.Commands {
		w, ok := want[c.Name]
		if !ok {
			continue
		}
		var codes []int
		for _, e := range c.ExitStatus {
			codes = append(codes, e.Code)
		}
		if !slices.Equal(codes, w) || c.ExitStatusInherited != (c.Name != "app") {
			t.Errorf("%s: exit codes %v inherited %v, want %v inherited %v", c.Name, codes, c.ExitStatusInherited, w, c.Name != "app")
		}
		if c.Name == "app child show" {
			out, _ := json.Marshal(c.ExitStatus[0].Output)
			if !strings.Contains(string(out), `"$ref":"#/definitions/Failure"`) {
				t.Errorf("%s: the inherited exit 1 output = %s, want the root's Failure", c.Name, out)
			}
		}
	}
}
