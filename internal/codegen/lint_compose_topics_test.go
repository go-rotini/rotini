package codegen

import "testing"

// TestComposedTopics_warns pins the warning for a spec mounted with `$ref` that declares
// topics: it is placed at the child's topics, in the child's file, and names the parent.
func TestComposedTopics_warns(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "go.mod", "module example.com/v\n\ngo 1.26\n")
	writeTestFile(t, dir, ".rotini.spec.yaml", `version: 0.0.0
command:
  name: demo
  commands:
    - $ref: child.spec.yaml
`)
	writeTestFile(t, dir, "child.spec.yaml", `version: 0.0.0
command:
  name: child
  summary: the child
  topics:
    - name: filters
      summary: how filters work
      body: A filter narrows a list.
`)
	writeTestFile(t, dir, ".rotini.conf.yaml", goldenConf)
	t.Chdir(dir)
	var warnings []error
	err := NewProcessor("0.0.0").Validate(".rotini.spec.yaml", ".rotini.conf.yaml", false, "", "",
		func(string, error) {},
		func(w []error) { warnings = append(warnings, w...) })
	if err != nil {
		t.Fatalf("Validate = %v", err)
	}
	const want = "spec: child.spec.yaml:6:5: command child: declares `topics`, which `help <topic>` can't reach: " +
		"this spec is mounted with `$ref` from .rotini.spec.yaml, and only the root spec's topics are part of the program; declare them there"
	var found bool
	for _, w := range warnings {
		found = found || w.Error() == want
	}
	if !found {
		t.Errorf("warnings = %v\nwant %q", warnings, want)
	}
}
