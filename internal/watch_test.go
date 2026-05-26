package internal

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// specWith builds a minimal mycli spec declaring the given top-level commands.
func specWith(commands ...string) string {
	var b strings.Builder
	b.WriteString("$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-spec.json\nname: mycli\ncommands:\n")
	for _, c := range commands {
		b.WriteString("  - name: " + c + "\n")
	}
	return b.String()
}

func fileContains(path, substr string) bool {
	b, err := os.ReadFile(path)
	return err == nil && bytes.Contains(b, []byte(substr))
}

// waitForCond polls cond until it returns true or the timeout elapses.
func waitForCond(timeout time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(timeout)
	for {
		if cond() {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestGenerateWatchInitialAndStop verifies the initial pass runs and that
// cancelling ctx makes GenerateWatch return cleanly (the signal-driven exit).
func TestGenerateWatchInitialAndStop(t *testing.T) {
	tmp := t.TempDir()
	writeTestFile(t, filepath.Join(tmp, "go.mod"), minimalGoMod)
	specPath := filepath.Join(tmp, ".rotini.spec.yaml")
	writeTestFile(t, specPath, specWith("alpha"))
	t.Chdir(tmp)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var buf bytes.Buffer
	done := make(chan error, 1)
	go func() { done <- GenerateWatch(ctx, specPath, "", &buf) }()

	rtg := filepath.Join(tmp, "rtg", "rotini.go")
	if !waitForCond(3*time.Second, func() bool { return fileContains(rtg, "MycliAlpha") }) {
		t.Fatalf("initial generate did not produce %s with MycliAlpha\noutput:\n%s", rtg, buf.String())
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("GenerateWatch returned error after cancel: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("GenerateWatch did not return after ctx cancellation")
	}
}

// TestGenerateWatchRegeneratesOnChange verifies that editing the spec while
// watching triggers a re-generation that reflects the change.
func TestGenerateWatchRegeneratesOnChange(t *testing.T) {
	tmp := t.TempDir()
	writeTestFile(t, filepath.Join(tmp, "go.mod"), minimalGoMod)
	specPath := filepath.Join(tmp, ".rotini.spec.yaml")
	writeTestFile(t, specPath, specWith("alpha"))
	t.Chdir(tmp)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var buf bytes.Buffer
	done := make(chan error, 1)
	go func() { done <- GenerateWatch(ctx, specPath, "", &buf) }()

	rtg := filepath.Join(tmp, "rtg", "rotini.go")
	if !waitForCond(3*time.Second, func() bool { return fileContains(rtg, "MycliAlpha") }) {
		t.Fatalf("initial generate missing MycliAlpha\noutput:\n%s", buf.String())
	}
	if fileContains(rtg, "MycliBeta") {
		t.Fatal("MycliBeta present before the spec was changed")
	}

	// Add a beta command; the watcher should pick it up and re-generate.
	writeTestFile(t, specPath, specWith("alpha", "beta"))
	if !waitForCond(5*time.Second, func() bool { return fileContains(rtg, "MycliBeta") }) {
		t.Fatalf("watch did not regenerate after spec change (no MycliBeta)\noutput:\n%s", buf.String())
	}

	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("GenerateWatch did not return after cancel")
	}
}
