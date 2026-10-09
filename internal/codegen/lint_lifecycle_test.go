package codegen

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const lifecycleSpec = `version: 0.0.0
command:
  name: acme
  exit_status:
    - { code: 0, summary: ok }
    - { code: 1, summary: failed }
    - { code: 4, name: busy, summary: the store is locked, retryable: true }
  flags:
    - name: conf
      summary: the config file
      identifiers: [--conf]
      deprecated: use --config
      deprecated_since: 1.4.0
      removed_in: 2.0.0
      schema: {type: string}
    - name: config
      summary: the config file
      identifiers: [--config, --cfg]
      deprecated_identifiers: [--cfg]
      deprecated_identifiers_removed_in: {--cfg: 1.9.0}
      schema: {type: string}
  commands:
    - name: old
      summary: the old way
      deprecated: use new
      removed_in: 3.0.0
      arguments:
        - {name: what, summary: what, deprecated: unused, deprecated_since: 1.2.0, schema: {type: string}}
    - name: watch
      summary: watch the tasks
      output: {type: object, properties: {id: {type: integer}}}
      output_stream: true
`

// TestLifecyclePages pins the planned deprecation beside a deprecated item, the stream
// wording in OUTPUT, and exit-code names in man and markdown (help has no EXIT STATUS).
func TestLifecyclePages(t *testing.T) {
	dir, _ := emitModule(t, lifecycleSpec, outputConf)
	pages := manPages(t, dir)
	for name, subs := range map[string][]string{
		"help_acme.txt": {
			"(deprecated since 1.4.0, removed in 2.0.0: use --config)",
			"(deprecated, removed in 3.0.0: use new)",
		},
		"help_acme_old.txt":   {"(deprecated since 1.2.0: unused)"},
		"help_acme_watch.txt": {"a stream of object, one item at a time (JSON: one compact value per line; YAML: one document per item, each starting ---)"},
		"acme.1": {
			"(deprecated since 1.4.0, removed in 2.0.0: use \\-\\-config)",
			".TP\n\\fB4\\fR (busy)\nthe store is locked (retryable)\n",
		},
		"acme-watch.1":           {"Writes a stream of \\fBobject\\fR to standard output, one item at a time"},
		"markdown_acme.md":       {"*(deprecated since 1.4.0, removed in 2.0.0: use --config)*", "- `4` (`busy`) — the store is locked (retryable)"},
		"markdown_acme_watch.md": {"Writes a stream of `object` to stdout, one item at a time"},
		"markdown_acme_old.md":   {"*(deprecated since 1.2.0: unused)*"},
	} {
		page, ok := pages[name]
		if !ok {
			t.Fatalf("no page %s; have %v", name, keysOf(pages))
		}
		for _, sub := range subs {
			if !strings.Contains(page, sub) {
				t.Errorf("%s lacks %q:\n%s", name, sub, page)
			}
		}
	}
	if strings.Contains(pages["help_acme.txt"], "busy") {
		t.Error("help shows exit-code names; only man and markdown have an EXIT STATUS section")
	}
}

// TestReleaseProblems pins which planned removals a release fails: at or below it, not above
// it, a prerelease counting as its release, and per-identifier removals.
func TestReleaseProblems(t *testing.T) {
	spec := decodeSpecYAML(t, lifecycleSpec)
	for _, tt := range []struct {
		release string
		want    []string
	}{
		{"1.8.0", nil},
		{"1.9.0", []string{`flag "--config": "--cfg" is planned for removal in 1.9.0; remove it from the spec before releasing 1.9.0`}},
		{"2.0.0-rc.1", []string{
			`flag "--conf": is planned for removal in 2.0.0 (deprecated since 1.4.0); remove it before releasing 2.0.0-rc.1`,
			`flag "--config": "--cfg" is planned for removal in 1.9.0`,
		}},
		{"3.0.0", []string{
			`flag "--conf": is planned`,
			`flag "--config": "--cfg" is planned`,
			`command acme/old: is planned for removal in 3.0.0; remove it before releasing 3.0.0`,
		}},
	} {
		var got []string
		for _, p := range releaseProblems(spec, tt.release) {
			got = append(got, p.Error())
		}
		if len(got) != len(tt.want) {
			t.Errorf("release %s: problems = %q, want %d", tt.release, got, len(tt.want))
			continue
		}
		for i, w := range tt.want {
			if !strings.Contains(got[i], w) {
				t.Errorf("release %s: problem %d = %q, want it to contain %q", tt.release, i, got[i], w)
			}
		}
	}
}

