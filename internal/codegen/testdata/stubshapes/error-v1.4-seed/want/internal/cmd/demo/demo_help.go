package demo

import (
	"context"
	"fmt"

	"github.com/go-rotini/rotini"
)

var _ rotini.ErrorHandler = (*demoHelpHandler)(nil)

type demoHelpHandler struct {
	rotini.NoCascadingPreRunE
	rotini.NoPreRunE
	rotini.NoPostRunE
	rotini.NoCascadingPostRunE
}

func (*demoHelpHandler) Run(ctx context.Context, rtx *rotini.Context) error {
	inputs, err := rtx.Inputs[DemoHelpInputs]()
	if err != nil {
		return err
	}

	if rtx.PluginHelp(inputs.DemoHelp.Arguments.Command...) {
		return nil
	}

	page, err := rtx.HelpFor(inputs.DemoHelp.Arguments.Command...)
	if err != nil {
		return err
	}

	_, err = fmt.Fprintln(rtx.Stdout, page)
	return err
}
