package rth

import (
	"context"
	"fmt"
	"os"

	"github.com/go-rotini/rotini"
	"github.com/go-rotini/rotini/cmd/rotini/rtg"
	"github.com/go-rotini/rotini/internal"
	"github.com/go-rotini/rotini/rtk"
)

type rotiniGenerateHandlers struct{}

var _ rotini.CommandHandlers = (*rotiniGenerateHandlers)(nil)

func (*rotiniGenerateHandlers) CascadingPreRun(ctx context.Context, rtx rotini.Context) {
}

func (*rotiniGenerateHandlers) PreRun(ctx context.Context, rtx rotini.Context) {
}

func (*rotiniGenerateHandlers) Run(ctx context.Context, rtx rotini.Context) {
	parser, err := rotini.Get[*rtk.Parser](rtx, rtk.ParserKey)
	if err != nil {
		fmt.Fprintln(os.Stderr, "rotini:", err)
		rtx.Exit(1)
		return
	}

	inputs, err := rtk.Parse[rtg.RotiniGenerateInputs](parser, rtx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "rotini:", err)
		rtx.Exit(1)
		return
	}
	in := inputs.RotiniGenerate

	if in.Flags.Help {
		fmt.Println(helpTextRotiniGenerate)
		return
	}

	if err := internal.Generate(in.Arguments.File, in.Flags.Config); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		rtx.Exit(1)
		return
	}
}

func (*rotiniGenerateHandlers) PostRun(ctx context.Context, rtx rotini.Context) {
}

func (*rotiniGenerateHandlers) CascadingPostRun(ctx context.Context, rtx rotini.Context) {
}
