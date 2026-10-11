package rotini

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/go-rotini/rotini"
	"github.com/go-rotini/rotini/internal/codegen"
)

// `rotini explain` prints a key's description and facts from the embedded schemas, and a
// mistyped key is a usage error naming the nearest one.
func TestCLI_explain(t *testing.T) {
	p, out, _ := newTestCLI(t)
	code, err := p.Run([]string{"explain", "command.flags.role"})
	if err != nil || code != 0 {
		t.Fatalf("Run: %d, %v", code, err)
	}
	for _, want := range []string{"spec: command.flags.role\n", "  type:     string\n", `"machine-output"`, "'dry-run' marks the bool flag"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}

	p, out, _ = newTestCLI(t)
	if _, err := p.Run([]string{"explain", "version"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "spec: version\n") || !strings.Contains(out.String(), "conf: version\n") {
		t.Errorf("a key in both documents isn't shown for each:\n%s", out.String())
	}

	p, _, _ = newTestCLI(t)
	_, err = p.Run([]string{"explain", "command.flgs"})
	if err == nil || !strings.Contains(err.Error(), `did you mean "command.flags"?`) || !errors.Is(err, rotini.ErrUsage) {
		t.Errorf("err = %v, want a usage error naming command.flags", err)
	}
}

// The key argument completes one segment at a time, with no space after a key that has keys
// below it.
func TestCLI_explainCompletion(t *testing.T) {
	p, out, _ := newTestCLI(t)
	if _, err := p.Run([]string{"__complete", "explain", "command.flags.ro"}); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if !slices.Contains(lines, "command.flags.role") || !slices.Contains(lines, "command.flags.role_value") {
		t.Errorf("candidates %q", lines)
	}
	p, out, _ = newTestCLI(t)
	if _, err := p.Run([]string{"__complete", "explain", "command.fla"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "command.flags.\n") || !strings.Contains(out.String(), "nospace") {
		t.Errorf("a key with keys below it should end in . with no space:\n%s", out.String())
	}
}

// `rotini validate --format json` writes one object listing every problem on stdout, with the
// hint split from the message, and exits 1 only for an error.
func TestCLI_validateJSON(t *testing.T) {
	for _, tt := range []struct {
		name     string
		failed   error
		warnings []error
		code     int
	}{
		{"errors", errors.Join(errors.New("first; fix it"), errors.New("second")), []error{errAdvisory}, 1},
		{"warnings only", nil, []error{errAdvisory}, 0},
		{"clean", nil, nil, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p, out, errb := newTestCLI(t)
			p.WithDependency(validateDep, codegen.ValidateFn(
				func(_, _ string, _ bool, _, _ string, _ func(string, error), onWarnings func([]error)) error {
					onWarnings(tt.warnings)
					return tt.failed
				}))
			code, _ := p.Run([]string{"validate", "a.yaml", "--format", "json"})
			if code != tt.code {
				t.Errorf("exit = %d, want %d", code, tt.code)
			}
			if errb.Len() != 0 {
				t.Errorf("stderr = %q, want nothing", errb.String())
			}
			var got struct {
				Problems []map[string]any `json:"problems"`
			}
			if err := json.Unmarshal(out.Bytes(), &got); err != nil {
				t.Fatalf("stdout isn't one JSON object: %v\n%s", err, out.String())
			}
			if got.Problems == nil {
				t.Fatal("problems is missing; it is always a list")
			}
			n := len(tt.warnings)
			if tt.failed != nil {
				n += 2
				if got.Problems[0]["message"] != "first" || got.Problems[0]["hint"] != "fix it" || got.Problems[0]["severity"] != "error" {
					t.Errorf("first problem %v", got.Problems[0])
				}
			}
			if len(got.Problems) != n {
				t.Errorf("%d problems, want %d: %v", len(got.Problems), n, got.Problems)
			}
		})
	}

	p, _, _ := newTestCLI(t)
	if _, err := p.Run([]string{"validate", "a.yaml", "--format", "json", "--watch"}); err == nil || !errors.Is(err, rotini.ErrUsage) {
		t.Errorf("--format json --watch: err = %v, want a usage error", err)
	}
}
