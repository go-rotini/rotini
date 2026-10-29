package codegen

import (
	"strings"
	"testing"
)

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

// TestComposedTopics_nameOfAMountedCommand pins that a root topic named like a command mounted
// with `$ref` is an error, reported once, whether the name comes from the child spec or from
// the parent beside the `$ref`: the page resolvers would otherwise have the name twice, and the
// generated code would not compile.
func TestComposedTopics_nameOfAMountedCommand(t *testing.T) {
	for _, tc := range []struct {
		name, topic, mount string
	}{
		{"child's own name", "child", "    - $ref: child.spec.yaml\n"},
		{"parent's alias", "kiddo", "    - $ref: child.spec.yaml\n      aliases: [kiddo]\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeTestFile(t, dir, "go.mod", "module example.com/v\n\ngo 1.26\n")
			writeTestFile(t, dir, ".rotini.spec.yaml", "version: 0.0.0\ncommand:\n  name: demo\n  topics:\n    - name: "+
				tc.topic+"\n      summary: a topic\n      body: text\n  commands:\n"+tc.mount)
			writeTestFile(t, dir, "child.spec.yaml", "version: 0.0.0\ncommand:\n  name: child\n  summary: the child\n")
			writeTestFile(t, dir, ".rotini.conf.yaml", goldenConf)
			t.Chdir(dir)
			err := NewProcessor("0.0.0").Validate(".rotini.spec.yaml", ".rotini.conf.yaml", false, "", "",
				func(string, error) {}, func([]error) {})
			if err == nil {
				t.Fatal("Validate accepted a topic named like a mounted command")
			}
			if got := strings.Count(err.Error(), `topic "`+tc.topic+`"`); got != 1 {
				t.Errorf("Validate reported the clash %d times, want once:\n%v", got, err)
			}
		})
	}
}
