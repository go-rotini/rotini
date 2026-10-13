//go:build unix

package codegen

import (
	"syscall"
	"testing"
	"time"
)

// Watch mode ends cleanly on SIGTERM, as kill, timeout and container stops send it.
func TestRunOrWatch_endsOnSIGTERM(t *testing.T) {
	specPath, confPath := watchFixture(t, true)
	counter := newPassCounter()
	done := make(chan error, 1)
	go func() {
		done <- runOrWatch(specPath, confPath, true, counter.pass, func(string, error) {})
	}()
	counter.await(t, 1, 5*time.Second)
	if err := syscall.Kill(syscall.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("runOrWatch after SIGTERM = %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("watch mode kept running after SIGTERM")
	}
}
