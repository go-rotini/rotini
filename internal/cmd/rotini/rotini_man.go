package rotini

import (
	"context"
	"fmt"

	"github.com/go-rotini/rotini"
)

type rotiniManHandlers struct {
	rotini.DefaultCascadingPreRun
	rotini.DefaultPreRun
	rotini.DefaultPostRun
	rotini.DefaultCascadingPostRun
}

var _ rotini.CommandHandlers = (*rotiniManHandlers)(nil)

func (*rotiniManHandlers) Run(ctx context.Context, rtx *rotini.Context) {
	parser := rotini.MustGet[*rotini.Parser](rtx, rotini.KeyParser)

	var inputs RotiniManInputs
	if err := parser.Parse(rtx, &inputs); err != nil {
		fmt.Fprintf(rtx.Stderr, "Error: %v\n\n", err)
		fmt.Fprintln(rtx.Stdout, HelpRotiniMan)
		rtx.SignalExit(rotini.ExitUsage)
		return
	}

	args := inputs.RotiniMan.Arguments
	flags := inputs.RotiniMan.Flags

	if flags.Help {
		fmt.Fprintln(rtx.Stdout, HelpRotiniMan)
		rtx.SignalExit(0)
		return
	}

	page, err := Man(args.Commands...)
	if err != nil {
		fmt.Fprintf(rtx.Stderr, "Error: %v\n\n", err)
		fmt.Fprintln(rtx.Stdout, HelpRotiniMan)
		rtx.SignalExit(1)
		return
	}

	fmt.Fprintln(rtx.Stdout, page)
}
