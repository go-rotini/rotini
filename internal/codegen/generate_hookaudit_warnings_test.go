package codegen

import (
	"strings"
	"testing"
)

// TestHookAudit_keepsEarlierWarnings pins that the hook audit adds to the warnings earlier
// steps recorded: a config schema warning still shows once the module has handler files.
func TestHookAudit_keepsEarlierWarnings(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "go.mod", "module example.com/demo\n\ngo 1.26\n")
	writeTestFile(t, dir, ".rotini.spec.yaml", `version: 0.0.0
command:
  name: demo
  config_files:
    - name: app
      path: ./app.yaml
      schema: { $ref: Missing }
  config:
    - name: region
      schema: { type: string }
`)
	writeTestFile(t, dir, ".rotini.conf.yaml", goldenConf+"  schemas:\n    config:\n      dir: schemas\n")
	t.Chdir(dir)

	gen := func() []error {
		var notices []error
		if err := NewProcessor("0.0.0").Generate(".rotini.spec.yaml", ".rotini.conf.yaml", false,
			func(string, error) {}, func(n []error) { notices = append(notices, n...) }); err != nil {
			t.Fatalf("Generate: %v", err)
		}
		return notices
	}
	gen() // the first run writes the handler stubs the audit reads on the second
	notices := gen()
	found := false
	for _, n := range notices {
		found = found || strings.Contains(n.Error(), `config file "app": its schema names a schema the spec doesn't declare`)
	}
	if !found {
		t.Errorf("notices = %v, want the config schema warning", notices)
	}
}
