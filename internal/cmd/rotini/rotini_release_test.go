package rotini

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-rotini/rotini/internal/codegen"
)

// TestCLI_validateRelease pins where validate's release comes from: --release, else the
// variable the conf's validate.release_env names, else none; and that a value that isn't
// X.Y.Z is an error naming its source.
func TestCLI_validateRelease(t *testing.T) {
	conf := filepath.Join(t.TempDir(), "ci.conf.yaml")
	if err := os.WriteFile(conf, []byte("version: 0.0.0\nvalidate:\n  release_env: ROTINI_TEST_RELEASE\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, env string
		argv      []string
		release   string
		code      int
		stderr    string
	}{
		{"neither", "", []string{"validate", "s.yaml", "--config", conf}, "", 0, ""},
		{"the env", "2.0.0", []string{"validate", "s.yaml", "--config", conf}, "2.0.0", 0, ""},
		{"--release beats the env", "2.0.0", []string{"validate", "s.yaml", "--config", conf, "--release", "3.0.0"}, "3.0.0", 0, ""},
		{"--release without a conf variable", "", []string{"validate", "s.yaml", "--release", "1.2.3"}, "1.2.3", 0, ""},
		{"a bad --release", "", []string{"validate", "s.yaml", "--release", "next"}, "", 1, `--release "next": want a release as X.Y.Z`},
		{"a bad env value", "soon", []string{"validate", "s.yaml", "--config", conf}, "", 1, `$ROTINI_TEST_RELEASE (validate.release_env) "soon"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("ROTINI_TEST_RELEASE", tt.env)
			p, _, errb := newTestCLI(t)
			got := "unset"
			p.WithDependency(validateDep, codegen.ValidateFn(
				func(_, _ string, _ bool, _, release string, _ func(string, error), _ func([]error)) error {
					got = release
					return nil
				}))
			code, _ := p.Run(tt.argv)
			if code != tt.code {
				t.Errorf("exit = %d, want %d; stderr:\n%s", code, tt.code, errb.String())
			}
			if tt.stderr != "" {
				if !strings.Contains(errb.String(), tt.stderr) || got != "unset" {
					t.Errorf("stderr = %q, validate ran with %q; want %q and no run", errb.String(), got, tt.stderr)
				}
				return
			}
			if got != tt.release {
				t.Errorf("release = %q, want %q", got, tt.release)
			}
		})
	}
}
