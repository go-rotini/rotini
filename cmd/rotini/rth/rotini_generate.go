package rth

import (
	"context"
	"fmt"
	"os"

	"github.com/go-rotini/rotini"
	"github.com/go-rotini/rotini/cmd/rotini/rtg"
	"github.com/go-rotini/rotini/internal"
)

type rotiniGenerateHandlers struct{}

var _ rotini.CommandHandlers = (*rotiniGenerateHandlers)(nil)

func (*rotiniGenerateHandlers) CascadingPreRun(ctx context.Context, rtx rotini.Context) {
}

func (*rotiniGenerateHandlers) PreRun(ctx context.Context, rtx rotini.Context) {
}

func (*rotiniGenerateHandlers) Run(ctx context.Context, rtx rotini.Context) {
	in := rotini.Inputs[rtg.RotiniGenerateInputs](rtx).RotiniGenerate
	if in.Flags.Help {
		fmt.Println(getRotiniHelp(helpKeyRotiniGenerate))
		return
	}
	if err := internal.Generate(in.Arguments.File, in.Flags.Config); err != nil {
		fmt.Fprintln(os.Stderr, "rotini generate:", err)
		rotini.Exit(rtx, 1)
		return
	}
	fmt.Println("generated from", in.Arguments.File)
}

func (*rotiniGenerateHandlers) PostRun(ctx context.Context, rtx rotini.Context) {
}

func (*rotiniGenerateHandlers) CascadingPostRun(ctx context.Context, rtx rotini.Context) {
}
