package rotini

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/go-rotini/rotini"
	"github.com/go-rotini/rotini/internal/codegen"
)

// runImport runs an import with opts, completed by the flags every import command takes,
// and reports it: the notes and summary on stderr, then the files on stdout.
func runImport(ctx context.Context, rtx *rotini.Context, opts codegen.ImportOptions, set RotiniImportFlagSet) {
	opts.Dir = set.Dir
	opts.Format = set.Format
	opts.Tags = set.Tags
	opts.Timeout = set.Timeout
	opts.ImporterVersion = set.ImporterVersion
	opts.Force = set.Force

	processor := codegen.NewProcessor(rtx.Version())
	dep, run := importDep, processor.Import
	if set.DryRun {
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

	if _, err := fmt.Fprintf(rtx.Stdout, "spec: %s\nconf: %s\n", filepath.ToSlash(imported.Spec), filepath.ToSlash(imported.Conf)); err != nil {
		haltWithWriteError(rtx, err)
		return
	}
	if set.DryRun {
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
