//go:build unix

package rotini

import (
	"bytes"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// A forced exit runs no hook, so the trap itself discards an open output file's temporary file.
func TestCreateOutput_forcedExitRemovesTemporaryFile(t *testing.T) {
	for _, tc := range []struct {
		name    string
		timeout time.Duration
	}{
		{"second signal", 0},
		{"termination timeout", 20 * time.Millisecond},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer swapTrapSignals(syscall.SIGUSR1)()
			dir := t.TempDir()
			exited := make(chan []string, 1)
			p := NewProgram(Definition{Name: "app", Handler: "App"}, &funcHandlers{run: func(rtx *Context) {
				out, err := CreateOutput(rtx, filepath.Join(dir, "report.txt"))
				if err != nil {
					t.Error(err)
					return
				}
				if _, err := out.Write([]byte("partial")); err != nil {
					t.Error(err)
				}
				_ = syscall.Kill(syscall.Getpid(), syscall.SIGUSR1)
				<-rtx.Context().Done()
				if tc.timeout == 0 {
					_ = syscall.Kill(syscall.Getpid(), syscall.SIGUSR1)
				}
				select {
				case left := <-exited:
					if len(left) != 0 {
						t.Errorf("files left at the forced exit: %q", left)
					}
				case <-time.After(2 * time.Second):
					t.Error("the run was not forced to exit")
				}
			}}).WithStdout(&bytes.Buffer{}).WithStderr(&bytes.Buffer{}).
				WithTerminationTimeout(tc.timeout).
				WithExit(func(int) {
					entries, _ := os.ReadDir(dir)
					var left []string
					for _, e := range entries {
						left = append(left, e.Name())
					}
					exited <- left
				})
			p.Run(nil)
		})
	}
}
