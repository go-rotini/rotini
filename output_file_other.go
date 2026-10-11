//go:build !windows

package rotini

// renameRetryable reports false: outside Windows, a rename never fails because another
// program has the file open.
func renameRetryable(error) bool { return false }
