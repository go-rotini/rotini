package rotini

import "errors"

// The write errors the output tests fail a writer with: a full disk and a closed pipe. Plan 9
// reports both as error strings.
var (
	errNoSpace    = errors.New("no space left on device")
	errBrokenPipe = errors.New("write on closed pipe")
)
