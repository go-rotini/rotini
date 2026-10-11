package rotini

import (
	"errors"
	"io/fs"
	"syscall"
)

// pathMissing reports whether err says a path does not exist. On Windows a name no file can
// have, such as one holding `*` or `?`, fails with ERROR_INVALID_NAME rather than not-found.
func pathMissing(err error) bool {
	const invalidName = syscall.Errno(123) // ERROR_INVALID_NAME
	return errors.Is(err, fs.ErrNotExist) || errors.Is(err, invalidName)
}
