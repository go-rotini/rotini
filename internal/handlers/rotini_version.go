package handlers

import (
	rotini "github.com/go-rotini/rotini/internal/rotini"
	"github.com/go-rotini/rotini/rtk"
)

// VersionHandlerImpl handles `rotini version`. Prints the binary's
// version (set via ldflags at build time; defaults to "dev").
type VersionHandlerImpl struct{}

// Run satisfies [rotini.VersionHandler].
func (h *VersionHandlerImpl) Run(ctx rotini.VersionCtx, inputs *rotini.VersionInputs) error {
	io := rtk.Get[*rtk.IO](ctx.Registry, "io")

	if inputs.Flags.Help {
		fprintln(io.Stdout, getHelp(helpKeyVersion))
		return nil
	}
	fprintln(io.Stdout, Version)
	return nil
}
