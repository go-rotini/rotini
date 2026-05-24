package rth

import (
	"context"
	"fmt"
	"os"

	"github.com/go-rotini/rotini"
	"github.com/go-rotini/rotini/cmd/rotini/rtg"
	"github.com/go-rotini/rotini/internal"
)

type rotiniInitializeHandlers struct{}

var _ rotini.CommandHandlers = (*rotiniInitializeHandlers)(nil)

func (*rotiniInitializeHandlers) CascadingPreRun(ctx context.Context, rtx rotini.Context) {
}

func (*rotiniInitializeHandlers) PreRun(ctx context.Context, rtx rotini.Context) {
}

func (*rotiniInitializeHandlers) Run(ctx context.Context, rtx rotini.Context) {
	inputs, err := rotini.Parse[rtg.RotiniInitializeInputs](rtx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "rotini:", err)
		rtx.Exit(1)
		return
	}
	in := inputs.RotiniInitialize

	if in.Flags.Help {
		fmt.Println(helpTextRotiniInitialize)
		return
	}

	if err := internal.Initialize(in.Arguments.Name, in.Flags.Format, in.Flags.Force, in.Flags.Into); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		rtx.Exit(1)
		return
	}
}

func (*rotiniInitializeHandlers) PostRun(ctx context.Context, rtx rotini.Context) {
}

func (*rotiniInitializeHandlers) CascadingPostRun(ctx context.Context, rtx rotini.Context) {
}
