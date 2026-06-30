package demo

import (
	"context"
	"fmt"

	"example.com/demo/internal/demo/rotini"
)

var _ rotini.Handlers = (*demoHandlers)(nil)

type demoHandlers struct {
	rotini.DefaultCascadingPreRun
	rotini.DefaultPreRun
	rotini.DefaultPostRun
	rotini.DefaultCascadingPostRun
}

func (*demoHandlers) CascadingPreRun(ctx context.Context, rtx *rotini.Context) {
	fmt.Fprintln(rtx.Stdout, "demoHandlers CascadingPreRun")
}

func (*demoHandlers) PreRun(ctx context.Context, rtx *rotini.Context) {
	fmt.Fprintln(rtx.Stdout, "demoHandlers PreRun")
}

func (*demoHandlers) Run(ctx context.Context, rtx *rotini.Context) {
	fmt.Fprintln(rtx.Stdout, "demoHandlers Run")
}

func (*demoHandlers) PostRun(ctx context.Context, rtx *rotini.Context) {
	fmt.Fprintln(rtx.Stdout, "demoHandlers PostRun")
}

func (*demoHandlers) CascadingPostRun(ctx context.Context, rtx *rotini.Context) {
	fmt.Fprintln(rtx.Stdout, "demoHandlers CascadingPostRun")
}
