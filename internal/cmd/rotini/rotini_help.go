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
	if answerHelp(rtx, func(in RotiniHelpInputs) bool { return in.RotiniHelp.Flags.Help }) {
		return
	}

	inputs, err := rotini.Collect[RotiniHelpInputs](rtx)
	if err != nil {
		rtx.HaltWith(err)
		return
	}

	help, err := Help(inputs.RotiniHelp.Arguments.Command...)
	if err != nil {
		rtx.HaltWith(rotini.UsageError(err))
		return
	}

	fmt.Fprintln(rtx.Stdout, help)
	rtx.HaltWithCode(0)
}
