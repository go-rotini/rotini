package rth

import (
	"context"
	"fmt"
	"os"

	"github.com/go-rotini/rotini"
	"github.com/go-rotini/rotini/cmd/rotini/rtg"
	"github.com/go-rotini/rotini/rtk"
)

type rotiniVersionHandlers struct{}

var _ rotini.CommandHandlers = (*rotiniVersionHandlers)(nil)

func (*rotiniVersionHandlers) CascadingPreRun(ctx context.Context, rtx rotini.Context) {
}

func (*rotiniVersionHandlers) PreRun(ctx context.Context, rtx rotini.Context) {
}

func (*rotiniVersionHandlers) Run(ctx context.Context, rtx rotini.Context) {
	inputs, err := rtk.Parse[rtg.RotiniVersionInputs](rtx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "rotini:", err)
		rtx.Exit(1)
		return
	}

	if inputs.RotiniVersion.Flags.Help {
		fmt.Println(helpTextRotiniVersion)
		return
	}

	fmt.Println(rtg.Version)
}

func (*rotiniVersionHandlers) PostRun(ctx context.Context, rtx rotini.Context) {
}

func (*rotiniVersionHandlers) CascadingPostRun(ctx context.Context, rtx rotini.Context) {
}
