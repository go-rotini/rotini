//go:build !linux && !darwin && !freebsd && !netbsd && !openbsd && !dragonfly

package rotini

import "os"

// disableEcho has no portable implementation off unix. Reporting "cannot" is honest: the caller
// falls back to a plain read, which is what it would do for a pipe anyway.
func disableEcho(file *os.File) (restore func(), ok bool) { return nil, false }
