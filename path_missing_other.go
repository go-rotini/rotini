//go:build !windows

package rotini

import (
	"errors"
	"io/fs"
)

// pathMissing reports whether err says a path does not exist.
func pathMissing(err error) bool { return errors.Is(err, fs.ErrNotExist) }
