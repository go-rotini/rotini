package rth

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

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
	in := rotini.Inputs[rtg.RotiniCompletionInputs](rtx).RotiniCompletion

	if in.Flags.Help {
		fmt.Println(helpTextRotiniCompletion)
		return
	}

	script, err := rotini.CompletionScript(filepath.Base(os.Args[0]), in.Arguments.Shell)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		rotini.Exit(rtx, 1)
		return
	}
	fmt.Print(script)
}

func (*rotiniCompletionHandlers) PostRun(ctx context.Context, rtx rotini.Context) {
}

func (*rotiniCompletionHandlers) CascadingPostRun(ctx context.Context, rtx rotini.Context) {
}
