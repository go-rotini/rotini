package rotini

import (
	"testing"
)

// TestRotiniVersion covers the version command handler (rotini_version.go): the success
// path (prints the version), the --help flag, and a parse error.
func TestRotiniVersion(t *testing.T) {
	cases := []struct {
		name             string
		argv             []string
		wantOut, wantErr string
		wantCode         int
	}{
		{"prints version", []string{"version"}, testVersion, "", 0},
		{"help flag", []string{"version", "--help"}, HelpRotiniVersion, "", 0},
		{"parse error on unknown flag", []string{"version", "--nope"}, HelpRotiniVersion, "Error:", 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, errb, code := runRotini(t, tc.argv)
			check(t, out, errb, code, tc.wantOut, tc.wantErr, tc.wantCode)
		})
	}
}
