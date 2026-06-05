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

type rotiniGenerateHandlers struct {
	rotini.DefaultCascadingPreRun
	rotini.DefaultPreRun
	rotini.DefaultPostRun
	rotini.DefaultCascadingPostRun
}

var _ rotini.CommandHandlers = (*rotiniGenerateHandlers)(nil)

func (*rotiniGenerateHandlers) Run(ctx context.Context, rtx *rotini.Context) {
	parser := rotini.MustGet[*rtk.Parser](rtx, "parser")

	var inputs rtg.RotiniGenerateInputs
	if err := parser.Parse(rtx, &inputs); err != nil {
		fmt.Fprintln(os.Stderr, "rotini:", err)
		rtx.Exit(1)
		return
	}

	args := inputs.RotiniGenerate.Arguments
	flags := inputs.RotiniGenerate.Flags

	if flags.Help {
		fmt.Fprintln(os.Stdout, rtg.HelpRotiniGenerate)
		return
	}

	fmt.Fprintf(os.Stdout, "spec: %s\nconf: %s\n\n", args.SpecFilePath, flags.ConfFilePath)

	err := internal.Generate(
		args.SpecFilePath,
		flags.ConfFilePath,
		flags.Watch,
		func(result string, err error) {
			if err != nil {
				fmt.Fprintln(os.Stderr, "Error:", err)
				return
			}
			fmt.Fprintln(os.Stdout, result)
		},
	)

	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		rtx.Exit(1)
	}
}
