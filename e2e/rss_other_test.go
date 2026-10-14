//go:build !mutation && !unix

package e2e

import "os/exec"

// runPeakRSS runs cmd; its peak memory is not reported on this platform.
func runPeakRSS(cmd *exec.Cmd) (int64, bool, error) { return 0, false, cmd.Run() }
