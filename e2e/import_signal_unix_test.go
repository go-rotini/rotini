//go:build !mutation && unix

package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// TestImportCobraInterrupt pins that Ctrl-C during an import stops it and leaves no temporary
// directory behind, and the program's tree as it was.
func TestImportCobraInterrupt(t *testing.T) {
	shim := importShim(t)
	bin := rotiniBin(t)
	dir := copyFixture(t, filepath.Join(shim, "cobra", "testdata", "corpus", "demo"))
	before := treeHash(t, dir)
	tmp := t.TempDir()

	cmd := exec.Command(bin, "import", "cobra", "--importer-version", shim, ".")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "TMPDIR="+tmp)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	// Interrupt once the import has written its overlay, so go test is about to run.
	deadline := time.Now().Add(time.Minute)
	for {
		if found, _ := filepath.Glob(filepath.Join(tmp, "rotini-import-*", "overlay.json")); len(found) > 0 {
			break
		}
		if time.Now().After(deadline) {
			_ = cmd.Process.Kill()
			t.Fatal("the import never reached go test")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := cmd.Process.Signal(syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err == nil {
		t.Error("an interrupted import exited 0")
	}
	if left, _ := filepath.Glob(filepath.Join(tmp, "rotini-import-*")); len(left) > 0 {
		t.Errorf("an interrupted import left %v", left)
	}
	if treeHash(t, dir) != before {
		t.Error("an interrupted import changed the program's tree")
	}
	if _, err := os.Stat(filepath.Join(dir, "cmd", "acme")); !os.IsNotExist(err) {
		t.Error("an interrupted import wrote the spec")
	}
}
