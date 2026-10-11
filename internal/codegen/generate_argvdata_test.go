package codegen

import (
	"strings"
	"testing"
)

// TestCompose_recordsInputsTypes pins the Definition's Inputs fields across composition: the
// root's own type, a composed child's type through the child's package, and a grandchild's
// through the grandchild's own package.
func TestCompose_recordsInputsTypes(t *testing.T) {
	// gofmt aligns the literal's fields, so compare with runs of space collapsed.
	root := strings.Join(strings.Fields(composeModuleStaged(t, transitiveTree())["internal/cmd/root/zz_root.go"]), " ")
	for _, want := range []string{
		"Inputs: reflect.TypeFor[RootInputs](),",
		"Inputs: reflect.TypeFor[childcli.ChildInputs](),",
		"Inputs: reflect.TypeFor[grandcli.GrandInputs](),",
	} {
		if !strings.Contains(root, want) {
			t.Errorf("root Definition lacks %q:\n%s", want, root)
		}
	}
	if n := strings.Count(root, "Inputs: reflect.TypeFor["); n != 3 {
		t.Errorf("root Definition records %d inputs types, want 3:\n%s", n, root)
	}
	if !strings.Contains(root, `"reflect"`) {
		t.Error("the root file doesn't import reflect")
	}
}

func TestExitStatusLiteral(t *testing.T) {
	if got := exitStatusLiteral(nil); got != "" {
		t.Errorf("no entries: %q", got)
	}
	got := exitStatusLiteral([]ExitStatusEntry{{Code: 0}, {Code: 3, Name: "not_found", Summary: `none "found"`, Retryable: true}})
	want := "ExitStatus: []rotini.ExitStatusDef{\n{Code: 0},\n{Code: 3, Name: \"not_found\", Summary: \"none \\\"found\\\"\", Retryable: true},\n},\n"
	if got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

func TestInputsTypeLiteral(t *testing.T) {
	if got := inputsTypeLiteral(""); got != "" {
		t.Errorf("no type: %q", got)
	}
	if got := inputsTypeLiteral("buildcli.BuildInputs"); got != "Inputs: reflect.TypeFor[buildcli.BuildInputs](),\n" {
		t.Errorf("got %q", got)
	}
}

// TestCompose_recordsOutputTypes pins the Definition's Output fields for composed commands:
// each names the type the command's own package declares, so DecodeOutput finds it, with the
// schema resolved against the composed spec's own named schemas.
func TestCompose_recordsOutputTypes(t *testing.T) {
	files := transitiveTree()
	files["cmd/grand/.rotini.spec.yaml"] = `version: 0.0.0
command:
  name: grand
  summary: the grandchild
  output:
    type: object
    properties:
      n: {type: integer}
`
	files["cmd/child/.rotini.spec.yaml"] = `version: 0.0.0
command:
  name: child
  summary: the child
  schemas:
    Item:
      type: object
      properties:
        id: {type: string}
  output: {type: object, properties: {ok: {type: boolean}}}
  commands:
    - name: list
      output: {$ref: "#/schemas/Item"}
      output_stream: true
    - $ref: ../grand/.rotini.spec.yaml
`
	root := strings.Join(strings.Fields(composeModuleStaged(t, files)["internal/cmd/root/zz_root.go"]), " ")
	for _, want := range []string{
		"Output: &rotini.OutputDef{Type: reflect.TypeFor[childcli.ChildOutput]()",
		"Output: &rotini.OutputDef{Type: reflect.TypeFor[childcli.ChildListOutput](), Schema: `",
		"Output: &rotini.OutputDef{Type: reflect.TypeFor[grandcli.GrandOutput]()",
		`"id":{"type":"string"}`,
		"Stream: true",
	} {
		if !strings.Contains(root, want) {
			t.Errorf("root Definition lacks %q:\n%s", want, root)
		}
	}
}
