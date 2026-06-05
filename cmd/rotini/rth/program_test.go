package rth

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/go-rotini/rotini/cmd/rotini/rtg"
	"github.com/go-rotini/rotini/rtk"
)

// TestGenerateHelp_viaProgram exercises a generated handler end-to-end through the real
// program lifecycle — set up exactly like production (rtg.NewProgram + Bind), with a
// recording exit (Program.WithExit) in place of os.Exit. Handlers write directly to
// os.Stdout/os.Stderr, so the test redirects those through pipes to capture them. This is
// the rotini-idiomatic way to test a handler: no harness, no Definition — you run the
// program you'd run in main.go.
func TestGenerateHelp_viaProgram(t *testing.T) {
	var code int
	stdout, stderr := captureStdio(t, func() {
		rtg.NewProgram(Handlers()).
			WithArguments([]string{"generate", "--help"}).
			WithExit(func(c int) { code = c }).
			Bind("parser", rtk.NewParser()).
			Execute()
	})

	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %q)", code, stderr)
	}
	if want := strings.TrimSpace(rtg.HelpRotiniGenerate); !strings.Contains(stdout, want) {
		t.Errorf("generate --help did not print the help page via the program:\n got %q", stdout)
	}
}

// captureStdio redirects os.Stdout and os.Stderr through pipes for the duration of fn,
// returning everything written to each. Pipes are drained on goroutines so a writer never
// blocks on a full pipe buffer.
func captureStdio(t *testing.T, fn func()) (stdout, stderr string) {
	t.Helper()
	origOut, origErr := os.Stdout, os.Stderr
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout, os.Stderr = outW, errW

	var outBuf, errBuf bytes.Buffer
	done := make(chan struct{}, 2)
	go func() { io.Copy(&outBuf, outR); done <- struct{}{} }()
	go func() { io.Copy(&errBuf, errR); done <- struct{}{} }()

	fn()

	outW.Close()
	errW.Close()
	<-done
	<-done
	os.Stdout, os.Stderr = origOut, origErr
	return outBuf.String(), errBuf.String()
}
