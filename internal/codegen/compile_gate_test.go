//go:build !mutation

package codegen

import "testing"

// compileGatesEnabled controls the tests that shell out to `go build` — the ones whose whole
// assertion is that generated code COMPILES.
//
// They are the most valuable tests in this package and the most expensive: each builds a real
// module, and together they are 13 of the suite's 19 seconds. Mutation testing runs the whole
// package suite once per mutant, so that cost is multiplied by every mutant in the package —
// which is how a gate that used to finish stopped finishing.
//
// They contribute almost nothing to mutation score in exchange, because "it compiles" is not
// an assertion a mutated boundary or a flipped conditional usually breaks. So `make
// test-mutation` sets the `mutation` build tag (see .gremlins.yaml) and they are skipped
// there, and NOWHERE else: an ordinary `go test ./...`, CI, and every tier in the verification
// chain still run all of them.
const compileGatesEnabled = true

// skipUnlessCompiling skips a test that builds a generated module: under -short, for a fast
// local loop, and under the `mutation` tag, so the mutation gate stays affordable.
func skipUnlessCompiling(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("builds a generated module; skipped under -short")
	}
	if !compileGatesEnabled {
		t.Skip("builds a generated module; skipped under the `mutation` build tag")
	}
}
