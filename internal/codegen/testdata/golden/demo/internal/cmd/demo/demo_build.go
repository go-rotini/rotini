package demo

import (
	"context"
	"fmt"

	"github.com/go-rotini/rotini"
)

var _ rotini.Handlers = (*demoBuildHandlers)(nil)

// demoBuildHandlers implements `demo build`.
//
// The four embedded types are no-op implementations of the hooks this command does
// not use, so only Run is written below. Implement any of them by declaring a method
// with the same name — CascadingPreRun and CascadingPostRun run for every command in
// the chain, PreRun/Run/PostRun only for this one.
type demoBuildHandlers struct {
	rotini.DefaultCascadingPreRun
	rotini.DefaultPreRun
	rotini.DefaultPostRun
	rotini.DefaultCascadingPostRun
}

// Run performs `demo build`.
//
// Collect reconciles every input channel the spec declares for this command — argv
// flags and arguments, environment variables, configuration files and defaults — into
// the generated DemoBuildInputs type, validated, in the documented precedence.
//
// A handler does not print its own errors: it RECORDS them, and the runtime reports
// them once, after teardown, with the right exit code.
func (*demoBuildHandlers) Run(ctx context.Context, rtx *rotini.Context) {
	inputs, err := rotini.Collect[DemoBuildInputs](rtx)
	if err != nil {
		rtx.RecordError(err)
		return
	}

	// TODO: replace this with the command's work.
	fmt.Fprintf(rtx.Stdout, "%s: %+v\n", "demo build", inputs)
}
