package rotini

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/go-rotini/rotini"
	"github.com/go-rotini/rotini/internal/codegen"
	"github.com/go-rotini/rotini/internal/contractdiff"
)

var _ rotini.HandlerE = (*rotiniDiffHandler)(nil)

// diffDep is the codegen entry point the diff handler calls; see dependencies.go.
var diffDep = rotini.NewDependency[codegen.DiffFn]("rotini.diff")

type rotiniDiffHandler struct {
	rotini.NoCascadingPreRunE
	rotini.NoPreRunE
	rotini.NoPostRunE
	rotini.NoCascadingPostRunE
}

func (*rotiniDiffHandler) Run(ctx context.Context, rtx *rotini.Context) error {
	inputs, err := rtx.Inputs[RotiniDiffInputs]()
	if err != nil {
		return inputError(err)
	}
	args := inputs.RotiniDiff.Arguments
	flags := inputs.RotiniDiff.Flags
	for _, source := range []string{args.Old, args.New} {
		// git would read such a revision as one of its own options.
		if strings.HasPrefix(source, "git:-") {
			return rotini.UsageError(fmt.Errorf("%s: a git revision can't start with '-'", source))
		}
	}

	// The current contract is built from the spec, so it must be found; with both contracts
	// given, the spec is optional and only locates the conf.
	spec, conf, err := codegen.ResolvePaths(flags.SpecFilePath, flags.ConfFilePath)
	if err != nil {
		if args.New == "" || flags.SpecFilePath != "" {
			return rotini.UsageError(err)
		}
		spec, conf = "", flags.ConfFilePath
	}

	release, err := releaseOf(rtx, spec, conf, flags.Release)
	if err != nil {
		return rotini.UsageError(err)
	}

	rtx.SetDependencyIfAbsent(diffDep, codegen.NewProcessor(rtx.Version()).Diff)
	report, err := rtx.MustDependency(diffDep)(spec, conf, args.Old, args.New, release)
	if err != nil {
		return problems(rtx, err)
	}

	for _, a := range report.UnmatchedAccepts {
		fmt.Fprintf(rtx.Stderr, "Warning: diff.accept entry %s at %q matched no change; remove it\n", a.Rule, a.Where)
	}
	var out RotiniDiffOutput
	if err := convert(report, &out); err != nil {
		return rotini.InternalError(err)
	}
	render := func(w io.Writer, _ string, _ RotiniDiffOutput) error { return report.WriteText(w) }
	if err := rtx.WriteOutput(out, flags.Format, render); err != nil {
		return err
	}
	if report.Failed(flags.FailOn) {
		rtx.HaltWithCode(RotiniDiffExitBreaking)
		return nil
	}
	rtx.HaltWithCode(RotiniDiffExitOK)
	return nil
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
