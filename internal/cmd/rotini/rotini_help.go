package rotini

import (
	"context"
	"fmt"

	"github.com/go-rotini/rotini"
)

type rotiniHelpHandlers struct {
	rotini.DefaultCascadingPreRun
	rotini.DefaultPreRun
	rotini.DefaultPostRun
	rotini.DefaultCascadingPostRun
}

var _ rotini.CommandHandlers = (*rotiniHelpHandlers)(nil)

func (*rotiniHelpHandlers) Run(ctx context.Context, rtx *rotini.Context) {
	inputs, err := rotini.Collect[RotiniHelpInputs](rtx)
	if err != nil {
		rtx.RecordError(err)
		rtx.SignalExit(2)
		return
	}

	args := inputs.RotiniHelp.Arguments
	flags := inputs.RotiniHelp.Flags

	if flags.Help {
		fmt.Fprintln(rtx.Stdout, HelpRotiniHelp)
		rtx.SignalExit(0)
		return
	}

	help, err := Help(args.Command...)
	if err != nil {
		rtx.RecordError(err)
		rtx.SignalExit(2)
		return
	}

	fmt.Fprintln(rtx.Stdout, help)
}
