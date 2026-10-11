package cmd

import "testing"

// This file does not compile; the import must not depend on the package's own tests.
func TestBroken(t *testing.T) {
	undefinedHelper(t)
}
