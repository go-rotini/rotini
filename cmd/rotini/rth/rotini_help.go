package rth

import (
	"context"
	"fmt"

	"github.com/go-rotini/rotini"
	"github.com/go-rotini/rotini/cmd/rotini/rtg"
)

type rotiniHelpHandlers struct {
	rotini.DefaultCascadingPreRun
	rotini.DefaultPreRun
	rotini.DefaultPostRun
	rotini.DefaultCascadingPostRun
}

var _ rotini.CommandHandlers = (*rotiniHelpHandlers)(nil)

func (*rotiniHelpHandlers) Run(ctx context.Context, rtx *rotini.Context) {
	parser := rotini.MustGet[*rotini.Parser](rtx, "parser")

	var inputs rtg.RotiniHelpInputs
	if err := parser.Parse(rtx, &inputs); err != nil {
		fmt.Fprintf(rtx.Stderr, "Error: %v\n\n", err)
		fmt.Fprintln(rtx.Stdout, rtg.HelpRotiniHelp)
		rtx.Exit(1)
		return
	}

	args := inputs.RotiniHelp.Arguments
	flags := inputs.RotiniHelp.Flags

	if flags.Help {
		fmt.Fprintln(rtx.Stdout, rtg.HelpRotiniHelp)
		rtx.Exit(0)
		return
	}

	text, err := rtg.Help(args.Command...)
	if err != nil {
		fmt.Fprintf(rtx.Stderr, "Error: %v\n\n", err)
		fmt.Fprintln(rtx.Stdout, rtg.HelpRotiniHelp)
		rtx.Exit(1)
		return
	}

	fmt.Fprintln(rtx.Stdout, text)
}
