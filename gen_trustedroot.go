//go:build ignore

// Command gen_trustedroot fetches the sigstore public-good trusted root via TUF
// (securely, validated against sigstore-go's embedded TUF root) and writes it to
// keyless_trustedroot.json, which the keyless verifier embeds for OFFLINE bundle
// verification (D-W9.10). Re-run via `go generate` to refresh after a sigstore key
// rotation. This is the one place that touches the network — dispatch never does.
package main

import (
	"fmt"
	"os"

	"github.com/sigstore/sigstore-go/pkg/root"
)

func main() {
	tr, err := root.FetchTrustedRoot()
	if err != nil {
		panic(err)
	}
	data, err := tr.MarshalJSON()
	if err != nil {
		panic(err)
	}
	// Prove it round-trips through the offline loader the verifier uses.
	if _, err := root.NewTrustedRootFromJSON(data); err != nil {
		panic(fmt.Errorf("round-trip: %w", err))
	}
	if err := os.WriteFile("keyless_trustedroot.json", data, 0o644); err != nil {
		panic(err)
	}
	fmt.Printf("wrote keyless_trustedroot.json (%d bytes)\n", len(data))
}
