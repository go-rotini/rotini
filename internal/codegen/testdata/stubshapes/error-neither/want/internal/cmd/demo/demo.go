package demo

import (
	"context"
	"fmt"

	"github.com/go-rotini/rotini"
)

var _ rotini.HandlerE = (*demoHandler)(nil)

type demoHandler struct {
	rotini.NoCascadingPreRunE
	rotini.NoPreRunE
	rotini.NoPostRunE
	rotini.NoCascadingPostRunE
}

func (*demoHandler) Run(ctx context.Context, rtx *rotini.Context) error {
	if _, err := fmt.Fprintln(rtx.Stdout, rtx.Help()); err != nil {
		return err
	}

	return rotini.MissingCommandError(rtx)
}
