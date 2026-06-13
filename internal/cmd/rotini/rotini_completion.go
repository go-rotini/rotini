package rotini

import (
	"context"
	"fmt"

	"github.com/go-rotini/rotini"
)

type rotiniCompletionHandlers struct {
	rotini.DefaultCascadingPreRun
	rotini.DefaultPreRun
	rotini.DefaultPostRun
	rotini.DefaultCascadingPostRun
}

var _ rotini.CommandHandlers = (*rotiniCompletionHandlers)(nil)

func (*rotiniCompletionHandlers) Run(ctx context.Context, rtx *rotini.Context) {
	inputs, err := rotini.Collect[RotiniCompletionInputs](rtx)
	if err != nil {
		rtx.RecordErr(err)
		rtx.SignalExit(rotini.ExitUsage)
		return
	}

	args := inputs.RotiniCompletion.Arguments
	flags := inputs.RotiniCompletion.Flags

	if flags.Help {
		fmt.Fprintln(rtx.Stdout, HelpRotiniCompletion)
		rtx.SignalExit(0)
		return
	}

	// The shell arg is enum-constrained in the spec, so an unsupported shell
	// never reaches here — Parse already rejected it.
	script, err := Completion(args.Shell)
	if err != nil {
		rtx.RecordErr(err)
		rtx.SignalExit(rotini.ExitUsage)
		return
	}

	// The `source <(rotini completion zsh)` idiom: the script, stdout, nothing else.
	fmt.Fprintln(rtx.Stdout, script)
}
