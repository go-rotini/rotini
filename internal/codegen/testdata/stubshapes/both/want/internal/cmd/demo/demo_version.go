package demo

import (
	"context"
	"fmt"

	"github.com/go-rotini/rotini"
)

var _ rotini.Handler = (*demoVersionHandler)(nil)

type demoVersionHandler struct {
	rotini.NoCascadingPreRun
	rotini.NoPreRun
	rotini.NoPostRun
	rotini.NoCascadingPostRun
}

func (*demoVersionHandler) Run(ctx context.Context, rtx *rotini.Context) {
	version := rtx.Version()
	if version == "" {
		version = "unknown"
	}

	if _, err := fmt.Fprintln(rtx.Stdout, rtx.CommandChain()[0].Name, version); err != nil {
		rtx.HaltWith(err)
	}
}
