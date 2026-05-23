package rth

import (
	"context"
	"fmt"
	"os"

	"github.com/go-rotini/rotini"
	"github.com/go-rotini/rotini/cmd/rotini/rtg"
	"github.com/go-rotini/rotini/internal"
)

type rotiniValidateHandlers struct{}

var _ rotini.CommandHandlers = (*rotiniValidateHandlers)(nil)

func (*rotiniValidateHandlers) CascadingPreRun(ctx context.Context, rtx rotini.Context) {
}

func (*rotiniValidateHandlers) PreRun(ctx context.Context, rtx rotini.Context) {
}

func (*rotiniValidateHandlers) Run(ctx context.Context, rtx rotini.Context) {
	in := rotini.Inputs[rtg.RotiniValidateInputs](rtx).RotiniValidate
	if in.Flags.Help {
		fmt.Println(getRotiniHelp(helpKeyRotiniValidate))
		return
	}
	if err := internal.Validate(in.Arguments.File, ""); err != nil {
		fmt.Fprintln(os.Stderr, "rotini validate:", err)
		rotini.Exit(rtx, 1)
		return
	}
	fmt.Println(in.Arguments.File, "is valid")
}

func (*rotiniValidateHandlers) PostRun(ctx context.Context, rtx rotini.Context) {
}

func (*rotiniValidateHandlers) CascadingPostRun(ctx context.Context, rtx rotini.Context) {
}
