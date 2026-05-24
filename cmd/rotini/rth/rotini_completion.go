package rth

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/go-rotini/rotini"
	"github.com/go-rotini/rotini/cmd/rotini/rtg"
	"github.com/go-rotini/rotini/rtk"
)

type rotiniCompletionHandlers struct{}

var _ rotini.CommandHandlers = (*rotiniCompletionHandlers)(nil)

func (*rotiniCompletionHandlers) CascadingPreRun(ctx context.Context, rtx rotini.Context) {
}

func (*rotiniCompletionHandlers) PreRun(ctx context.Context, rtx rotini.Context) {
}

func (*rotiniCompletionHandlers) Run(ctx context.Context, rtx rotini.Context) {
	inputs, err := rtk.Parse[rtg.RotiniCompletionInputs](rtx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "rotini:", err)
		rtx.Exit(2)
		return
	}
	in := inputs.RotiniCompletion

	if in.Flags.Help {
		fmt.Println(helpTextRotiniCompletion)
		return
	}

	script, err := rtk.CompletionScript(filepath.Base(os.Args[0]), in.Arguments.Shell)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		rtx.Exit(1)
		return
	}
	fmt.Print(script)
}

func (*rotiniCompletionHandlers) PostRun(ctx context.Context, rtx rotini.Context) {
}

func (*rotiniCompletionHandlers) CascadingPostRun(ctx context.Context, rtx rotini.Context) {
}
