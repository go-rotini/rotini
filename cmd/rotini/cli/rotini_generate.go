package cli

import (
	"context"
	"fmt"

	"github.com/go-rotini/rotini"
	"github.com/go-rotini/rotini/internal"
)

type rotiniGenerateHandlers struct {
	rotini.DefaultCascadingPreRun
	rotini.DefaultPreRun
	rotini.DefaultPostRun
	rotini.DefaultCascadingPostRun
}

var _ rotini.CommandHandlers = (*rotiniGenerateHandlers)(nil)

func (*rotiniGenerateHandlers) Run(ctx context.Context, rtx *rotini.Context) {
	parser := rotini.MustGet[*rotini.Parser](rtx, "parser")

	var inputs RotiniGenerateInputs
	if err := parser.Parse(rtx, &inputs); err != nil {
		fmt.Fprintln(rtx.Stderr, "rotini:", err)
		rtx.Exit(1)
		return
	}

	args := inputs.RotiniGenerate.Arguments
	flags := inputs.RotiniGenerate.Flags

	if flags.Help {
		fmt.Fprintln(rtx.Stdout, HelpRotiniGenerate)
		return
	}

	fmt.Fprintf(rtx.Stdout, "spec: %s\nconf: %s\n\n", args.SpecFilePath, flags.ConfFilePath)

	rtx.BindIfAbsent("generate", internal.Generate)
	generate := rotini.MustGet[internal.GenerateFn](rtx, "generate")
	version := rotini.MustGet[string](rtx, "version")

	err := generate(
		args.SpecFilePath,
		flags.ConfFilePath,
		flags.Watch,
		version,
		func(result string, err error) {
			if err != nil {
				fmt.Fprintln(rtx.Stderr, "Error:", err)
				return
			}
			fmt.Fprintln(rtx.Stdout, result)
		},
	)

	if err != nil {
		fmt.Fprintln(rtx.Stderr, "Error:", err)
		rtx.Exit(1)
	}

	rtx.Exit(0)
}
