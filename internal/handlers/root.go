package handlers

import (
	rotini "github.com/go-rotini/rotini/internal/rotini"
	"github.com/go-rotini/rotini/rtk"
)

// RootHandlerImpl handles invocations of `rotini` with no
// sub-command. Renders the root help text and propagates the right
// exit code:
//
//   - --help or no args  → help to stdout, exit 0
//   - --version          → version to stdout, exit 0
//   - any other state    → help to stdout, exit 1
type RootHandlerImpl struct{}

// Run satisfies [rotini.RootHandler].
func (h *RootHandlerImpl) Run(ctx rotini.RootCtx, inputs *rotini.RootInputs) error {
	io := rtk.Get[*rtk.IO](ctx.Registry, "io")

	if inputs.Flags.Help {
		fprintln(io.Stdout, getHelp(helpKeyRoot))
		return nil
	}
	if inputs.Flags.Version {
		fprintln(io.Stdout, Version)
		return nil
	}

	// No flags + no subcommand: print help and signal exit 1 so
	// scripts can detect "user forgot the command."
	fprintln(io.Stdout, getHelp(helpKeyRoot))
	return &rtk.EarlyExit{Code: 1}
}

// fprintln writes s + "\n" to w, swallowing the error. Handlers
// don't propagate IO errors here — if stdout is broken, there is no
// recovery path for a help-text printer.
func fprintln(w *rtk.Writer, s string) {
	_, _ = w.Println(s)
}
