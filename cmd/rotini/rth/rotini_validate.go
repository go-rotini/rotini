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

type rotiniValidateHandlers struct{}

var _ rotini.CommandHandlers = (*rotiniValidateHandlers)(nil)

func (*rotiniValidateHandlers) CascadingPreRun(ctx context.Context, rtx rotini.Context) {
}

func (*rotiniValidateHandlers) PreRun(ctx context.Context, rtx rotini.Context) {
}

func (*rotiniValidateHandlers) Run(ctx context.Context, rtx rotini.Context) {
	parser, ok := rtx.Get("parser").(*rtk.Parser)
	if !ok {
		fmt.Fprintln(os.Stderr, "rotini: parser not bound")
		rtx.Exit(1)
		return
	}

	var inputs rtg.RotiniValidateInputs
	if err := parser.Parse(rtx, &inputs); err != nil {
		fmt.Fprintln(os.Stderr, "rotini:", err)
		rtx.Exit(1)
		return
	}
	in := inputs.RotiniValidate

	if in.Flags.Help {
		fmt.Println(helpTextRotiniValidate)
		return
	}

	if err := internal.Validate(in.Arguments.File, ""); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		rtx.Exit(1)
		return
	}
}

func (*rotiniValidateHandlers) PostRun(ctx context.Context, rtx rotini.Context) {
}

func (*rotiniValidateHandlers) CascadingPostRun(ctx context.Context, rtx rotini.Context) {
}
