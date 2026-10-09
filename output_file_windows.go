package rotini

import (
	"errors"
	"syscall"
)

// renameRetryable reports whether a failed rename is worth retrying: another program (an
// editor, a virus scanner, the search indexer) briefly holds the file open.
func renameRetryable(err error) bool {
	const (
		accessDenied     = syscall.Errno(5)  // ERROR_ACCESS_DENIED
		sharingViolation = syscall.Errno(32) // ERROR_SHARING_VIOLATION
	)
	return errors.Is(err, accessDenied) || errors.Is(err, sharingViolation)
}
