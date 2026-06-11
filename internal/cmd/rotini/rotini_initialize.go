package rotini

import (
	"context"
	"fmt"

	"github.com/go-rotini/rotini"
	"github.com/go-rotini/rotini/internal"
)

type rotiniInitializeHandlers struct {
	rotini.DefaultCascadingPreRun
	rotini.DefaultPreRun
	rotini.DefaultPostRun
	rotini.DefaultCascadingPostRun
}

var _ rotini.CommandHandlers = (*rotiniInitializeHandlers)(nil)

func (*rotiniInitializeHandlers) Run(ctx context.Context, rtx *rotini.Context) {
	parser := rotini.MustGet[*rotini.Parser](rtx, "parser")

	var inputs RotiniInitializeInputs
	if err := parser.Parse(rtx, &inputs); err != nil {
		fmt.Fprintf(rtx.Stderr, "Error: %v\n\n", err)
		fmt.Fprintln(rtx.Stdout, HelpRotiniInitialize)
		rtx.SignalExit(1)
		return
	}

	args := inputs.RotiniInitialize.Arguments
	flags := inputs.RotiniInitialize.Flags

	if flags.Help {
		fmt.Fprintln(rtx.Stdout, HelpRotiniInitialize)
		rtx.SignalExit(0)
		return
	}

	if args.Name == "" {
		fmt.Fprintln(rtx.Stderr, "Error: a name argument is required")
		rtx.SignalExit(1)
		return
	}

	build := rotini.MustGet[*rotini.Build](rtx, "build")
	rtx.BindIfAbsent("initialize", internal.NewProcessor(build.VersionSemantic).Initialize)
	initialize := rotini.MustGet[internal.InitializeFn](rtx, "initialize")

	if err := initialize(args.Name, flags.Format, flags.Force); err != nil {
		fmt.Fprintln(rtx.Stderr, "Error:", err)
		rtx.SignalExit(1)
		return
	}
}
