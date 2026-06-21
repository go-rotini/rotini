//go:generate go run gen_trustedroot.go
package rotini

import (
	_ "embed"
	"fmt"
	"os"

	"github.com/sigstore/sigstore-go/pkg/bundle"
	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/verify"
)

// The keyless (sigstore) signature rung (D-W9.10): rotini VERIFIES a remote binary's
// sidecar bundle against an expected signer identity, fully offline, before dispatch — it
// never signs (signing is done by standard tooling in the publisher's CI). The sigstore
// public-good trusted root is embedded so verification needs no network; refresh it with
// `go generate` after a sigstore key rotation.

//go:embed keyless_trustedroot.json
var embeddedTrustedRoot []byte

// sigstoreVerifyKeyless is the default [verifyKeyless]: it verifies the sidecar bundle at
// bundlePath is a valid keyless signature over the file at binaryPath, issued to an
// identity matching (issuer, subject), against the embedded sigstore trusted root. It
// returns nil on success or an error describing the failure (wrong identity, bad
// signature, artifact mismatch, missing transparency-log entry, …). Fully offline.
func sigstoreVerifyKeyless(binaryPath, bundlePath, issuer, subject string) error {
	trustedRoot, err := root.NewTrustedRootFromJSON(embeddedTrustedRoot)
	if err != nil {
		return fmt.Errorf("load embedded sigstore trusted root: %w", err)
	}
	verifier, err := verify.NewVerifier(trustedRoot,
		verify.WithSignedCertificateTimestamps(1),
		verify.WithTransparencyLog(1),
		verify.WithObserverTimestamps(1),
	)
	if err != nil {
		return fmt.Errorf("build sigstore verifier: %w", err)
	}
	sigBundle, err := bundle.LoadJSONFromPath(bundlePath)
	if err != nil {
		return fmt.Errorf("load signature bundle: %w", err)
	}
	identity, err := verify.NewShortCertificateIdentity(issuer, "", subject, "")
	if err != nil {
		return fmt.Errorf("build identity matcher: %w", err)
	}
	artifact, err := os.Open(binaryPath)
	if err != nil {
		return fmt.Errorf("open binary: %w", err)
	}
	defer func() { _ = artifact.Close() }()
	if _, err := verifier.Verify(sigBundle, verify.NewPolicy(
		verify.WithArtifact(artifact),
		verify.WithCertificateIdentity(identity),
	)); err != nil {
		return fmt.Errorf("verify signature bundle: %w", err)
	}
	return nil
}
