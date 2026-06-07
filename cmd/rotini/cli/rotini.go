package cli

import (
	"context"
	"fmt"

	"github.com/go-rotini/rotini"
)

type rotiniHandlers struct {
	rotini.DefaultCascadingPreRun
	rotini.DefaultPreRun
	rotini.DefaultPostRun
	rotini.DefaultCascadingPostRun
}

var _ rotini.CommandHandlers = (*rotiniHandlers)(nil)

func (*rotiniHandlers) Run(ctx context.Context, rtx *rotini.Context) {
	parser := rotini.MustGet[*rotini.Parser](rtx, "parser")

	var inputs RotiniInputs
	if err := parser.Parse(rtx, &inputs); err != nil {
		fmt.Fprintf(rtx.Stderr, "Error: %v\n\n", err)
		fmt.Fprintln(rtx.Stdout, HelpRotini)
		rtx.Exit(1)
		return
	}

	flags := inputs.Rotini.Flags
	switch {
	case flags.Help:
		fmt.Fprintln(rtx.Stdout, HelpRotini)
		rtx.Exit(0)
		return
	case flags.Version:
		version := rotini.MustGet[string](rtx, "version")
		fmt.Fprintln(rtx.Stdout, version)
		rtx.Exit(0)
		return
	default:
		fmt.Fprintln(rtx.Stdout, HelpRotini)
		rtx.Exit(1)
		return
	}
}
