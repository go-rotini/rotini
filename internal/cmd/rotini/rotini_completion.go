package rotini

import (
	"context"
	"fmt"

	"github.com/go-rotini/rotini"
)

var _ rotini.Handler = (*rotiniCompletionHandler)(nil)

type rotiniCompletionHandler struct {
	rotini.NoCascadingPreRun
	rotini.NoPreRun
	rotini.NoPostRun
	rotini.NoCascadingPostRun
}

func (*rotiniCompletionHandler) Run(ctx context.Context, rtx *rotini.Context) {
	inputs, err := rtx.Inputs[RotiniCompletionInputs]()
	if err != nil {
		haltWithInputError(rtx, err)
		return
	}

	script, err := Completion(inputs.RotiniCompletion.Arguments.Shell)
	if err != nil {
		rtx.HaltWith(rotini.UsageError(err))
		return
	}

	if _, err := fmt.Fprint(rtx.Stdout, script); err != nil {
		haltWithWriteError(rtx, err)
	}
}
