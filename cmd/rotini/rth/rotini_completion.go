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
		fmt.Fprintln(rtx.Stderr, "rotini:", err)
		rtx.Exit(2)
		return
	}
	in := inputs.RotiniCompletion

	if in.Flags.Help {
		fmt.Fprintln(rtx.Stdout, rtg.HelpRotiniCompletion)
		return
	}

	script, err := rtg.Completion(in.Arguments.Shell)
	if err != nil {
		fmt.Fprintln(rtx.Stderr, "Error:", err)
		rtx.Exit(1)
		return
	}
	fmt.Fprint(rtx.Stdout, script)
}
