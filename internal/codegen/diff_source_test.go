package codegen

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-rotini/rotini/internal/contractdiff"
)

const diffSourceSpec = `version: 0.0.0
command:
  name: app
  commands:
    - name: compact
      summary: compact the store
    - name: list
      summary: list things
`

const diffSourceConf = `version: 0.0.0
generate:
  contract:
    file: cli-contract.json
  packages:
    - type: cmd
      file: internal/cmd/app/zz_app.go
      package: app
`

// diffModule writes a module at dir holding the spec and conf, and generates it once so its
// contract file exists.
func diffModule(t *testing.T, dir, spec, conf string) {
	t.Helper()
	writeTestFile(t, dir, "go.mod", "module example.com/app\n\ngo 1.27\n")
	writeTestFile(t, dir, ".rotini.spec.yaml", spec)
	writeTestFile(t, dir, ".rotini.conf.yaml", conf)
	t.Chdir(dir)
	if err := NewProcessor("0.0.0").Generate(".rotini.spec.yaml", ".rotini.conf.yaml", false, nil, nil); err != nil {
		t.Fatalf("Generate: %v", err)
	}
}

// The contract built in memory is the file generate writes, byte for byte.
func TestProcessorContract_matchesGenerate(t *testing.T) {
	dir := t.TempDir()
	diffModule(t, dir, diffSourceSpec, diffSourceConf)
	built, err := NewProcessor("0.0.0").Contract(".rotini.spec.yaml", ".rotini.conf.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if written := readTestFile(t, filepath.Join(dir, "cli-contract.json")); string(built) != written {
		t.Errorf("Contract differs from the file generate writes:\n%s\n---\n%s", built, written)
	}
	// It needs no contract file in the conf.
	if _, err := NewProcessor("0.0.0").Contract(".rotini.spec.yaml", ""); err != nil {
		t.Errorf("Contract without a conf: %v", err)
	}
}

func removeCompact(spec string) string {
	return strings.Replace(spec, "    - name: compact\n      summary: compact the store\n", "", 1)
}

func diffRules(r contractdiff.Report) []string {
	var out []string
	for _, f := range r.Findings {
		out = append(out, f.Rule+" "+f.Where)
	}
	return out
}

func TestProcessorDiff_fileSource(t *testing.T) {
	dir := t.TempDir()
	diffModule(t, dir, diffSourceSpec, diffSourceConf)
	if err := os.Rename("cli-contract.json", "old.json"); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, dir, ".rotini.spec.yaml", removeCompact(diffSourceSpec))
	p := NewProcessor("0.0.0")

	r, err := p.Diff(".rotini.spec.yaml", ".rotini.conf.yaml", "old.json", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if got := diffRules(r); len(got) != 1 || got[0] != "COMMAND_NO_DELETE app compact" {
		t.Errorf("findings = %v", got)
	}

	// Both contracts given: no spec needed.
	writeTestFile(t, dir, "new.json", string(mustContract(t, p)))
	if r, err := p.Diff("", ".rotini.conf.yaml", "old.json", "new.json", ""); err != nil || len(r.Findings) != 1 {
		t.Errorf("file to file = %v, %v", diffRules(r), err)
	}

	writeTestFile(t, dir, "bad.json", `{`)
	writeTestFile(t, dir, "other.json", `{"format": "something/1"}`)
	for _, tc := range []struct{ source, want string }{
		{"missing.json", "contract missing.json: no such file"},
		{"bad.json", "not JSON"},
		{"other.json", `format "something/1"`},
	} {
		if _, err := p.Diff(".rotini.spec.yaml", ".rotini.conf.yaml", tc.source, "", ""); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %q", tc.source, err, tc.want)
		}
	}
	if _, err := p.Diff(".rotini.spec.yaml", ".rotini.conf.yaml", "old.json", "", "2.0"); err == nil {
		t.Error("a release that isn't X.Y.Z was accepted")
	}
}

