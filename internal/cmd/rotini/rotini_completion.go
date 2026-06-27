package rotini

import (
	"context"
	"fmt"

	rotini "github.com/go-rotini/rotini/internal/runtime"
)

var _ rotini.Handlers = (*rotiniCompletionHandlers)(nil)

type rotiniCompletionHandlers struct {
	rotini.DefaultCascadingPreRun
	rotini.DefaultPreRun
	rotini.DefaultPostRun
	rotini.DefaultCascadingPostRun
}

func (*rotiniCompletionHandlers) Run(ctx context.Context, rtx *rotini.Context) {
	inputs, err := rotini.Collect[RotiniCompletionInputs](rtx)
	if err != nil {
		rtx.RecordError(err)
		rtx.SignalExit(1)
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
		rtx.RecordError(err)
		rtx.SignalExit(1)
		return
	}

	// The `source <(rotini completion zsh)` idiom: the script, stdout, nothing else.
	fmt.Fprintln(rtx.Stdout, script)
}
