package rotini

import (
	"context"
	"fmt"

	"github.com/go-rotini/rotini"
	"github.com/go-rotini/rotini/internal/codegen"
)

var _ rotini.Handlers = (*rotiniGenerateHandlers)(nil)

type rotiniGenerateHandlers struct {
	rotini.DefaultCascadingPreRun
	rotini.DefaultPreRun
	rotini.DefaultPostRun
	rotini.DefaultCascadingPostRun
}

func (*rotiniGenerateHandlers) Run(ctx context.Context, rtx *rotini.Context) {
	parser := rtx.MustGet[*rotini.Parser](rotini.KeyParser)

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

	version := rtx.MustGet[string](KeyRotiniVersion)
	rtx.BindIfAbsent("generate", codegen.NewProcessor(version).Generate)
	generate := rtx.MustGet[codegen.GenerateFn]("generate")

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
		// Anything the pass removed. Generating is not supposed to be destructive, so
		// on the rare occasion it is, it says so.
		func(notices []error) {
			for _, n := range notices {
				fmt.Fprintln(rtx.Stderr, "Note:", n)
			}
		},
	)

	if err != nil {
		rtx.RecordError(err)
		rtx.SignalExit(1)
		return
	}

	rtx.SignalExit(0)
}
