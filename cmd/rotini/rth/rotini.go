package rth

import (
	"context"
	"fmt"
	"os"

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
	inputs, err := rotini.Parse[rtg.RotiniInputs](rtx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "rotini:", err)
		rotini.Exit(rtx, 2)
		return
	}
	flags := inputs.Rotini.Flags

	switch {
	case flags.Help:
		fmt.Println(helpTextRotini)
	case flags.Version:
		fmt.Println(rtg.Version)
	default:
		fmt.Println(helpTextRotini)
		rotini.Exit(rtx, 1)
	}
}

func (*rotiniHandlers) PostRun(ctx context.Context, rtx rotini.Context) {
}

func (*rotiniHandlers) CascadingPostRun(ctx context.Context, rtx rotini.Context) {
}
