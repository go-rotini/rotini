package rotini

import (
	"context"
	"fmt"

	"github.com/go-rotini/rotini"
)

var _ rotini.ErrorHandler = (*rotiniCompletionHandler)(nil)

type rotiniCompletionHandler struct {
	rotini.NoCascadingPreRunE
	rotini.NoPreRunE
	rotini.NoPostRunE
	rotini.NoCascadingPostRunE
}

func (*rotiniCompletionHandler) Run(ctx context.Context, rtx *rotini.Context) error {
	inputs, err := rtx.Inputs[RotiniCompletionInputs]()
	if err != nil {
		return inputError(err)
	}

	script, err := Completion(inputs.RotiniCompletion.Arguments.Shell)
	if err != nil {
		return rotini.UsageError(err)
	}

	if _, err := fmt.Fprint(rtx.Stdout, script); err != nil {
		return writeError(err)
	}

	return nil
}
