package codegen

import (
	"strings"
	"testing"
)

// TestInitialismIdents_generatedTypes proves the schema types keep their wire names while their
// Go names get the same casing: fields renamed, a synthesized nested type renamed with every
// reference to it, and the top-level names the rest of the program refers to left exactly as
// written.
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
