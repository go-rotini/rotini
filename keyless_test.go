package rotini

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sigstore/sigstore-go/pkg/root"
)

// The embedded sigstore trusted root parses, so keyless verification can run offline.
// (A failure here means it needs refreshing: `go generate ./...`.)
func TestEmbeddedTrustedRoot_valid(t *testing.T) {
	if _, err := root.NewTrustedRootFromJSON(embeddedTrustedRoot); err != nil {
		t.Fatalf("embedded trusted root invalid: %v — run `go generate`", err)
	}
}

// The default keyless verifier is wired (init) and reaches real verification past the
// trusted-root load, rejecting a bogus bundle rather than passing. A full positive path
// needs a real signed bundle fixture (signing infra) and is left as a follow-up.
func TestSigstoreVerifyKeyless_rejectsBogusBundle(t *testing.T) {
	if verifyKeyless == nil {
		t.Fatal("verifyKeyless is not wired by default")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "art")
	if err := os.WriteFile(bin, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	bundle := filepath.Join(dir, "art"+keylessBundleSuffix)
	if err := os.WriteFile(bundle, []byte(`{"not":"a real bundle"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := sigstoreVerifyKeyless(bin, bundle, "https://issuer.example", "subject"); err == nil {
		t.Fatal("want an error for a bogus bundle, got nil")
	}
}