func mustContract(t *testing.T, p *Processor) []byte {
	t.Helper()
	b, err := p.Contract(".rotini.spec.yaml", ".rotini.conf.yaml")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// git:<ref> reads the committed contract with git show from the module root, which need not be
// the repository's root.
func TestProcessorDiff_gitSource(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	repo := t.TempDir()
	mod := filepath.Join(repo, "tools", "app")
	diffModule(t, mod, diffSourceSpec, diffSourceConf)
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo, "-c", "user.name=t", "-c", "user.email=t@t", "-c", "commit.gpgsign=false"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	git("add", ".")
	git("commit", "-q", "-m", "v1")
	writeTestFile(t, mod, ".rotini.spec.yaml", removeCompact(diffSourceSpec))
	if err := os.MkdirAll(filepath.Join(mod, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(filepath.Join(mod, "sub")) // module-root-relative wherever it runs
	p := NewProcessor("0.0.0")
	spec, conf := filepath.Join(mod, ".rotini.spec.yaml"), filepath.Join(mod, ".rotini.conf.yaml")

	for _, source := range []string{"git:HEAD", "git:HEAD:cli-contract.json"} {
		r, err := p.Diff(spec, conf, source, "", "")
		if err != nil {
			t.Fatalf("%s: %v", source, err)
		}
		if got := diffRules(r); len(got) != 1 || got[0] != "COMMAND_NO_DELETE app compact" {
			t.Errorf("%s: findings = %v", source, got)
		}
	}

	noContract := filepath.Join(mod, "plain.conf.yaml")
	writeTestFile(t, mod, "plain.conf.yaml", strings.Replace(diffSourceConf, "  contract:\n    file: cli-contract.json\n", "", 1))
	for _, tc := range []struct{ conf, source, want string }{
		{conf, "git:no-such-ref", "git show no-such-ref:cli-contract.json"},
		{conf, "git:HEAD:missing.json", "missing.json doesn't exist at HEAD; commit a contract first"},
		{noContract, "git:HEAD", "set generate.contract.file"},
		{conf, "git:HEAD:../x.json", "must be module-root-relative"},
		{conf, "git:", "names no revision"},
	} {
		if _, err := p.Diff(spec, tc.conf, tc.source, "", ""); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %q", tc.source, err, tc.want)
		}
	}

	outside := t.TempDir()
	diffModule(t, outside, diffSourceSpec, diffSourceConf)
	if _, err := p.Diff(".rotini.spec.yaml", ".rotini.conf.yaml", "git:HEAD", "", ""); err == nil || !strings.Contains(err.Error(), "not in a git repository") {
		t.Errorf("outside a repository: err = %v", err)
	}
}

func TestProcessorDiff_modSource(t *testing.T) {
	dir := t.TempDir()
	diffModule(t, dir, diffSourceSpec, diffSourceConf)
	cache := t.TempDir()
	writeTestFile(t, cache, "cli-contract.json", readTestFile(t, filepath.Join(dir, "cli-contract.json")))
	orig := moduleDirFunc
	t.Cleanup(func() { moduleDirFunc = orig })
	moduleDirFunc = func(module, version string) (string, error) {
		if module != "example.com/app" || version != "v1.0.0" {
			return "", errors.New("module example.com/other@v9.9.9 not available; add it to go.mod (go get example.com/other@v9.9.9)")
		}
		return cache, nil
	}
	writeTestFile(t, dir, ".rotini.spec.yaml", removeCompact(diffSourceSpec))
	p := NewProcessor("0.0.0")
	r, err := p.Diff(".rotini.spec.yaml", ".rotini.conf.yaml", "mod://example.com/app@v1.0.0/cli-contract.json", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if got := diffRules(r); len(got) != 1 || got[0] != "COMMAND_NO_DELETE app compact" {
		t.Errorf("findings = %v", got)
	}
	for _, tc := range []struct{ source, want string }{
		{"mod://example.com/app@v1.0.0/missing.json", "missing.json doesn't exist in example.com/app@v1.0.0"},
		{"mod://example.com/app@v1.0.0", "names no contract file"},
		{"mod://example.com/other@v9.9.9/cli-contract.json", "go get"},
		{"mod://example.com/app/cli-contract.json", "invalid module ref"},
	} {
		if _, err := p.Diff(".rotini.spec.yaml", ".rotini.conf.yaml", tc.source, "", ""); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %q", tc.source, err, tc.want)
		}
	}
}

// The conf's diff.accept entries acknowledge findings, and the conf is checked first.
func TestProcessorDiff_accept(t *testing.T) {
	dir := t.TempDir()
	diffModule(t, dir, diffSourceSpec, diffSourceConf)
	if err := os.Rename("cli-contract.json", "old.json"); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, dir, ".rotini.spec.yaml", removeCompact(diffSourceSpec))
	writeTestFile(t, dir, ".rotini.conf.yaml", diffSourceConf+`diff:
  accept:
    - {rule: COMMAND_NO_DELETE, where: app compact, reason: replaced by purge}
    - {rule: FLAG_NO_DELETE, where: app --gone, reason: stale}
`)
	p := NewProcessor("0.0.0")
	r, err := p.Diff(".rotini.spec.yaml", ".rotini.conf.yaml", "old.json", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if r.Findings[0].Accepted == nil || r.Findings[0].Accepted.Reason != "replaced by purge" {
		t.Errorf("the finding isn't accepted: %+v", r.Findings[0])
	}
	if len(r.UnmatchedAccepts) != 1 || r.UnmatchedAccepts[0].Where != "app --gone" {
		t.Errorf("unmatched = %+v", r.UnmatchedAccepts)
	}

	writeTestFile(t, dir, "new.json", string(mustContract(t, p)))
	writeTestFile(t, dir, ".rotini.conf.yaml", diffSourceConf+"diff:\n  accept:\n    - {rule: COMAND_NO_DELETE, where: app compact, reason: x}\n")
	if _, err := p.Diff("", ".rotini.conf.yaml", "old.json", "new.json", ""); err == nil || !strings.Contains(err.Error(), `did you mean "COMMAND_NO_DELETE"`) {
		t.Errorf("a mistyped rule: err = %v", err)
	}
}
