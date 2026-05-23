package rth

import (
	"context"
	"fmt"

	"github.com/go-rotini/rotini"
	"github.com/go-rotini/rotini/cmd/rotini/rtg"
)

type rotiniHandlers struct{}

var _ rotini.CommandHandlers = (*rotiniHandlers)(nil)

func (*rotiniHandlers) CascadingPreRun(ctx context.Context, rtx rotini.Context) {
}

func (*rotiniHandlers) PreRun(ctx context.Context, rtx rotini.Context) {
}

func (*rotiniHandlers) Run(ctx context.Context, rtx rotini.Context) {
	flags := rotini.Inputs[rtg.RotiniInputs](rtx).Rotini.Flags

	switch {
	case flags.Help:
		fmt.Println(getRotiniHelp(helpKeyRotini))
	case flags.Version:
		fmt.Println(rtg.Version)
	default:
		fmt.Println(getRotiniHelp(helpKeyRotini))
		rotini.Exit(rtx, 1)
	}
}

func (*rotiniHandlers) PostRun(ctx context.Context, rtx rotini.Context) {
}

func (*rotiniHandlers) CascadingPostRun(ctx context.Context, rtx rotini.Context) {
}
