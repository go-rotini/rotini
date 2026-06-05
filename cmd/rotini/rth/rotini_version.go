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
		fmt.Fprintln(rtx.Stderr, "rotini:", err)
		rtx.Exit(1)
		return
	}

	if inputs.RotiniVersion.Flags.Help {
		fmt.Fprintln(rtx.Stdout, rtg.HelpRotiniVersion)
		return
	}

	fmt.Fprintln(rtx.Stdout, rtg.Version)
}
