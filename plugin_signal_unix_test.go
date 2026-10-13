//go:build unix

package rotini

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// A plugin ended by a signal makes the host exit 128+n, as a shell reports it, not 255.
func TestRun_pluginKilledBySignalExits128PlusN(t *testing.T) {
	writeFakeBinary(t, "app-die", "#!/bin/sh\nkill -TERM $$\n")
	def := Definition{Name: "app", Handler: "App", Plugins: []PluginDef{{Name: "die", Binary: "app-die"}}}

	p, _, _ := pluginProgram(def, []string{"die"})
	if code, _ := p.Run(p.args); code != 128+int(syscall.SIGTERM) {
		t.Errorf("exit code = %d, want %d", code, 128+int(syscall.SIGTERM))
	}
}

// A trapped signal while a plugin runs kills the plugin and exits with the signal's code.
func TestRun_hostSignalDuringPluginExitsWithSignalCode(t *testing.T) {
	defer swapTrapSignals(syscall.SIGUSR1)()
	ready := filepath.Join(t.TempDir(), "ready")
	writeFakeBinary(t, "app-wait", "#!/bin/sh\ntouch "+ready+"\nexec sleep 30\n")
	def := Definition{Name: "app", Handler: "App", Plugins: []PluginDef{{Name: "wait", Binary: "app-wait"}}}

	p, _, _ := pluginProgram(def, []string{"wait"})
	done := make(chan int, 1)
	go func() {
		code, _ := p.Run(p.args)
		done <- code
	}()
	waitForFile(t, ready, "the plugin to start")
	_ = syscall.Kill(syscall.Getpid(), syscall.SIGUSR1)

	select {
	case code := <-done:
		if want := 128 + int(syscall.SIGUSR1); code != want {
			t.Errorf("exit code = %d, want %d", code, want)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("the signal did not end the plugin")
	}
}

// A trapped signal asks the plugin to stop with the same signal, so its handler can clean up
// before the host exits with the signal's code. The handler holds until the test lets it go,
// so the host is seen waiting for it without relying on how long anything takes.
func TestRun_hostSignalLetsPluginCleanUp(t *testing.T) {
	defer swapTrapSignals(syscall.SIGUSR1)()
	dir := t.TempDir()
	ready, stopping := filepath.Join(dir, "ready"), filepath.Join(dir, "stopping")
	proceed, marker := filepath.Join(dir, "proceed"), filepath.Join(dir, "cleaned")
	writeFakeBinary(t, "app-wait", "#!/bin/sh\n"+
		"trap 'touch "+stopping+"; i=0; while [ ! -e "+proceed+" ] && [ $i -lt 6000 ]; do sleep 0.01; i=$((i+1)); done; touch "+marker+"; exit 0' USR1\n"+
		"touch "+ready+"\nsleep 30 >/dev/null 2>&1 &\nwait\n")
	// However the test ends, the plugin's handler is let go, so the plugin never outlives it.
	t.Cleanup(func() { _ = os.WriteFile(proceed, nil, 0o600) })
	def := Definition{Name: "app", Handler: "App", Plugins: []PluginDef{{Name: "wait", Binary: "app-wait"}}}

	p, _, _ := pluginProgram(def, []string{"wait"})
	// A grace period longer than the test's own waits, so a loaded machine can't end the plugin
	// before the test lets it go.
	p.WithTerminationTimeout(time.Minute)
	done := make(chan int, 1)
	go func() {
		code, _ := p.Run(p.args)
		done <- code
	}()
	waitForFile(t, ready, "the plugin to start")
	_ = syscall.Kill(syscall.Getpid(), syscall.SIGUSR1)
	waitForFile(t, stopping, "the plugin's signal handler to run")

	select {
	case code := <-done:
		t.Fatalf("the host exited (%d) while the plugin's signal handler was still running", code)
	default:
	}
	if err := os.WriteFile(proceed, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	select {
	case code := <-done:
		if want := 128 + int(syscall.SIGUSR1); code != want {
			t.Errorf("exit code = %d, want %d", code, want)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("the plugin did not end after its signal handler was let go")
	}
	if _, err := os.Stat(marker); err != nil {
		t.Error("the plugin was killed before its signal handler finished")
	}
}

// waitForFile waits for path to exist, failing the test after 20 seconds.
func waitForFile(t *testing.T, path, what string) {
	t.Helper()
	for start := time.Now(); time.Since(start) < 20*time.Second; time.Sleep(10 * time.Millisecond) {
		if _, err := os.Stat(path); err == nil {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}

// A plugin past its timeout gets SIGTERM and a grace period, not an immediate kill.
func TestRun_pluginTimeoutSendsTerm(t *testing.T) {
	dir := t.TempDir()
	marker, ready := filepath.Join(dir, "cleaned"), filepath.Join(dir, "ready")
	writeFakeBinary(t, "app-slow", "#!/bin/sh\n"+
		"trap 'touch "+marker+"; exit 0' TERM\n"+
		"touch "+ready+"\n"+
		"sleep 5 >/dev/null 2>&1 &\nwait\n")
	def := Definition{Name: "app", Handler: "App",
		Plugins: []PluginDef{{Name: "slow", Binary: "app-slow", Timeout: 2 * time.Second}}}

	p, _, _ := pluginProgram(def, []string{"slow"})
	_, err := p.Run(p.args)
	var pe *PluginError
	if !errors.As(err, &pe) || pe.Kind != PluginTimeout {
		t.Fatalf("err = %v, want a plugin timeout", err)
	}
	if _, err := os.Stat(ready); err != nil {
		t.Skip("the plugin hadn't set its trap before its timeout (a loaded machine)")
	}
	if _, err := os.Stat(marker); err != nil {
		t.Error("the timed-out plugin was killed without a chance to clean up")
	}
}

// The caller's deadline is a cancellation of the run, not the plugin's own timeout.
func TestRun_callerDeadlineIsNotPluginTimeout(t *testing.T) {
	writeFakeBinary(t, "app-wait", "#!/bin/sh\nexec sleep 3\n")
	def := Definition{Name: "app", Handler: "App", Plugins: []PluginDef{{Name: "wait", Binary: "app-wait"}}}

	p, _, _ := pluginProgram(def, []string{"wait"})
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	code, err := p.RunContext(ctx, p.args)
	if pe, ok := errors.AsType[*PluginError](err); ok {
		t.Errorf("err = %v (kind %v), want no PluginError", pe, pe.Kind)
	}
	if want := 128 + int(syscall.SIGTERM); code != want {
		t.Errorf("exit code = %d, want %d", code, want)
	}
}
