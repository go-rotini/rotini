package rotini

import (
	"io"
	"testing"
	"testing/fstest"
)

// TestWithFS_windowsDevices pins that a Windows device bypasses the run's file system, as it
// does with none: NUL is written and read even through a read-only FS that doesn't hold it.
func TestWithFS_windowsDevices(t *testing.T) {
	for _, dir := range []string{"", t.TempDir()} {
		rtx := NewContextFor(Definition{Name: "app", Handler: "App"}, nil).WithFS(fstest.MapFS{})
		if dir != "" {
			rtx = rtx.WithDir(dir)
		}
		for _, name := range []string{"NUL", "nul", `\\.\NUL`} {
			out, err := CreateOutput(rtx, name)
			if err != nil {
				t.Fatalf("dir %q: CreateOutput(%s): %v", dir, name, err)
			}
			if _, err := io.WriteString(out, "discarded"); err != nil {
				t.Errorf("dir %q: write %s: %v", dir, name, err)
			}
			if err := out.Close(); err != nil {
				t.Errorf("dir %q: close %s: %v", dir, name, err)
			}
			in, err := OpenInput(rtx, name)
			if err != nil {
				t.Fatalf("dir %q: OpenInput(%s): %v", dir, name, err)
			}
			if data, err := io.ReadAll(in); err != nil || len(data) != 0 {
				t.Errorf("dir %q: read %s = %q, %v", dir, name, data, err)
			}
			_ = in.Close()
		}
	}
}
