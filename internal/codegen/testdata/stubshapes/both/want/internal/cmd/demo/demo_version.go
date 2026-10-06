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

	fmt.Fprintln(rtx.Stdout, version)
}
