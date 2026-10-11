package rotini

import (
	"bytes"
	"errors"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestAcceptance_wiringError pins what a user sees when a program misses a feature its tree
// needs: a process that dispatches a declared plugin without the plugins feature exits 1 with
// the wiring error on stderr.
func TestAcceptance_wiringError(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "wired")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	if out, err := exec.Command("go", "build", "-o", bin, "./testdata/wiredcli").CombinedOutput(); err != nil {
		t.Fatalf("build fixture: %v\n%s", err, out)
	}
	var stdout, stderr bytes.Buffer
	cmd := exec.Command(bin, "ctx")
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	ee, ok := errors.AsType[*exec.ExitError](err)
	if !ok || ee.ExitCode() != 1 {
		t.Fatalf("run = %v, want exit 1; stderr: %s", err, stderr.String())
	}
	for _, want := range []string{`command "wired" dispatches plugin "ctx"`, "does not wire plugins", "rotini.Plugins()"} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("stderr %q lacks %q", stderr.String(), want)
		}
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want empty", stdout.String())
	}
}
