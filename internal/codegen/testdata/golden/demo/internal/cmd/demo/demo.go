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
func (*demoHandlers) Run(ctx context.Context, rtx *rotini.Context) {
	inputs, err := rotini.Collect[DemoInputs](rtx)
	if err != nil {
		rtx.RecordError(err)
		return
	}

	// TODO: replace this with the command's work.
	fmt.Fprintf(rtx.Stdout, "%s: %+v\n", "demo", inputs)
}
