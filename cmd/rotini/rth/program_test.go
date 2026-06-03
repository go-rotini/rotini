package rth

import (
	"bytes"
	"strings"
	"testing"

	"github.com/go-rotini/rotini/cmd/rotini/rtg"
	"github.com/go-rotini/rotini/rtk"
)

// TestGenerateHelp_viaProgram exercises a generated handler end-to-end through the real
// program lifecycle — set up exactly like production (rtg.NewProgram + Bind), but with
// capturing streams and a recording exit (Program.WithStdout/WithStderr/WithExit) in
// place of os.Stdout/os.Exit, so stdout, stderr, and the exit code can all be asserted.
// This is the rotini-idiomatic way to test a handler: no harness, no Definition — you
// run the program you'd run in main.go.
func TestGenerateHelp_viaProgram(t *testing.T) {
	var code int
	out, errb := &bytes.Buffer{}, &bytes.Buffer{}

	rtg.NewProgram(Handlers()).
		WithArguments([]string{"generate", "--help"}).
		WithStdout(out).
		WithStderr(errb).
		WithExit(func(c int) { code = c }).
		Bind("parser", rtk.NewParser()).
		Bind("io", rtk.NewIO().WithStdout(out).WithStderr(errb)).
		Execute()

	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %q)", code, errb.String())
	}
	if want := strings.TrimSpace(rtg.HelpRotiniGenerate); !strings.Contains(out.String(), want) {
		t.Errorf("generate --help did not print the help page via the program:\n got %q", out.String())
	}
}
