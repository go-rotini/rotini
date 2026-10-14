package rotini

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
)

// A config write flushes the new content before renaming it into place, then the directory.
func TestSetConfigValue_syncsBeforeRename(t *testing.T) {
	dir := t.TempDir()
	path := pfWrite(t, dir, "app.yaml", cwYAML)
	var synced []string
	old := syncFile
	syncFile = func(f *os.File) error {
		synced = append(synced, f.Name())
		return old(f)
	}
	t.Cleanup(func() { syncFile = old })

	rtx := cwContext(dir, nil, cwFile(path))
	if err := SetConfigValue(rtx, "app", "region", "us"); err != nil {
		t.Fatal(err)
	}
	if len(synced) == 0 || filepath.Dir(synced[0]) != dir || synced[0] == path {
		t.Fatalf("synced %q, want the temporary file first", synced)
	}
	if runtime.GOOS != "windows" && !slices.Contains(synced, dir) {
		t.Errorf("synced %q, want the directory too", synced)
	}
}
