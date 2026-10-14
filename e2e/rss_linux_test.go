//go:build !mutation && linux

package e2e

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// runPeakRSS runs cmd and returns its peak resident memory in bytes. Linux's rusage peak for a
// child includes the memory it shared with this process before exec, so a busy test process
// would inflate it; the exec'd program's own high-water mark (VmHWM) is sampled instead while
// it runs.
func runPeakRSS(cmd *exec.Cmd) (int64, bool, error) {
	if err := cmd.Start(); err != nil {
		return 0, false, err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	status := fmt.Sprintf("/proc/%d/status", cmd.Process.Pid)
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	var peak int64
	for {
		select {
		case err := <-done:
			return peak, peak > 0, err
		case <-tick.C:
			if hwm, ok := vmHWM(status); ok && hwm > peak {
				peak = hwm
			}
		}
	}
}

// vmHWM reads the VmHWM line of a /proc status file, in bytes. It is absent once the process
// has exited.
func vmHWM(path string) (int64, bool) {
	f, err := os.Open(path)
	if err != nil {
		return 0, false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if v, ok := strings.CutPrefix(sc.Text(), "VmHWM:"); ok {
			kb, err := strconv.ParseInt(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(v), "kB")), 10, 64)
			return kb << 10, err == nil
		}
	}
	_ = sc.Err() // a file that can't be read is no reading, as for a process that has exited
	return 0, false
}
