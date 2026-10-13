//go:build !plan9

package rotini

import "syscall"

// The write errors the output tests fail a writer with: a full disk and a closed pipe.
var (
	errNoSpace    error = syscall.ENOSPC
	errBrokenPipe error = syscall.EPIPE
)
