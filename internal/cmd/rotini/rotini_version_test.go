package rotini

import (
	"testing"
)

// TestRotiniVersion covers the version command handler (rotini_version.go): the success
// path (prints the version) and the --help flag (unchanged), plus a parse error — which
// now records the error and stops, so rotini's default OnError prints "rotini: <err>" to
// stderr (no help dump) and exits 1.
func TestRotiniVersion(t *testing.T) {
	cases := []struct {
		name             string
		argv             []string
		wantOut, wantErr string
		wantCode         int
	}{
		{"prints version", []string{"version"}, testVersion, "", 0},
		{"help flag", []string{"version", "--help"}, HelpRotiniVersion, "", 0},
		{"parse error on unknown flag", []string{"version", "--nope"}, "", `rotini: unknown flag "--nope"`, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, errb, code := runRotini(t, tc.argv)
			check(t, out, errb, code, tc.wantOut, tc.wantErr, tc.wantCode)
		})
	}
}