// TestValidateRelease pins the release check in Validate: positioned in the file it is in,
// run over a local composed spec, and skipped without a release.
func TestValidateRelease(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "go.mod", "module example.com/acme\n\ngo 1.27\n")
	writeTestFile(t, dir, ".rotini.spec.yaml", lifecycleSpec+`    - $ref: ./kid/.rotini.spec.yaml
`)
	writeTestFile(t, dir, "kid/.rotini.spec.yaml", `version: 0.0.0
command:
  name: kid
  flags:
    - {name: x, identifiers: [--x], deprecated: gone, removed_in: 2.0.0, schema: {type: bool}}
`)
	writeTestFile(t, dir, ".rotini.conf.yaml", goldenConf)
	t.Chdir(dir)
	validate := func(release string) error {
		return NewProcessor("0.0.0").Validate(".rotini.spec.yaml", ".rotini.conf.yaml", false, "", release, nil, nil)
	}
	if err := validate(""); err != nil {
		t.Fatalf("without a release, nothing is checked: %v", err)
	}
	err := validate("2.0.0")
	if err == nil {
		t.Fatal("a removal due at the release passed")
	}
	msg := err.Error()
	for _, want := range []string{
		`.rotini.spec.yaml:20:`, `flag "--conf": is planned for removal in 2.0.0`,
		filepath.Join("kid", ".rotini.spec.yaml") + ":5:", `flag "--x": is planned for removal in 2.0.0`,
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("Validate --release 2.0.0 = %v, want %q", msg, want)
		}
	}
	if err := validate("two"); !errors.Is(err, errReleaseFormat) {
		t.Errorf("an unreadable release = %v, want errReleaseFormat", err)
	}
}

// TestReleaseEnv pins that the conf names the variable, and that a missing conf names none.
func TestReleaseEnv(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, ".rotini.spec.yaml", "version: 0.0.0\ncommand:\n  name: a\n")
	writeTestFile(t, dir, ".rotini.conf.yaml", "version: 0.0.0\nvalidate:\n  release_env: RELEASE\n")
	t.Chdir(dir)
	if got := ReleaseEnv(".rotini.spec.yaml", ".rotini.conf.yaml"); got != "RELEASE" {
		t.Errorf("ReleaseEnv = %q", got)
	}
	if err := os.Remove(".rotini.conf.yaml"); err != nil {
		t.Fatal(err)
	}
	if got := ReleaseEnv(".rotini.spec.yaml", ""); got != "" {
		t.Errorf("ReleaseEnv without a conf = %q", got)
	}
}

// TestStreamAudit pins the warning for a handler that writes items on a command whose output
// isn't marked as a stream, and its absence once marked.
func TestStreamAudit(t *testing.T) {
	spec := `version: 0.0.0
command:
  name: demo
  commands:
    - name: watch
      output: {type: object, properties: {id: {type: integer}}}
`
	dir := t.TempDir()
	writeTestFile(t, dir, "go.mod", "module example.com/demo\n\ngo 1.27\n")
	writeTestFile(t, dir, ".rotini.spec.yaml", spec)
	writeTestFile(t, dir, ".rotini.conf.yaml", goldenConf)
	t.Chdir(dir)
	gen := func() []string {
		var got []string
		if err := NewProcessor("0.0.0").Generate(".rotini.spec.yaml", ".rotini.conf.yaml", false, nil, func(n []error) {
			for _, e := range n {
				if strings.Contains(e.Error(), "WriteOutputItem") {
					got = append(got, e.Error())
				}
			}
		}); err != nil {
			t.Fatal(err)
		}
		return got
	}
	gen()
	appendToStub(t, filepath.Join(dir, "internal", "cmd", "demo"), "demo_watch.go", `
func (*demoWatchHandler) PostRun(ctx context.Context, rtx *rotini.Context) {
	_ = rtx.WriteOutputItem(DemoWatchOutput{})
}
`)
	want := `internal/cmd/demo/demo_watch.go:31: "demo watch" writes items with WriteOutputItem; declare output_stream: true on it, or it fails at run time`
	if got := gen(); len(got) != 1 || got[0] != want {
		t.Errorf("warnings = %q, want [%q]", got, want)
	}
	writeTestFile(t, dir, ".rotini.spec.yaml", spec+"      output_stream: true\n")
	if got := gen(); len(got) != 0 {
		t.Errorf("a marked stream still warns: %q", got)
	}
}

// TestContractVar pins generate.contract.go: the Contract variable is byte for byte the
// contract file, and works without one.
func TestContractVar(t *testing.T) {
	dir, _ := emitModule(t, lifecycleSpec, `version: 0.0.0
generate:
  contract:
    file: cli-contract.json
    go: true
  packages:
    - type: cmd
      file: internal/cmd/acme/zz_acme.go
      package: acme
`)
	file := readEmitted(t, dir, "cli-contract.json")
	cmd := readEmitted(t, dir, "internal/cmd/acme/zz_acme.go")
	if !strings.Contains(cmd, "var Contract = "+goRawString(file)+"\n") {
		t.Errorf("Contract is not the contract file, byte for byte:\n%s", cmd)
	}

	writeTestFile(t, dir, ".rotini.conf.yaml", `version: 0.0.0
generate:
  contract:
    go: true
  packages:
    - type: cmd
      file: internal/cmd/acme/zz_acme.go
      package: acme
`)
	if err := os.Remove(filepath.Join(dir, "cli-contract.json")); err != nil {
		t.Fatal(err)
	}
	if err := NewProcessor("0.0.0").Generate(".rotini.spec.yaml", ".rotini.conf.yaml", false, nil, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(readEmitted(t, dir, "internal/cmd/acme/zz_acme.go"), "var Contract = ") {
		t.Error("go: true without a file wrote no Contract")
	}
	if _, err := os.Stat(filepath.Join(dir, "cli-contract.json")); !os.IsNotExist(err) {
		t.Error("go: true without a file wrote a contract file")
	}
	skipUnlessCompiling(t)
	if out, err := goBuild(t, dir); err != nil {
		t.Fatalf("the generated module does not build:\n%s", out)
	}
}
