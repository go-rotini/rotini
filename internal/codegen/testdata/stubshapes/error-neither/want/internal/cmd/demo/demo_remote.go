package demo

import (
	"context"
	"fmt"

	"github.com/go-rotini/rotini"
)

var _ rotini.HandlerE = (*demoRemoteHandler)(nil)

type demoRemoteHandler struct {
	rotini.NoCascadingPreRunE
	rotini.NoPreRunE
	rotini.NoPostRunE
	rotini.NoCascadingPostRunE
}

func (*demoRemoteHandler) Run(ctx context.Context, rtx *rotini.Context) error {
	if _, err := fmt.Fprintln(rtx.Stdout, rtx.Help()); err != nil {
		return err
	}

	rtx.HaltWithCode(1)
	return nil
}
