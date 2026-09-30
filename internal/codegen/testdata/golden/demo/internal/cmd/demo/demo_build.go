package demo

import (
	"context"
	"fmt"

	"github.com/go-rotini/rotini"
)

var _ rotini.Handlers = (*demoBuildHandlers)(nil)

// demoBuildHandlers implements `demo build`.
//
// The embedded types are no-op implementations of the hooks this command does not use, so
// only Run is written below. Implement one by declaring a method with the same name:
// CascadingPreRun and CascadingPostRun run for every command in the chain, PreRun, Run and
// PostRun only for this one.
//
// One value of this type serves THIS command's hooks for one run, so a field is a fine home
// for state passing between them — a transaction opened in PreRun and committed in PostRun,
// say. State that has to reach a DIFFERENT command in the chain does not fit in a field: use
// the registry (rotini.Key + BindTo) for that. See rotini.Handlers for the full rule.
type demoBuildHandlers struct {
	rotini.DefaultCascadingPreRun
	rotini.DefaultPreRun
	rotini.DefaultPostRun
	rotini.DefaultCascadingPostRun
}

// Run performs `demo build`.
//
// Collect reconciles every input channel the spec declares for this command into the
// generated DemoBuildInputs, validated, in the documented precedence. A handler does not print
// its own errors: it records them, and the runtime reports them once, after teardown.
//
// This file was created once and is now yours — rotini never overwrites it. Delete anything
// below you do not want. If this command leaves the spec, `go generate` removes the file;
// delete the `var _ rotini.Handlers` line above, or list the file under `keep:`, to hold on
// to it.
func (*demoBuildHandlers) Run(ctx context.Context, rtx *rotini.Context) {

	inputs, err := rotini.Collect[DemoBuildInputs](rtx)
	if err != nil {
		// HaltWith records the error and stops the run as one act, claiming no exit code —
		// the funnel decides what an input failure costs. It is the correct spelling in
		// every hook, so failing never has to be written differently depending on which
		// hook you are in. To record a problem and CONTINUE instead, use rtx.RecordError.
		rtx.HaltWith(err)
		return
	}

	// TODO: replace this with the command's work.
	fmt.Fprintf(rtx.Stdout, "%s: %+v\n", "demo build", inputs)
}
