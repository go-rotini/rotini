package rotini

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/go-rotini/rotini"
	"github.com/go-rotini/rotini/internal/codegen"
	"github.com/go-rotini/rotini/internal/contractdiff"
)

var _ rotini.Handler = (*rotiniDiffHandler)(nil)

// diffDep is the codegen entry point the diff handler calls; see dependencies.go.
var diffDep = rotini.NewDependency[codegen.DiffFn]("rotini.diff")

type rotiniDiffHandler struct {
	rotini.NoCascadingPreRun
	rotini.NoPreRun
	rotini.NoPostRun
	rotini.NoCascadingPostRun
}

func (*rotiniDiffHandler) Run(ctx context.Context, rtx *rotini.Context) {
	inputs, err := rtx.Inputs[RotiniDiffInputs]()
	if err != nil {
		haltWithInputError(rtx, err)
		return
	}
	args := inputs.RotiniDiff.Arguments
	flags := inputs.RotiniDiff.Flags

	// The current contract is built from the spec, so it must be found; with both contracts
	// given, the spec is optional and only locates the conf.
	spec, conf, err := codegen.ResolvePaths(flags.SpecFilePath, flags.ConfFilePath)
	if err != nil {
		if args.New == "" || flags.SpecFilePath != "" {
			rtx.HaltWith(rotini.UsageError(err))
			return
		}
		spec, conf = "", flags.ConfFilePath
	}

	release, err := releaseOf(rtx, spec, conf, flags.Release)
	if err != nil {
		rtx.HaltWith(rotini.UsageError(err))
		return
	}

	rtx.SetDependencyIfAbsent(diffDep, codegen.NewProcessor(rtx.Version()).Diff)
	report, err := rtx.MustGetDependency(diffDep)(spec, conf, args.Old, args.New, release)
	if err != nil {
		haltWithProblems(rtx, err)
		return
	}

	for _, a := range report.UnmatchedAccepts {
		fmt.Fprintf(rtx.Stderr, "Warning: diff.accept entry %s at %q matched no change; remove it\n", a.Rule, a.Where)
	}
	var out RotiniDiffOutput
	if err := convert(report, &out); err != nil {
		rtx.HaltWith(rotini.InternalError(err))
		return
	}
	render := func(w io.Writer, _ string, _ RotiniDiffOutput) error { return report.WriteText(w) }
	if err := rtx.WriteOutput(out, flags.Format, render); err != nil {
		rtx.HaltWith(err)
		return
	}
	if report.Failed(flags.FailOn) {
		rtx.HaltWithCode(RotiniDiffExitBreaking)
		return
	}
	rtx.HaltWithCode(RotiniDiffExitOk)
}

// convert copies the report into the declared output type, which has the same JSON shape. Both
// lists are written as arrays, empty or not.
func convert(report contractdiff.Report, out *RotiniDiffOutput) error {
	b, err := json.Marshal(report)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, out); err != nil {
		return err
	}
	if out.Findings == nil {
		out.Findings = []RotiniDiffOutputFindingsItem{}
	}
	if out.UnmatchedAccepts == nil {
		out.UnmatchedAccepts = []RotiniDiffOutputUnmatchedAcceptsItem{}
	}
	return nil
}
