package rotini

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-rotini/rotini/internal/codegen"
	"github.com/go-rotini/rotini/internal/contractdiff"
)

var (
	breakingFinding = contractdiff.Finding{Severity: contractdiff.Breaking, Rule: contractdiff.RuleCommandNoDelete, Where: "app compact", Message: "command removed"}
	possibleFinding = contractdiff.Finding{Severity: contractdiff.PossiblyBreaking, Rule: contractdiff.RuleInputDefaultChanged, Where: "app --n", Message: "default 1 → 2"}
)

// report builds a report the way contractdiff does, counted.
func report(findings []contractdiff.Finding, unmatched ...contractdiff.Accept) contractdiff.Report {
	r := contractdiff.Report{Findings: findings, UnmatchedAccepts: unmatched}
	for _, f := range findings {
		switch f.Severity {
		case contractdiff.Breaking:
			r.Summary.Breaking++
		case contractdiff.PossiblyBreaking:
			r.Summary.PossiblyBreaking++
		}
	}
	return r
}

type diffCall struct{ spec, conf, old, new, release string }

// diffCLI is the CLI with a diff double that records its call and returns r or err.
func diffCLI(t *testing.T, r contractdiff.Report, err error) (run func(argv ...string) (int, string, string), call *diffCall) {
	t.Helper()
	call = &diffCall{}
	return func(argv ...string) (int, string, string) {
		p, out, errb := newTestCLI(t)
		p.WithOutputChecks(true).WithDependency(diffDep, codegen.DiffFn(func(spec, conf, oldSource, newSource, release string) (contractdiff.Report, error) {
			*call = diffCall{spec, conf, oldSource, newSource, release}
			return r, err
		}))
		code, _ := p.Run(argv)
		return code, out.String(), errb.String()
	}, call
}

func TestCLI_diffExitCodes(t *testing.T) {
	tests := []struct {
		name   string
		report contractdiff.Report
		argv   []string
		code   int
	}{
		{"no changes", report(nil), nil, 0},
		{"breaking", report([]contractdiff.Finding{breakingFinding}), nil, 2},
		{"breaking, --fail-on never", report([]contractdiff.Finding{breakingFinding}), []string{"--fail-on", "never"}, 0},
		{"possibly breaking", report([]contractdiff.Finding{possibleFinding}), nil, 0},
		{"possibly breaking, --fail-on possibly", report([]contractdiff.Finding{possibleFinding}), []string{"--fail-on", "possibly"}, 2},
		{"unmatched acknowledgement", report(nil, contractdiff.Accept{Rule: "FLAG_NO_DELETE", Where: "app --x", Reason: "r"}), nil, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			run, _ := diffCLI(t, tt.report, nil)
			code, _, stderr := run(append([]string{"diff", "old.json", "new.json", "--spec", "s.yaml"}, tt.argv...)...)
			if code != tt.code {
				t.Errorf("exit = %d, want %d; stderr:\n%s", code, tt.code, stderr)
			}
		})
	}

	run, _ := diffCLI(t, contractdiff.Report{}, errors.New("contract old.json: no such file"))
	if code, _, stderr := run("diff", "old.json", "--spec", "s.yaml"); code != 1 || !strings.Contains(stderr, "Error: contract old.json: no such file") {
		t.Errorf("an error: exit %d, stderr %q", code, stderr)
	}
}

func TestCLI_diffOutput(t *testing.T) {
	r := report([]contractdiff.Finding{breakingFinding}, contractdiff.Accept{Rule: "FLAG_NO_DELETE", Where: "app --x", Reason: "r"})

	run, _ := diffCLI(t, r, nil)
	_, stdout, stderr := run("diff", "old.json", "--spec", "s.yaml")
	if !strings.Contains(stdout, "Breaking:\n  COMMAND_NO_DELETE  app compact  command removed") || !strings.Contains(stdout, "1 breaking, 0 possibly breaking") {
		t.Errorf("text output:\n%s", stdout)
	}
	if !strings.Contains(stderr, `Warning: diff.accept entry FLAG_NO_DELETE at "app --x" matched no change`) {
		t.Errorf("stderr = %q, want the unmatched entry named", stderr)
	}

	// JSON is checked against the command's declared output (WithOutputChecks).
	_, stdout, stderr = run("diff", "old.json", "--spec", "s.yaml", "--format", "json")
	var doc struct {
		Findings []struct {
			Rule string `json:"rule"`
		} `json:"findings"`
		UnmatchedAccepts []struct {
			Rule string `json:"rule"`
		} `json:"unmatched_accepts"`
		Summary struct {
			Breaking int `json:"breaking"`
		} `json:"summary"`
	}
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatalf("not JSON: %v\n%s\n%s", err, stdout, stderr)
	}
	if len(doc.Findings) != 1 || doc.Findings[0].Rule != "COMMAND_NO_DELETE" || doc.Summary.Breaking != 1 || len(doc.UnmatchedAccepts) != 1 {
		t.Errorf("json = %s", stdout)
	}
}

// diff's spec is a flag; it must be found only when the new contract is built from it.
func TestCLI_diffSpecAndRelease(t *testing.T) {
	t.Chdir(t.TempDir())
	run, call := diffCLI(t, report(nil), nil)
	if code, _, stderr := run("diff", "old.json", "new.json"); code != 0 || call.spec != "" || call.new != "new.json" {
		t.Errorf("both contracts, no spec: exit %d, call %+v, stderr %q", code, *call, stderr)
	}
	*call = diffCall{}
	if code, _, stderr := run("diff", "old.json"); code != 1 || call.old != "" || !strings.Contains(stderr, "spec") {
		t.Errorf("no spec to build from: exit %d, call %+v, stderr %q", code, *call, stderr)
	}

	if err := os.WriteFile(filepath.Join(".", ".rotini.conf.yaml"), []byte("version: 0.0.0\nvalidate:\n  release_env: ROTINI_TEST_RELEASE\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ROTINI_TEST_RELEASE", "2.0.0")
	if run("diff", "old.json", "--spec", "s.yaml"); call.release != "2.0.0" {
		t.Errorf("release from the env = %q", call.release)
	}
	if run("diff", "old.json", "--spec", "s.yaml", "--release", "3.0.0"); call.release != "3.0.0" {
		t.Errorf("--release beats the env: %q", call.release)
	}
	if code, _, stderr := run("diff", "old.json", "--spec", "s.yaml", "--release", "next"); code != 1 || !strings.Contains(stderr, `--release "next"`) {
		t.Errorf("a bad --release: exit %d, stderr %q", code, stderr)
	}
}
