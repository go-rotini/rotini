package demo

import (
	"context"
	"fmt"

	"github.com/go-rotini/rotini"
)

var _ rotini.Handlers = (*demoHandlers)(nil)

// demoHandlers implements `demo`.
//
// The embedded types are no-op implementations of the hooks this command does not use, so
// only Run is written below. Implement one by declaring a method with the same name:
// CascadingPreRun and CascadingPostRun run for every command in the chain, PreRun, Run and
// PostRun only for this one.
type demoHandlers struct {
	rotini.DefaultCascadingPreRun
	rotini.DefaultPreRun
	rotini.DefaultPostRun
	rotini.DefaultCascadingPostRun
}

// Run performs `demo`.
//
// Collect reconciles every input channel the spec declares for this command into the
// generated DemoInputs, validated, in the documented precedence. A handler does not print
// its own errors: it records them, and the runtime reports them once, after teardown.
//
// This file was created once and is now yours — rotini never overwrites it. Delete anything
// below you do not want.
func (*demoHandlers) Run(ctx context.Context, rtx *rotini.Context) {

	inputs, err := rotini.Collect[DemoInputs](rtx)
	if err != nil {
		// HaltWith records the error and stops the run as one act, claiming no exit code —
		// the funnel decides what an input failure costs. It is the correct spelling in
		// every hook, so failing never has to be written differently depending on which
		// hook you are in. To record a problem and CONTINUE instead, use rtx.RecordError.
		rtx.HaltWith(err)
		return
	}

	// TODO: replace this with the command's work.
	fmt.Fprintf(rtx.Stdout, "%s: %+v\n", "demo", inputs)
}
