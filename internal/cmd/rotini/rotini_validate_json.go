package rotini

import (
	"errors"

	"github.com/go-rotini/rotini"
	"github.com/go-rotini/rotini/internal/codegen"
)

// validateJSON runs `rotini validate --format json`: one JSON object listing every problem on
// stdout, nothing else there, and exit 1 when any problem is an error.
func validateJSON(rtx *rotini.Context, specPath string, flags RotiniValidateFlags) {
	if flags.Watch {
		rtx.HaltWith(rotini.UsageError(errors.New("--format json can't be combined with --watch; a watch reports each pass as it happens")))
		return
	}
	spec, conf, err := codegen.ResolvePaths(specPath, flags.ConfFilePath)
	if err != nil {
		rtx.HaltWith(rotini.UsageError(err))
		return
	}
	release, err := releaseOf(rtx, spec, conf, flags.Release)
	if err != nil {
		rtx.HaltWith(rotini.UsageError(err))
		return
	}
	rtx.SetDependencyIfAbsent(validateDep, codegen.NewProcessor(rtx.Version()).Validate)
	validate := rtx.MustGetDependency(validateDep)

	var warnings []error
	failed := validate(spec, conf, false, flags.Fail, release, func(string, error) {}, func(w []error) { warnings = append(warnings, w...) })
	out := RotiniValidateOutput{Problems: []RotiniValidateOutputProblemsItem{}}
	add := func(err error, warning bool) {
		r := codegen.ProblemRecordOf(err, warning)
		out.Problems = append(out.Problems, RotiniValidateOutputProblemsItem{
			Severity: r.Severity, Document: r.Document, File: r.File, Line: r.Line, Col: r.Col,
			Pointer: r.Pointer, Message: r.Message, Hint: r.Hint,
		})
	}
	if failed != nil {
		for _, p := range flatten(failed) {
			add(p, false)
		}
	}
	for _, w := range warnings {
		add(w, true)
	}
	if err := rtx.WriteOutput(out, "json", nil); err != nil {
		haltWithWriteError(rtx, err)
		return
	}
	if failed != nil {
		rtx.HaltWithCode(1)
		return
	}
	rtx.HaltWithCode(0)
}
