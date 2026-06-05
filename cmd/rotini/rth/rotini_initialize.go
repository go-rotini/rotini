package rth

import (
	"context"
	"fmt"
	"os"

	"github.com/go-rotini/rotini"
	"github.com/go-rotini/rotini/cmd/rotini/rtg"
	"github.com/go-rotini/rotini/internal"
	"github.com/go-rotini/rotini/rtk"
)

type rotiniInitializeHandlers struct {
	rotini.DefaultCascadingPreRun
	rotini.DefaultPreRun
	rotini.DefaultPostRun
	rotini.DefaultCascadingPostRun
}

var _ rotini.CommandHandlers = (*rotiniInitializeHandlers)(nil)

func (*rotiniInitializeHandlers) Run(ctx context.Context, rtx *rotini.Context) {
	parser := rotini.MustGet[*rtk.Parser](rtx, "parser")

	var inputs rtg.RotiniInitializeInputs
	if err := parser.Parse(rtx, &inputs); err != nil {
		fmt.Fprintln(os.Stderr, "rotini:", err)
		rtx.Exit(1)
		return
	}
	in := inputs.RotiniInitialize

	if in.Flags.Help {
		fmt.Fprintln(os.Stdout, rtg.HelpRotiniInitialize)
		return
	}

	name := in.Arguments.Name
	if name == "" {
		fmt.Fprintln(os.Stderr, "Error: a name argument is required")
		rtx.Exit(1)
		return
	}

	if err := internal.Initialize(name, in.Flags.Format, in.Flags.Force, in.Flags.Into); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		rtx.Exit(1)
		return
	}
}
