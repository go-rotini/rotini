package rth

import (
	"context"

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
	io := rotini.MustGet[*rtk.IO](rtx, "io")

	var inputs rtg.RotiniGenerateInputs
	if err := parser.Parse(rtx, &inputs); err != nil {
		io.Stderr.Println("rotini:", err)
		rtx.Exit(1)
		return
	}

	args := inputs.RotiniGenerate.Arguments
	flags := inputs.RotiniGenerate.Flags

	if flags.Help {
		io.Stdout.Println(rtg.HelpRotiniGenerate)
		return
	}

	io.Stdout.Printf("spec: %s\nconf: %s\n\n", args.SpecFilePath, flags.ConfFilePath)

	err := internal.Generate(
		args.SpecFilePath,
		flags.ConfFilePath,
		flags.Watch,
		func(result string, err error) {
			if err != nil {
				io.Stderr.Println("Error:", err)
				return
			}
			io.Stdout.Println(result)
		},
	)

	if err != nil {
		io.Stderr.Println("Error:", err)
		rtx.Exit(1)
	}
}
