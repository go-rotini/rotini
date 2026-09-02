package rotini

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestSubprocess_runAndOutput(t *testing.T) {
	out, err := NewSubprocess("echo", "hello").Output(context.Background())
	if err != nil || out != "hello" {
		t.Fatalf("Output = (%q, %v), want (\"hello\", nil)", out, err)
	}
}

func TestSubprocess_streamsToWriters(t *testing.T) {
	var out, errb bytes.Buffer
	code, err := NewSubprocess("sh", "-c", "echo to-out; echo to-err >&2").
		WithStdout(&out).WithStderr(&errb).Run(context.Background())
	if err != nil || code != 0 {
		t.Fatalf("Run = (%d, %v), want (0, nil)", code, err)
	}
	if !strings.Contains(out.String(), "to-out") || !strings.Contains(errb.String(), "to-err") {
		t.Errorf("streams not separated: out=%q err=%q", out.String(), errb.String())
	}
}

// A non-zero exit is both a code and a typed error, and the message quotes the
// child's stderr rather than the useless "exit status 1".
func TestSubprocess_nonZeroExitCarriesStderr(t *testing.T) {
	code, err := NewSubprocess("sh", "-c", "echo boom >&2; exit 3").Run(context.Background())
	if code != 3 {
		t.Errorf("exit code = %d, want 3", code)
	}
	var subErr *SubprocessError
	if !errors.As(err, &subErr) {
		t.Fatalf("err = %v, want a *SubprocessError", err)
	}
	if subErr.ExitCode != 3 {
		t.Errorf("SubprocessError.ExitCode = %d, want 3", subErr.ExitCode)
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("error %q does not quote the child's stderr", err)
	}
	if !errors.Is(err, ErrInternal) {
		t.Error("a subprocess failure must categorize as internal — the program chose the command")
	}
	if _, ok := errors.AsType[*exec.ExitError](err); !ok {
		t.Error("the underlying *exec.ExitError is not reachable")
	}
}

// A caller that streams stderr owns it, so it is not also captured into the
// message (that would print it twice).
func TestSubprocess_streamedStderrIsNotAlsoCaptured(t *testing.T) {
	var errb bytes.Buffer
	_, err := NewSubprocess("sh", "-c", "echo boom >&2; exit 1").WithStderr(&errb).Run(context.Background())
	var subErr *SubprocessError
	if !errors.As(err, &subErr) {
		t.Fatalf("err = %v, want a *SubprocessError", err)
	}
	if subErr.Stderr != "" {
		t.Errorf("stderr captured despite being streamed: %q", subErr.Stderr)
	}
	if !strings.Contains(errb.String(), "boom") {
		t.Errorf("stderr did not reach the caller's writer: %q", errb.String())
	}
}

func TestSubprocess_missingBinary(t *testing.T) {
	code, err := NewSubprocess("definitely-not-a-real-binary-xyz").Run(context.Background())
	if code != -1 {
		t.Errorf("exit code for a missing binary = %d, want -1", code)
	}
	if _, ok := errors.AsType[*SubprocessError](err); !ok {
		t.Fatalf("err = %v, want a *SubprocessError", err)
	}
	if !strings.Contains(err.Error(), "failed to run") {
		t.Errorf("error %q should say the command never ran", err)
	}
}

func TestSubprocess_timeoutKills(t *testing.T) {
	start := time.Now()
	_, err := NewSubprocess("sleep", "10").WithTimeout(100 * time.Millisecond).Run(context.Background())
	if err == nil {
		t.Fatal("a timed-out command returned no error")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("timeout took %v — the child was not killed", elapsed)
	}
}

func TestSubprocess_contextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(50 * time.Millisecond); cancel() }()
	if _, err := NewSubprocess("sleep", "10").Run(ctx); err == nil {
		t.Fatal("a canceled command returned no error")
	}
}

