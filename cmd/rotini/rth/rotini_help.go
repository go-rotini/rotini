package rth

import (
	"context"
	"fmt"
	"os"

	"github.com/go-rotini/rotini"
	"github.com/go-rotini/rotini/cmd/rotini/rtg"
	"github.com/go-rotini/rotini/rtk"
)

type rotiniHelpHandlers struct {
	rotini.DefaultCascadingPreRun
	rotini.DefaultPreRun
	rotini.DefaultPostRun
	rotini.DefaultCascadingPostRun
}

var _ rotini.CommandHandlers = (*rotiniHelpHandlers)(nil)

func (*rotiniHelpHandlers) Run(ctx context.Context, rtx *rotini.Context) {
	parser := rotini.MustGet[*rtk.Parser](rtx, "parser")

	var inputs rtg.RotiniHelpInputs
	if err := parser.Parse(rtx, &inputs); err != nil {
		fmt.Fprintln(os.Stderr, "rotini:", err)
		rtx.Exit(1)
		return
	}
	in := inputs.RotiniHelp
	if in.Flags.Help {
		fmt.Fprintln(os.Stdout, rtg.HelpRotiniHelp)
		return
	}

	text, err := rtg.Help(in.Arguments.Command...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n\n%s", err, rtg.HelpRotini)
		rtx.Exit(1)
		return
	}
	fmt.Fprintln(os.Stdout, text)
}
