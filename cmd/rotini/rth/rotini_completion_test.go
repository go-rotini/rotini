package rth

import (
	"testing"

	"github.com/go-rotini/rotini/cmd/rotini/rtg"
)

// TestRotiniCompletion covers the completion command handler (rotini_completion.go): a
// successful script, the --help flag, a shell that parses (enum-allowed) but has no
// generated script, a missing shell argument, and an invalid shell (a parse error).
func TestRotiniCompletion(t *testing.T) {
	cases := []struct {
		name             string
		argv             []string
		wantOut, wantErr string
		wantCode         int
	}{
		{"bash script", []string{"completion", "bash"}, rtg.CompletionBash, "", 0},
		{"help flag", []string{"completion", "--help"}, rtg.HelpRotiniCompletion, "", 0},
		{"enum-allowed shell with no script errors", []string{"completion", "nushell"}, "", "Error:", 1},
		{"missing shell errors", []string{"completion"}, "", "Error:", 1},
		{"invalid shell is a parse error", []string{"completion", "xyz"}, rtg.HelpRotiniCompletion, "Error:", 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, errb, code := runRotini(t, tc.argv)
			check(t, out, errb, code, tc.wantOut, tc.wantErr, tc.wantCode)
		})
	}
}
