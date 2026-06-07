package rth

import (
	"context"
	"fmt"

	"github.com/go-rotini/rotini"
	"github.com/go-rotini/rotini/cmd/rotini/rtg"
)

type rotiniVersionHandlers struct {
	rotini.DefaultCascadingPreRun
	rotini.DefaultPreRun
	rotini.DefaultPostRun
	rotini.DefaultCascadingPostRun
}

var _ rotini.CommandHandlers = (*rotiniVersionHandlers)(nil)

func (*rotiniVersionHandlers) Run(ctx context.Context, rtx *rotini.Context) {
	parser := rotini.MustGet[*rotini.Parser](rtx, "parser")

	var inputs rtg.RotiniVersionInputs
	if err := parser.Parse(rtx, &inputs); err != nil {
		fmt.Fprintf(rtx.Stderr, "Error: %v\n\n", err)
		fmt.Fprintln(rtx.Stdout, rtg.HelpRotiniVersion)
		rtx.Exit(1)
		return
	}

	flags := inputs.RotiniVersion.Flags

	if flags.Help {
		fmt.Fprintln(rtx.Stdout, rtg.HelpRotiniVersion)
		rtx.Exit(0)
		return
	}

	fmt.Fprintln(rtx.Stdout, rtg.Version)
}
