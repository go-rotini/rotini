package rth

import (
	"context"
	"strings"
	"testing"

	"github.com/go-rotini/rotini/cmd/rotini/rtg"
	"github.com/go-rotini/rotini/rtk"
)

// TestGenerateHandler_helpViaHarness dogfoods rtk.Harness against a real generated
// handler: the harness wires the standard service doubles and resolves the companion's
// own command tree, so `generate --help` runs through the actual Run method and its help
// page is captured for assertion — no real terminal, spec file, or process involved.
func TestGenerateHandler_helpViaHarness(t *testing.T) {
	h := rtk.NewHarness(rtk.HarnessConfig{
		Def:  rtg.Definition,
		Argv: []string{"generate", "--help"},
	})

	(&rotiniGenerateHandlers{}).Run(context.Background(), h.Rtx)

	if want := strings.TrimSpace(rtg.HelpRotiniGenerate); !strings.Contains(h.Out.String(), want) {
		t.Errorf("generate --help did not print the help page via the harness:\n got %q\nwant it to contain %q", h.Out.String(), want)
	}
	if h.Err.Len() != 0 {
		t.Errorf("unexpected stderr: %q", h.Err.String())
	}
}
