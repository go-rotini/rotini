package codegen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The fuzz targets feed arbitrary bytes through decoding, schema validation, lint and source
// location. The invariant is that validation never panics and never fails with an empty
// message.

// FuzzValidateSpec drives the whole validate stage with arbitrary spec bytes.
func FuzzValidateSpec(f *testing.F) {
	for _, seed := range []string{
		"version: 0.0.0\ncommand:\n  name: demo\n",
		"version: 0.0.0\ncommand:\n  name: demo\n  flags:\n    - name: a\n      schema: {type: int, minimum: 1}\n",
		"version: 0.0.0\ncommand: {name: d, commands: [{name: c, timeout: 10s}]}\n",
		"version: 0.0.0\ncommand:\n  name: d\n  config_files:\n    - name: c\n      discover: {strategy: xdg, file: f}\n",
		"{\"version\":\"0.0.0\",\"command\":{\"name\":\"d\"}}",
		"version: 0.0.0\ncommand:\n  name: d\n  schemas:\n    X: {type: object}\n  output: {$ref: '#/schemas/Y'}\n",
		"",
		"\x00\x00\x00",
		"version: 0.0.0\ncommand:\n  name: d\n  flags:\n    - {name: p, schema: {type: string, pattern: '(('}}\n",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, spec string) {
		dir := t.TempDir()
		mustWrite(t, filepath.Join(dir, "go.mod"), "module example.com/f\n\ngo 1.26\n")
		mustWrite(t, filepath.Join(dir, ".rotini.spec.yaml"), spec)
		mustWrite(t, filepath.Join(dir, ".rotini.conf.yaml"), lintFixtureConf)
		t.Chdir(dir)

		err := NewProcessor("0.0.0").Validate(".rotini.spec.yaml", ".rotini.conf.yaml", false, "collect",
			func(string, error) {}, func([]error) {})
		if err != nil && strings.TrimSpace(err.Error()) == "" {
			t.Fatalf("validate rejected the document with an empty message")
		}
	})
}

// FuzzValidateConf is FuzzValidateSpec for the other document, against a fixed valid spec.
func FuzzValidateConf(f *testing.F) {
	for _, seed := range []string{
		lintFixtureConf,
		"version: 0.0.0\n",
		"version: 0.0.0\ngenerate: {packages: [{type: cmd, file: a/b.go, package: b}]}\n",
		"version: 0.0.0\ngenerate:\n  features:\n    - {type: help, enabled: true, embed: true, embed_dir: x}\n",
		"{\"version\":\"0.0.0\"}",
		"",
		"generate: [",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, conf string) {
		dir := t.TempDir()
		mustWrite(t, filepath.Join(dir, "go.mod"), "module example.com/f\n\ngo 1.26\n")
		mustWrite(t, filepath.Join(dir, ".rotini.spec.yaml"), "version: 0.0.0\ncommand:\n  name: demo\n")
		mustWrite(t, filepath.Join(dir, ".rotini.conf.yaml"), conf)
		t.Chdir(dir)

		err := NewProcessor("0.0.0").Validate(".rotini.spec.yaml", ".rotini.conf.yaml", false, "collect",
			func(string, error) {}, func([]error) {})
		if err != nil && strings.TrimSpace(err.Error()) == "" {
			t.Fatalf("validate rejected the conf with an empty message")
		}
	})
}

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}