func TestSubprocess_env(t *testing.T) {
	out, err := NewSubprocess("sh", "-c", "echo $ROTINI_TEST_VAR").
		WithEnv("ROTINI_TEST_VAR=set").Output(context.Background())
	if err != nil || out != "set" {
		t.Fatalf("Output = (%q, %v), want the env value", out, err)
	}
	// WithoutParentEnv leaves the child only what was added. (PATH is a poor probe:
	// sh supplies its own default when the environment has none.)
	t.Setenv("ROTINI_TEST_INHERITED", "from-parent")
	out, err = NewSubprocess("sh", "-c", "echo [$ROTINI_TEST_INHERITED]").Output(context.Background())
	if err != nil || out != "[from-parent]" {
		t.Fatalf("inherited env = (%q, %v), want the parent value", out, err)
	}
	out, err = NewSubprocess("sh", "-c", "echo [$ROTINI_TEST_INHERITED]").WithoutParentEnv().Output(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if out != "[]" {
		t.Errorf("WithoutParentEnv leaked the parent environment: %q", out)
	}
}

func TestSubprocess_dir(t *testing.T) {
	dir := t.TempDir()
	out, err := NewSubprocess("pwd").WithDir(dir).Output(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(out, strings.TrimPrefix(dir, "/private")) {
		t.Errorf("pwd = %q, want it under %q", out, dir)
	}
}

// Lines is an ITERATOR, not a pair of callbacks: the consumer can break out, and
// the run's error arrives in the loop rather than in a closure that cannot return one.
func TestSubprocess_linesTagsStreams(t *testing.T) {
	var got []Line
	var runErr error
	for line, err := range NewSubprocess("sh", "-c", "echo one; echo two >&2; echo three").Lines(context.Background()) {
		if err != nil {
			runErr = err
			continue
		}
		got = append(got, line)
	}
	if runErr != nil {
		t.Fatalf("Lines reported %v", runErr)
	}
	if len(got) != 3 {
		t.Fatalf("got %d lines, want 3: %+v", len(got), got)
	}
	var sawStderr bool
	for _, l := range got {
		if l.Stream == StreamStderr && l.Text == "two" {
			sawStderr = true
		}
	}
	if !sawStderr {
		t.Errorf("stderr line not tagged as such: %+v", got)
	}
}

func TestSubprocess_linesSurfacesExitError(t *testing.T) {
	var runErr error
	for _, err := range NewSubprocess("sh", "-c", "echo x; exit 4").Lines(context.Background()) {
		if err != nil {
			runErr = err
		}
	}
	var subErr *SubprocessError
	if !errors.As(runErr, &subErr) || subErr.ExitCode != 4 {
		t.Errorf("Lines error = %v, want a *SubprocessError with code 4", runErr)
	}
}

// Breaking out of the loop kills the child rather than leaking it.
func TestSubprocess_linesBreakStopsTheChild(t *testing.T) {
	start := time.Now()
	for line := range NewSubprocess("sh", "-c", "echo first; sleep 10").Lines(context.Background()) {
		if line.Text == "first" {
			break
		}
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("break took %v — the child outlived the loop", elapsed)
	}
}

func TestStream_String(t *testing.T) {
	if StreamStdout.String() != "stdout" || StreamStderr.String() != "stderr" {
		t.Error("Stream.String is wrong")
	}
}

// Scan() reports false for BOTH end-of-stream and a read failure, so a scanner used
// without a final Err() check silently truncates. Lines caps a line at 1MB, so a
// longer one must surface as an error rather than as a short, successful-looking run.
func TestSubprocess_linesReportsScanFailure(t *testing.T) {
	// One line of 2MB, no newline until the end — past the scanner's cap.
	script := "printf 'x%.0s' $(seq 1 2000000); echo"

	var got []Line
	var runErr error
	for line, err := range NewSubprocess("sh", "-c", script).Lines(context.Background()) {
		if err != nil {
			runErr = err
			continue
		}
		got = append(got, line)
	}

	if runErr == nil {
		t.Fatalf("an over-long line was silently truncated: got %d lines, no error", len(got))
	}
	var subErr *SubprocessError
	if !errors.As(runErr, &subErr) {
		t.Fatalf("err = %v, want a *SubprocessError", runErr)
	}
	if !errors.Is(runErr, bufio.ErrTooLong) {
		t.Errorf("cause = %v, want bufio.ErrTooLong", subErr.Cause)
	}
}

// A clean run still reports no error — the scan check must not invent failures.
func TestSubprocess_linesNoFalseScanError(t *testing.T) {
	var n int
	for _, err := range NewSubprocess("sh", "-c", "echo a; echo b; echo c").Lines(context.Background()) {
		if err != nil {
			t.Fatalf("clean run reported %v", err)
		}
		n++
	}
	if n != 3 {
		t.Errorf("got %d lines, want 3", n)
	}
}
