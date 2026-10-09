package codegen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPages_golden pins every page of testdata/pages/spec.yaml in help, man and markdown, so
// group descriptions and their order, flag alignment, constraint notes, the stdin section and
// the passthrough note can't drift. Refresh with
// `go test ./internal/codegen -run Pages_golden -update`.
func TestPages_golden(t *testing.T) {
	dir := filepath.Join("testdata", "pages")
	body, err := os.ReadFile(filepath.Join(dir, "spec.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	gp, err := resolveTree(decodeSpecYAML(t, string(body)), filepath.Join(t.TempDir(), ".rotini.spec.yaml"), "example.com/ops")
	if err != nil {
		t.Fatal(err)
	}
	for _, feat := range []struct {
		desc docFeature
		ext  string
	}{
		{helpFeatureDesc, ".help.txt"},
		{manFeatureDesc, ".1"},
		{markdownFeatureDesc, ".md"},
	} {
		tmpl, err := parseDocTemplate(feat.desc.tmplFile, feat.desc.embedded)
		if err != nil {
			t.Fatal(err)
		}
		for _, n := range flattenFeature(gp, feat.desc) {
			render := renderDocText
			if feat.desc.manPages {
				render = renderManText
			}
			got, err := render(tmpl, n.data)
			if err != nil {
				t.Fatal(err)
			}
			file := strings.Join(append([]string{"ops"}, n.path...), "-") + feat.ext
			if *updateGolden {
				writeTestFile(t, dir, file, got)
				continue
			}
			want, err := os.ReadFile(filepath.Join(dir, file))
			if err != nil {
				t.Fatalf("%v (run with -update)", err)
			}
			if got != string(want) {
				t.Errorf("%s differs from the golden file:\n--- got ---\n%s\n--- want ---\n%s", file, got, want)
			}
		}
	}
}
