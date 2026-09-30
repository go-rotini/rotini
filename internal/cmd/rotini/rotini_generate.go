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
	parser := rtx.Parser()

	var inputs RotiniGenerateInputs
	if err := parser.Parse(rtx, &inputs); err != nil {
		rtx.HaltWith(err)
		return
	}

	args := inputs.RotiniGenerate.Arguments
	flags := inputs.RotiniGenerate.Flags

	if flags.Help {
		fmt.Fprintln(rtx.Stdout, HelpRotiniGenerate)
		return
	}

	fmt.Fprintf(rtx.Stdout, "spec: %s\nconf: %s\n", args.SpecFilePath, flags.ConfFilePath)

	version := rtx.Version()
	rtx.BindIfAbsent("generate", codegen.NewProcessor(version).Generate)
	generate := rtx.MustGet[codegen.GenerateFn]("generate")

	err := generate(
		args.SpecFilePath,
		flags.ConfFilePath,
		flags.Watch,
		func(result string, err error) {
			if err != nil {
				for _, problem := range flatten(err) {
					fmt.Fprintln(rtx.Stderr, "Error:", problem)
				}
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
		// One outcome per problem, as validate records them: the generator joins its findings
		// into one error, and the funnel's "Error: %s" would mark only the first line.
		problems := flatten(err)
		for _, problem := range problems[:len(problems)-1] {
			rtx.RecordError(problem)
		}
		rtx.HaltWith(problems[len(problems)-1])
		return
	}

	rtx.HaltWithCode(0)
}
