package codegen

import (
	"strings"
	"testing"
)

// TestDuplicateKeyProblems pins that validate rejects a key repeated in one mapping of a spec
// or conf, in every format, positioned at the repeat; and that a YAML merge override, which is
// how merges work, is not reported.
func TestDuplicateKeyProblems(t *testing.T) {
	const okConf = "version: 0.0.0\n"
	tests := []struct {
		name, specFile, spec, confFile, conf string
		want                                 []string // substrings of the joined problems; none means valid
	}{
		{
			name:     "yaml block mapping",
			specFile: ".rotini.spec.yaml",
			spec: `version: 0.0.0
command:
  name: demo
  flags:
    - name: debug
      schema: { type: bool }
      schema: { type: string }
`,
			want: []string{`spec: .rotini.spec.yaml:7:7: /command/flags/0/schema: duplicate key "schema" (first at line 6)`},
		},
		{
			name:     "yaml flow mapping",
			specFile: ".rotini.spec.yaml",
			spec: `version: 0.0.0
command: { name: demo, summary: a, summary: b }
`,
			want: []string{`spec: .rotini.spec.yaml:2:`, `/command/summary: duplicate key "summary" (first at line 2)`},
		},
		{
			name:     "yaml conf",
			specFile: ".rotini.spec.yaml",
			spec:     "version: 0.0.0\ncommand:\n  name: demo\n",
			confFile: ".rotini.conf.yaml",
			conf:     "version: 0.0.0\nvalidate:\n  fail: all\n  fail: fast\n",
			want:     []string{`conf: .rotini.conf.yaml:4:3: /validate/fail: duplicate key "fail" (first at line 3)`},
		},
		{
			name:     "json",
			specFile: ".rotini.spec.json",
			spec:     "{\n  \"version\": \"0.0.0\",\n  \"command\": {\"name\": \"demo\", \"summary\": \"a\", \"summary\": \"b\"}\n}\n",
			want:     []string{`spec: .rotini.spec.json:3:`, `key "summary": repeats a key of the same mapping`},
		},
		{
			name:     "jsonc",
			specFile: ".rotini.spec.jsonc",
			spec:     "{\n  // the spec\n  \"version\": \"0.0.0\",\n  \"version\": \"0.0.0\",\n  \"command\": {\"name\": \"demo\"}\n}\n",
			want:     []string{`spec: .rotini.spec.jsonc:4:3: key "version": repeats a key of the same mapping; the second would replace the first`},
		},
		{
			name:     "toml",
			specFile: ".rotini.spec.toml",
			spec:     "version = \"0.0.0\"\n\n[command]\nname = \"demo\"\nname = \"again\"\n",
			want:     []string{`spec: .rotini.spec.toml:5:`, `key "name": repeats a key`},
		},
		{
			name:     "a yaml merge override is not a duplicate",
			specFile: ".rotini.spec.yaml",
			spec: `version: 0.0.0
command:
  name: demo
  flags:
    - &base
      name: debug
      schema: { type: bool }
    - <<: *base
      name: trace
`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Chdir(dir)
			writeTestFile(t, dir, tt.specFile, tt.spec)
			confPath := ""
			if tt.confFile != "" {
				writeTestFile(t, dir, tt.confFile, tt.conf)
				confPath = tt.confFile
			} else {
				writeTestFile(t, dir, ".rotini.conf.yaml", okConf)
				confPath = ".rotini.conf.yaml"
			}
			var reported error
			err := NewProcessor("0.0.0").Validate(tt.specFile, confPath, false, "", "", func(_ string, e error) {
				if e != nil {
					reported = e
				}
			}, func([]error) {})
			if err == nil {
				err = reported
			}
			if len(tt.want) == 0 {
				if err != nil {
					t.Fatalf("Validate: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("Validate passed a document with a duplicate key")
			}
			for _, want := range tt.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("problems = %q, want them to contain %q", err.Error(), want)
				}
			}
			if strings.Contains(err.Error(), "rotini bug") {
				t.Errorf("a duplicate key was reported as a rotini bug: %v", err)
			}
		})
	}
}

// TestDuplicateKeyProblems_composedChild pins that a repeated key in a locally composed child
// spec is reported in the child's own file.
func TestDuplicateKeyProblems_composedChild(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeTestFile(t, dir, ".rotini.spec.yaml", "version: 0.0.0\ncommand:\n  name: demo\n  commands:\n    - $ref: ./child.yaml\n")
	writeTestFile(t, dir, "child.yaml", "version: 0.0.0\ncommand:\n  name: child\n  summary: a\n  summary: b\n")
	writeTestFile(t, dir, ".rotini.conf.yaml", "version: 0.0.0\n")
	err := NewProcessor("0.0.0").Validate(".rotini.spec.yaml", ".rotini.conf.yaml", false, "", "", func(string, error) {}, func([]error) {})
	if err == nil || !strings.Contains(err.Error(), `spec: child.yaml:5:3: /command/summary: duplicate key "summary" (first at line 4)`) {
		t.Errorf("Validate = %v, want the child's duplicate key", err)
	}
}
