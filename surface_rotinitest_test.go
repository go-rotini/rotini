package rotini

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRotinitestSurface is TestExportedSurface for the rotinitest package:
// `go test . -run Surface -update-surface` refreshes both snapshots.
func TestRotinitestSurface(t *testing.T) {
	got := strings.Join(exportedSurface(t, "rotinitest"), "\n") + "\n"
	golden := filepath.Join("testdata", "surface_rotinitest.txt")
	if *updateSurface {
		if err := os.WriteFile(golden, []byte(got), 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("read surface snapshot (run with -update-surface to create it): %v", err)
	}
	if got != string(want) {
		t.Errorf("rotinitest's exported surface changed — review the diff, then re-run with -update-surface if intended:\n%s",
			surfaceDiff(strings.Split(string(want), "\n"), strings.Split(got, "\n")))
	}
}
