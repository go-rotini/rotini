package codegen

import (
	"strings"
	"testing"
)

// TestCompose_recordsInputsTypes pins the Definition's Inputs fields across composition: the
// root's own type, a composed child's type through the child's package, and none for a
// grandchild whose package the root doesn't import.
func TestCompose_recordsInputsTypes(t *testing.T) {
	// gofmt aligns the literal's fields, so compare with runs of space collapsed.
	root := strings.Join(strings.Fields(composeModuleStaged(t, transitiveTree())["internal/cmd/root/zz_root.go"]), " ")
	for _, want := range []string{
		"Inputs: reflect.TypeFor[RootInputs](),",
		"Inputs: reflect.TypeFor[childcli.ChildInputs](),",
	} {
		if !strings.Contains(root, want) {
			t.Errorf("root Definition lacks %q:\n%s", want, root)
		}
	}
	if n := strings.Count(root, "Inputs: reflect.TypeFor["); n != 2 {
		t.Errorf("root Definition records %d inputs types, want 2 (none for the grandchild):\n%s", n, root)
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
