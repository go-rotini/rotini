package rotini

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/go-rotini/rotini"
	"github.com/go-rotini/rotini/internal/codegen"
)

var _ rotini.Handler = (*rotiniGenerateHandler)(nil)

type rotiniGenerateHandler struct {
	rotini.NoCascadingPreRun
	rotini.NoPreRun
	rotini.NoPostRun
	rotini.NoCascadingPostRun
}

func (*rotiniGenerateHandler) Run(ctx context.Context, rtx *rotini.Context) {
	inputs, err := rtx.Inputs[RotiniGenerateInputs]()
	if err != nil {
		haltWithInputError(rtx, err)
		return
	}
	args := inputs.RotiniGenerate.Arguments
	flags := inputs.RotiniGenerate.Flags

	spec, conf, ok := resolveInputs(rtx, args.SpecFilePath, flags.ConfFilePath)
	if !ok {
		return
	}

	dryRun, err := resolveDryRun(rtx, flags.DryRun, spec, conf)
	if err != nil {
		rtx.HaltWith(err)
		return
	}
	if dryRun && flags.Watch {
		rtx.HaltWith(rotini.UsageError(errors.New("--watch can't be used with a dry run (set by generate.dry_run_env in the conf); pass --no-dry-run to watch")))
		return
	}

	version := rtx.Version()
	if dryRun {
		rtx.SetDependencyIfAbsent(generateDryRunDep, codegen.NewProcessor(version).GenerateDryRun)
		planned, err := rtx.MustGetDependency(generateDryRunDep)(spec, conf, printWarnings(rtx))
		if err != nil {
			haltWithProblems(rtx, err)
			return
		}
		reportPlanned(rtx, planned.Result, planned.Changes)
		return
	}

	rtx.SetDependencyIfAbsent(generateDep, codegen.NewProcessor(version).Generate)
	generate := rtx.MustGetDependency(generateDep)

	// Warnings are validate's, plus each pruned file and the handler audit's findings.
	if err := generate(spec, conf, flags.Watch, printResult(rtx), printWarnings(rtx)); err != nil {
		haltWithProblems(rtx, err)
		return
	}

	rtx.HaltWithCode(0)
}

// resolveDryRun decides whether generate dry-runs: --dry-run or --no-dry-run when given on the
// command line, else the conf's generate.dry_run_env when that variable is set to a true
// value, else not.
func resolveDryRun(rtx *rotini.Context, flag bool, spec, conf string) (bool, error) {
	argv, err := rtx.ArgvInputs[RotiniGenerateInputs]()
	if err != nil {
		return false, err
	}
	if _, given := argv.Set["RotiniGenerate.Flags.DryRun"]; given {
		return flag, nil
	}
	name := codegen.DryRunEnv(spec, conf)
	return name != "" && envTrue(os.Getenv(name)), nil
}

// envTrue reports whether an environment variable's value turns a switch on: 1, true, yes or
// on, in any case, ignoring surrounding space.
func envTrue(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// reportPlanned finishes a dry run's report. With nothing to change it prints the timing line,
// like a generate that wrote nothing, and exits 0. Otherwise it lists each change on stderr
// with a closing count, and exits 2.
func reportPlanned(rtx *rotini.Context, result string, changes []string) {
	if len(changes) == 0 {
		fmt.Fprintln(rtx.Stdout, result)
		rtx.HaltWithCode(0)
		return
	}
	for _, c := range changes {
		fmt.Fprintln(rtx.Stderr, c)
	}
	noun := "changes"
	if len(changes) == 1 {
		noun = "change"
	}
	fmt.Fprintf(rtx.Stderr, "dry run: %d %s not written\n", len(changes), noun)
	rtx.HaltWithCode(exitWouldChange)
}

// exitWouldChange is a dry run's exit code when something would change. 1 stays an error, so
// CI can tell drift from a broken spec.
const exitWouldChange = 2
