//go:build unix

package rotini

import (
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
	writeFakeBinary(t, "app-wait", "#!/bin/sh\nexec sleep 5\n")
	def := Definition{Name: "app", Handler: "App", Plugins: []PluginDef{{Name: "wait", Binary: "app-wait"}}}

	p, _, _ := pluginProgram(def, []string{"wait"})
	done := make(chan int, 1)
	go func() {
		code, _ := p.Run(p.args)
		done <- code
	}()
	time.Sleep(200 * time.Millisecond)
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
