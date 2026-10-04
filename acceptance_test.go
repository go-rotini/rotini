package rotini

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// This file is the process tier of the input conformance suite: it builds the
// fixture binary (testdata/acmecli) once and runs it as a subprocess — the
// only honest way to witness real exit codes (os.Exit through the default
// trap), auto-detected pipes vs. no-pipe stdin, the __complete wire protocol,
// and signal handling. `make test-acceptance` runs it. The matrix IDs owned
// by this tier (TestConformance_matrixComplete enforces the exactly-once
// split with the in-process tier):

var acceptanceMatrixIDs = []string{"ARG-04", "STDIN-02", "STDIN-07"}

var (
	acmeBinOnce sync.Once
	acmeBinPath string
	acmeBinErr  error
)

// acmeBin builds the fixture binary once per test process and returns its
// path. The binary lands in a temp dir removed by TestMain.
func acmeBin(t *testing.T) string {
	t.Helper()
	acmeBinOnce.Do(func() {
		dir, err := os.MkdirTemp("", "rotini-acceptance-")
		if err != nil {
			acmeBinErr = err
			return
		}
		acmeBinDir = dir
		acmeBinPath = filepath.Join(dir, "acme")
		if runtime.GOOS == "windows" {
			acmeBinPath += ".exe" // Windows runs a program only by an executable extension
		}
		cmd := exec.Command("go", "build", "-o", acmeBinPath, "./testdata/acmecli")
		if out, err := cmd.CombinedOutput(); err != nil {
			acmeBinErr = fmt.Errorf("build fixture: %v\n%s", err, out)
		}
	})
	if acmeBinErr != nil {
		t.Fatal(acmeBinErr)
	}
	return acmeBinPath
}

var acmeBinDir string

func TestMain(m *testing.M) {
	code := m.Run()
	if acmeBinDir != "" {
		os.RemoveAll(acmeBinDir)
	}
	os.Exit(code)
}

// acmeRun runs the fixture binary with args; stdin nil means the child gets
// /dev/null (a character device — exactly the "no pipe" condition).
func acmeRun(t *testing.T, stdin string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	cmd := exec.Command(acmeBin(t), args...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	code = 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("run fixture: %v", err)
	}
	return out.String(), errb.String(), code
}

// ARG-04: a missing required positional is a usage error, with the message on stderr and
// NOTHING executed.
//
// Exit 1 is the DEFAULT REPORTER's flat floor, not a category mapping: rotini labels the error
// CategoryUsage and leaves the code to the reporter (see Category). A program wanting the common
// "2 means the command line was wrong" convention maps it itself.
func TestAcceptance_ARG_04_missingRequired(t *testing.T) {
	stdout, stderr, code := acmeRun(t, "", "widget", "get")
	if code != 1 {
		t.Errorf("exit = %d, want %d", code, 1)
	}
	if !strings.Contains(stderr, "missing required") {
		t.Errorf("stderr = %q, want the missing-required usage error", stderr)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want empty — nothing executed", stdout)
	}
}

// STDIN-02: a piped document is auto-detected (stdin is not a TTY) and read
// by the declared stdin channel without any flag.
func TestAcceptance_STDIN_02_autoDetectedPipe(t *testing.T) {
	stdout, stderr, code := acmeRun(t, "kind: Widget\n", "ingest")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %s)", code, stderr)
	}
	if !strings.Contains(stdout, "INGEST kind=Widget") {
		t.Errorf("stdout = %q, want the piped payload ingested", stdout)
	}
}

// STDIN-07: "-" with nothing piped (the child's stdin is /dev/null — a
// character device, the same condition as an interactive TTY) must error
// promptly, never hang. The test's own timeout is the no-hang assertion.
func TestAcceptance_STDIN_07_dashWithoutPipe(t *testing.T) {
	stdout, stderr, code := acmeRun(t, "", "apply", "-f", "-")
	if code != 1 {
		t.Errorf("exit = %d, want %d", code, 1)
	}
	if !strings.Contains(stderr, "stdin is empty") {
		t.Errorf("stderr = %q, want the empty-stdin error", stderr)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want empty", stdout)
	}
}

// The __complete wire protocol answers over the real process boundary: the
// generated shell scripts call `<binary> __complete <words…>` and read one
// candidate per stdout line.
func TestAcceptance_completionProtocol(t *testing.T) {
	stdout, _, code := acmeRun(t, "", "__complete", "")
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	// One candidate per line; a described candidate is "name\tdescription",
	// an undescribed one is bare — both shapes ride the same wire.
	byName := map[string]string{}
	for line := range strings.SplitSeq(strings.TrimRight(stdout, "\n"), "\n") {
		name, desc, _ := strings.Cut(line, "\t")
		byName[name] = desc
	}
	for _, want := range []string{"widget", "apply", "ingest", "sleep"} {
		if _, ok := byName[want]; !ok {
			t.Errorf("candidates %v missing %q", byName, want)
		}
	}
	if byName["widget"] != "manage widgets" {
		t.Errorf("widget description = %q, want the fixture summary", byName["widget"])
	}
	if byName["ingest"] != "" {
		t.Errorf("ingest description = %q, want bare (no summary declared)", byName["ingest"])
	}

	// A flag candidate carries its summary on every identifier.
	stdout, _, code = acmeRun(t, "", "__complete", "apply", "--f")
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if want := "--file\tmanifest path, or - for stdin"; !strings.Contains(stdout, want) {
		t.Errorf("flag candidates = %q, want %q", stdout, want)
	}
}

// The default signal trap over a real process: SIGINT during a running hook
// cancels the context, the lifecycle stops cleanly, and the process exits 130.
func TestAcceptance_signalInterrupt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("signal delivery semantics differ on windows")
	}
	cmd := exec.Command(acmeBin(t), "sleep")
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	// Wait for the handler to be inside Run before signaling.
	buf := make([]byte, len("sleeping\n"))
	if _, err := out.Read(buf); err != nil {
		t.Fatalf("read sleeping marker: %v", err)
	}
	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		ee, ok := err.(*exec.ExitError)
		if !ok || ee.ExitCode() != 130 {
			t.Errorf("exit = %v, want code 130 (128+SIGINT)", err)
		}
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("fixture did not exit after SIGINT — the trap must stop the lifecycle")
	}
}
