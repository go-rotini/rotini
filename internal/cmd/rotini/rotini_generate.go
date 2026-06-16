package rotini

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
	parser := rotini.MustGet[*rotini.Parser](rtx, rotini.KeyParser)

	var inputs RotiniGenerateInputs
	if err := parser.Parse(rtx, &inputs); err != nil {
		rtx.RecordError(err)
		rtx.SignalExit(1)
		return
	}

	args := inputs.RotiniGenerate.Arguments
	flags := inputs.RotiniGenerate.Flags

	if flags.Help {
		fmt.Fprintln(rtx.Stdout, HelpRotiniGenerate)
		return
	}

	fmt.Fprintf(rtx.Stdout, "spec: %s\nconf: %s\n", args.SpecFilePath, flags.ConfFilePath)

	v := rotini.MustGet[*rotini.Versioner](rtx, rotini.KeyVersioner)
	rtx.BindIfAbsent("generate", internal.NewProcessor(v.VersionSemantic).Generate)
	generate := rotini.MustGet[internal.GenerateFn](rtx, "generate")

	err := generate(
		args.SpecFilePath,
		flags.ConfFilePath,
		flags.Watch,
		func(result string, err error) {
			if err != nil {
				fmt.Fprintln(rtx.Stderr, "Error:", err)
				return
			}
			fmt.Fprintln(rtx.Stdout, result)
		},
	)

	if err != nil {
		rtx.RecordError(err)
		rtx.SignalExit(1)
		return
	}

	rtx.SignalExit(0)
}
