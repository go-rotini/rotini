package codegen

import (
	"strings"
	"testing"
)

// TestInitialismIdents_generatedTypes pins initialism casing on generated fields and nested
// types (with their references), while JSON tags and fixed top-level names are unchanged.
func TestInitialismIdents_generatedTypes(t *testing.T) {
	t.Parallel()
	src := "type Resource struct {\n\tApiVersion string `json:\"apiVersion\"`\n\tSpec *ResourceApiSpec `json:\"spec\"`\n}\n\n" +
		"type ResourceApiSpec struct {\n\tHostIp string `json:\"hostIp\"`\n\tHostIP string `json:\"HostIP\"`\n}\n\ntype ApiThing struct{}\n"
	got, err := initialismIdents(src, map[string]bool{"Resource": true, "ApiThing": true})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"APIVersion string `json:\"apiVersion\"`",
		"Spec *ResourceAPISpec",
		"type ResourceAPISpec struct",
		"HostIp string", // HostIP is already declared: renaming would collide, so it is left
		"type ApiThing struct{}",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}
