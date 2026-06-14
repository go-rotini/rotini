package rotini

import (
	"testing"
)

// TestRotiniHelp covers the help command handler (rotini_help.go): rendering a command's
// help (success) and the --help flag (both unchanged), plus the error paths — which now
// record the error and stop with rtx.SignalExit(1), so rotini's default OnError
// prints "rotini: <err>" to stderr (no help dump) and the handler's usage exit code stands
// (the first non-zero code wins). A bad command name and a bad flag both exit 1.
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
		{"unknown command errors", []string{"help", "bogus"}, "", "rotini:", 1},
		{"parse error on unknown flag", []string{"help", "--nope"}, "", `rotini: unknown flag "--nope"`, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, errb, code := runRotini(t, tc.argv)
			check(t, out, errb, code, tc.wantOut, tc.wantErr, tc.wantCode)
		})
	}
}
