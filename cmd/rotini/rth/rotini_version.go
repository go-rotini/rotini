package rth

import (
	"context"
	"fmt"

	"github.com/go-rotini/rotini"
	"github.com/go-rotini/rotini/cmd/rotini/rtg"
)

type rotiniVersionHandlers struct{}

var _ rotini.CommandHandlers = (*rotiniVersionHandlers)(nil)

func (*rotiniVersionHandlers) CascadingPreRun(ctx context.Context, rtx rotini.Context) {
}

func (*rotiniVersionHandlers) PreRun(ctx context.Context, rtx rotini.Context) {
}

func (*rotiniVersionHandlers) Run(ctx context.Context, rtx rotini.Context) {
	flags := rotini.Inputs[rtg.RotiniVersionInputs](rtx).RotiniVersion.Flags

	if flags.Help {
		fmt.Println(getRotiniHelp(helpKeyRotiniVersion))
		return
	}

	fmt.Println(rtg.Version)
}

func (*rotiniVersionHandlers) PostRun(ctx context.Context, rtx rotini.Context) {
}

func (*rotiniVersionHandlers) CascadingPostRun(ctx context.Context, rtx rotini.Context) {
}
