package codegen

import (
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
