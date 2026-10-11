//go:build !mutation && !unix

package e2e

import "os"

// maxRSS is not reported on this platform.
func maxRSS(*os.ProcessState) (int64, bool) { return 0, false }
