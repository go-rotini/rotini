package rotini

import (
	"context"
	"fmt"

	"github.com/go-rotini/rotini"
	"github.com/go-rotini/rotini/internal/codegen"
)

var _ rotini.Handlers = (*rotiniValidateHandlers)(nil)

type rotiniValidateHandlers struct {
	rotini.DefaultCascadingPreRun
	rotini.DefaultPreRun
	rotini.DefaultPostRun
	rotini.DefaultCascadingPostRun
}

func (*rotiniValidateHandlers) Run(ctx context.Context, rtx *rotini.Context) {
	parser := rtx.Parser()

	var inputs RotiniValidateInputs
	if err := parser.Parse(rtx, &inputs); err != nil {
		rtx.HaltWith(err)
		return
	}

	args := inputs.RotiniValidate.Arguments
	flags := inputs.RotiniValidate.Flags

	if flags.Help {
		fmt.Fprintln(rtx.Stdout, rtx.Help())
		rtx.HaltWithCode(0)
		return
	}

	fmt.Fprintf(rtx.Stdout, "spec: %s\nconf: %s\n", args.SpecFilePath, flags.ConfFilePath)

	version := rtx.Version()
	rtx.BindIfAbsent("validate", codegen.NewProcessor(version).Validate)
	validate := rtx.MustGet[codegen.ValidateFn]("validate")

	err := validate(
		args.SpecFilePath,
		flags.ConfFilePath,
		flags.Watch,
		flags.Fail,
		func(result string, err error) {
			if err != nil {
				for _, problem := range flatten(err) {
					fmt.Fprintln(rtx.Stderr, "Error:", problem)
				}
				return
			}
			fmt.Fprintln(rtx.Stdout, result)
		},
		// Validator warnings are non-fatal: record them so the funnel
		// reports them (the default prints "Warning: …"); they never fail the run.
		func(warnings []error) {
			for _, w := range warnings {
				rtx.RecordWarning(w)
			}
		},
	)

	if err != nil {
		// Record each problem SEPARATELY, the way the warnings above already are. The
		// validator joins its findings into one error, and a funnel printing
		// "Error: %s" then marks only the first line of it — so a three-problem report
		// had one line starting "Error:" and two that were prefix-identical to the
		// informational "spec: <path>" banner. `grep '^Error:'` found one problem in
		// three, and an editor parsing the output could not tell a finding from a header.
		for _, problem := range flatten(err) {
			rtx.RecordError(problem)
		}
		rtx.HaltWithCode(1)
		return
	}

	rtx.HaltWithCode(0)
}

// flatten expands an errors.Join tree into its leaves, so each validation problem is recorded
// as its own outcome. A non-joined error is its own only leaf.
func flatten(err error) []error {
	joined, ok := err.(interface{ Unwrap() []error })
	if !ok {
		return []error{err}
	}
	var out []error
	for _, e := range joined.Unwrap() {
		out = append(out, flatten(e)...)
	}
	if len(out) == 0 {
		return []error{err}
	}
	return out
}
