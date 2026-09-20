//go:build mutation

package codegen

import "testing"

// See compile_gate_test.go for why the `mutation` build tag turns the compile gates off.

const compileGatesEnabled = false

func skipUnlessCompiling(t *testing.T) {
	t.Helper()
	t.Skip("builds a generated module; skipped under the `mutation` build tag")
}
