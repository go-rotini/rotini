package rth

import (
	"context"
	"fmt"

	"github.com/go-rotini/rotini"
	"github.com/go-rotini/rotini/cmd/rotini/rtg"
	"github.com/go-rotini/rotini/internal"
)

type rotiniInitializeHandlers struct {
	rotini.DefaultCascadingPreRun
	rotini.DefaultPreRun
	rotini.DefaultPostRun
	rotini.DefaultCascadingPostRun
}

var _ rotini.CommandHandlers = (*rotiniInitializeHandlers)(nil)

func (*rotiniInitializeHandlers) Run(ctx context.Context, rtx *rotini.Context) {
	parser := rotini.MustGet[*rotini.Parser](rtx, "parser")

	var inputs rtg.RotiniInitializeInputs
	if err := parser.Parse(rtx, &inputs); err != nil {
		fmt.Fprintf(rtx.Stderr, "Error: %v\n\n", err)
		fmt.Fprintln(rtx.Stdout, rtg.HelpRotiniInitialize)
		rtx.Exit(1)
		return
	}

	args := inputs.RotiniInitialize.Arguments
	flags := inputs.RotiniInitialize.Flags

	if flags.Help {
		fmt.Fprintln(rtx.Stdout, rtg.HelpRotiniInitialize)
		rtx.Exit(0)
		return
	}

	if args.Name == "" {
		fmt.Fprintln(rtx.Stderr, "Error: a name argument is required")
		rtx.Exit(1)
		return
	}

	// Bind the real implementation only if a caller (e.g. a test) hasn't injected one,
	// so the work is dependency-injectable at the registry seam without main.go wiring it.
	if !rtx.Has("initialize") {
		rtx.Bind("initialize", internal.Initialize)
	}
	initialize := rotini.MustGet[internal.InitializeFn](rtx, "initialize")

	if err := initialize(args.Name, flags.Format, flags.Force, flags.Into); err != nil {
		fmt.Fprintln(rtx.Stderr, "Error:", err)
		rtx.Exit(1)
		return
	}
}
