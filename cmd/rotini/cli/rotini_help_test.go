package cli

import (
	"testing"
)

// TestRotiniHelp covers the help command handler (rotini_help.go): rendering a command's
// help (success), the --help flag, an unknown-command error, and a parse error.
func TestRotiniHelp(t *testing.T) {
	genHelp, err := Help("generate")
	if err != nil {
		t.Fatalf("rtg.Help(generate): %v", err)
	}

	cases := []struct {
		name             string
		argv             []string
		wantOut, wantErr string
		wantCode         int
	}{
		{"prints help for a command", []string{"help", "generate"}, genHelp, "", 0},
		{"help flag", []string{"help", "--help"}, HelpRotiniHelp, "", 0},
		{"unknown command errors", []string{"help", "bogus"}, HelpRotiniHelp, "Error:", 1},
		{"parse error on unknown flag", []string{"help", "--nope"}, HelpRotiniHelp, "Error:", 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, errb, code := runRotini(t, tc.argv)
			check(t, out, errb, code, tc.wantOut, tc.wantErr, tc.wantCode)
		})
	}
}
