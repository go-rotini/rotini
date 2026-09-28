package rotini

import (
	"context"
	"fmt"

	"github.com/go-rotini/rotini"
)

var _ rotini.Handlers = (*rotiniHelpHandlers)(nil)

type rotiniHelpHandlers struct {
	rotini.DefaultCascadingPreRun
	rotini.DefaultPreRun
	rotini.DefaultPostRun
	rotini.DefaultCascadingPostRun
}

func (*rotiniHelpHandlers) Run(ctx context.Context, rtx *rotini.Context) {
	inputs, err := rotini.Collect[RotiniHelpInputs](rtx)
	if err != nil {
		rtx.RecordError(err)
		rtx.HaltWithCode(1)
		return
	}

	args := inputs.RotiniHelp.Arguments
	flags := inputs.RotiniHelp.Flags

	if flags.Help {
		fmt.Fprintln(rtx.Stdout, HelpRotiniHelp)
		rtx.HaltWithCode(0)
		return
	}

	help, err := Help(args.Command...)
	if err != nil {
		rtx.RecordError(err)
		rtx.HaltWithCode(1)
		return
	}

	fmt.Fprintln(rtx.Stdout, help)
}
