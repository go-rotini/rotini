package rth

import (
	"context"
	"fmt"

	"github.com/go-rotini/rotini"
	"github.com/go-rotini/rotini/cmd/rotini/rtg"
)

type rotiniHandlers struct {
	rotini.DefaultCascadingPreRun
	rotini.DefaultPreRun
	rotini.DefaultPostRun
	rotini.DefaultCascadingPostRun
}

var _ rotini.CommandHandlers = (*rotiniHandlers)(nil)

func (*rotiniHandlers) Run(ctx context.Context, rtx *rotini.Context) {
	parser := rotini.MustGet[*rotini.Parser](rtx, "parser")

	var inputs rtg.RotiniInputs
	if err := parser.Parse(rtx, &inputs); err != nil {
		fmt.Fprintf(rtx.Stderr, "Error: %v\n\n", err)
		fmt.Fprintln(rtx.Stdout, rtg.HelpRotini)
		rtx.Exit(1)
		return
	}

	flags := inputs.Rotini.Flags
	switch {
	case flags.Help:
		fmt.Fprintln(rtx.Stdout, rtg.HelpRotini)
		rtx.Exit(0)
	case flags.Version:
		fmt.Fprintln(rtx.Stdout, rtg.Version)
		rtx.Exit(0)
	default:
		fmt.Fprintln(rtx.Stdout, rtg.HelpRotini)
		rtx.Exit(1)
	}
}
