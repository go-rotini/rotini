package rotini

import (
	"context"
	"fmt"

	"github.com/go-rotini/rotini"
)

var _ rotini.HandlerE = (*rotiniHelpHandler)(nil)

type rotiniHelpHandler struct {
	rotini.NoCascadingPreRunE
	rotini.NoPreRunE
	rotini.NoPostRunE
	rotini.NoCascadingPostRunE
}

func (*rotiniHelpHandler) Run(ctx context.Context, rtx *rotini.Context) error {
	inputs, err := rtx.Inputs[RotiniHelpInputs]()
	if err != nil {
		return inputError(err)
	}

	topic := inputs.RotiniHelp.Arguments.Command
	help, err := Help(topic...)
	if err != nil {
		return inputError(err)
	}

	if _, err := fmt.Fprintln(rtx.Stdout, help); err != nil {
		return writeError(err)
	}
	rtx.HaltWithCode(0)
	return nil
}
