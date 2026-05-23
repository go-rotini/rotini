package rth

import (
	"context"
	"fmt"
	"os"

	"github.com/go-rotini/rotini"
	"github.com/go-rotini/rotini/cmd/rotini/rtg"
)

type rotiniCompletionHandlers struct{}

var _ rotini.CommandHandlers = (*rotiniCompletionHandlers)(nil)

func (*rotiniCompletionHandlers) CascadingPreRun(ctx context.Context, rtx rotini.Context) {
}

func (*rotiniCompletionHandlers) PreRun(ctx context.Context, rtx rotini.Context) {
}

func (*rotiniCompletionHandlers) Run(ctx context.Context, rtx rotini.Context) {
	flags := rotini.Inputs[rtg.RotiniCompletionInputs](rtx).RotiniCompletion.Flags

	if flags.Help {
		fmt.Println(getRotiniHelp(helpKeyRotiniCompletion))
		return
	}

	fmt.Fprintln(os.Stderr, "rotini completion: shell completion is not yet implemented")
	rotini.Exit(rtx, 1)
}

func (*rotiniCompletionHandlers) PostRun(ctx context.Context, rtx rotini.Context) {
}

func (*rotiniCompletionHandlers) CascadingPostRun(ctx context.Context, rtx rotini.Context) {
}
