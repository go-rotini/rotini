package rth

import (
	"context"
	"fmt"

	"github.com/go-rotini/rotini"
	"github.com/go-rotini/rotini/cmd/rotini/rtg"
)

type rotiniCompletionHandlers struct {
	rotini.DefaultCascadingPreRun
	rotini.DefaultPreRun
	rotini.DefaultPostRun
	rotini.DefaultCascadingPostRun
}

var _ rotini.CommandHandlers = (*rotiniCompletionHandlers)(nil)

func (*rotiniCompletionHandlers) Run(ctx context.Context, rtx *rotini.Context) {
	parser := rotini.MustGet[*rotini.Parser](rtx, "parser")

	var inputs rtg.RotiniCompletionInputs
	if err := parser.Parse(rtx, &inputs); err != nil {
		fmt.Fprintf(rtx.Stderr, "Error: %v\n\n", err)
		fmt.Fprintln(rtx.Stdout, rtg.HelpRotiniCompletion)
		rtx.Exit(1)
		return
	}

	args := inputs.RotiniCompletion.Arguments
	flags := inputs.RotiniCompletion.Flags

	if flags.Help {
		fmt.Fprintln(rtx.Stdout, rtg.HelpRotiniCompletion)
		rtx.Exit(0)
		return
	}

	script, err := rtg.Completion(args.Shell)
	if err != nil {
		fmt.Fprintln(rtx.Stderr, "Error:", err)
		rtx.Exit(1)
		return
	}

	fmt.Fprint(rtx.Stdout, script)
}
