package rotini

import (
	"context"
	"fmt"

	"github.com/go-rotini/rotini"
	"github.com/go-rotini/rotini/internal/codegen"
)

var _ rotini.Handler = (*rotiniImportCobraHandler)(nil)

type rotiniImportCobraHandler struct {
	rotini.NoCascadingPreRun
	rotini.NoPreRun
	rotini.NoPostRun
	rotini.NoCascadingPostRun
}

func (*rotiniImportCobraHandler) Run(ctx context.Context, rtx *rotini.Context) {
	inputs, err := rtx.Inputs[RotiniImportCobraInputs]()
	if err != nil {
		haltWithInputError(rtx, err)
		return
	}
	args := inputs.RotiniImportCobra.Arguments
	flags := inputs.RotiniImportCobra.Flags
	opts := codegen.ImportOptions{
		Package:         args.Package,
		Root:            flags.Root,
		Name:            flags.Name,
		Dir:             flags.Dir,
		Strict:          flags.Strict,
		Format:          flags.Format,
		Tags:            flags.Tags,
		Timeout:         flags.Timeout,
		ImporterVersion: flags.ImporterVersion,
		Force:           flags.Force,
	}

	processor := codegen.NewProcessor(rtx.Version())
	dep, run := importDep, processor.Import
	if flags.DryRun {
		dep, run = importDryRunDep, processor.ImportDryRun
	}
	rtx.SetDependencyIfAbsent(dep, run)
	imported, err := rtx.MustGetDependency(dep)(ctx, opts)

	// The loss report comes first, whether or not the spec could be written.
	for _, note := range imported.Notes {
		fmt.Fprintln(rtx.Stderr, note)
	}
	if imported.Summary != "" {
		fmt.Fprintln(rtx.Stderr, imported.Summary)
	}
	if err != nil {
		haltWithProblems(rtx, err)
		return
	}

	if _, err := fmt.Fprintf(rtx.Stdout, "spec: %s\nconf: %s\n", imported.Spec, imported.Conf); err != nil {
		haltWithWriteError(rtx, err)
		return
	}
	if flags.DryRun {
		reportPlanned(rtx, imported.Result, imported.Changes)
		return
	}
	if _, err := fmt.Fprintln(rtx.Stdout, imported.Result); err != nil {
		haltWithWriteError(rtx, err)
		return
	}
	if !requiresRuntime(".") {
		rtx.RecordWarning(fmt.Errorf("go.mod does not require %s yet; run `go get %s` before building the imported CLI", runtimeModule, runtimeModule))
	}
}
