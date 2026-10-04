//go:build !mutation

package codegen

import "testing"

// compileGatesEnabled enables the tests that `go build` generated code. They dominate the
// suite's run time and add little mutation score, so `make test-mutation` sets the `mutation`
// build tag (see .gremlins.yaml) to disable them; every other run keeps them.
const compileGatesEnabled = true

// skipUnlessCompiling skips a test that builds a generated module under -short or the
// `mutation` build tag.
func skipUnlessCompiling(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("builds a generated module; skipped under -short")
	}
	if !compileGatesEnabled {
		t.Skip("builds a generated module; skipped under the `mutation` build tag")
	}
}
