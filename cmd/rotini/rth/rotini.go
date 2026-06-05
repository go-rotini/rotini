package rth

import (
	"context"
	"fmt"
	"os"

	"github.com/go-rotini/rotini"
	"github.com/go-rotini/rotini/cmd/rotini/rtg"
	"github.com/go-rotini/rotini/rtk"
)

type rotiniHandlers struct {
	rotini.DefaultCascadingPreRun
	rotini.DefaultPreRun
	rotini.DefaultPostRun
	rotini.DefaultCascadingPostRun
}

var _ rotini.CommandHandlers = (*rotiniHandlers)(nil)

func (*rotiniHandlers) Run(ctx context.Context, rtx *rotini.Context) {
	parser := rotini.MustGet[*rtk.Parser](rtx, "parser")

	var inputs rtg.RotiniInputs
	if err := parser.Parse(rtx, &inputs); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n\n", err)
		fmt.Fprintln(os.Stdout, rtg.HelpRotini)
		rtx.Exit(1)
		return
	}

	flags := inputs.Rotini.Flags
	switch {
	case flags.Help:
		fmt.Fprintln(os.Stdout, rtg.HelpRotini)
		rtx.ExitNow(0)
	case flags.Version:
		fmt.Fprintln(os.Stdout, rtg.Version)
		rtx.ExitNow(0)
	default:
		fmt.Fprintln(os.Stdout, rtg.HelpRotini)
		rtx.ExitNow(1)
	}
}
