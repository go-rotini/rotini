package demo

import (
	"context"
	"fmt"

	"github.com/go-rotini/rotini"
)

var _ rotini.ErrorHandler = (*demoVersionHandler)(nil)

type demoVersionHandler struct {
	rotini.NoCascadingPreRunE
	rotini.NoPreRunE
	rotini.NoPostRunE
	rotini.NoCascadingPostRunE
}

func (*demoVersionHandler) Run(ctx context.Context, rtx *rotini.Context) error {
	version := rtx.Version()
	if version == "" {
		version = "unknown"
	}

	_, err := fmt.Fprintln(rtx.Stdout, rtx.CommandChain()[0].Name, version)
	return err
}
