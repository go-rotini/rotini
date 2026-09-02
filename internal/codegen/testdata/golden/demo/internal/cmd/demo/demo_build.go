package demo

import (
	"context"
	"fmt"

	"github.com/go-rotini/rotini"
)

var _ rotini.Handlers = (*demoBuildHandlers)(nil)

type demoBuildHandlers struct {
	rotini.DefaultCascadingPreRun
	rotini.DefaultPreRun
	rotini.DefaultPostRun
	rotini.DefaultCascadingPostRun
}

func (*demoBuildHandlers) CascadingPreRun(ctx context.Context, rtx *rotini.Context) {
	fmt.Fprintln(rtx.Stdout, "demoBuildHandlers CascadingPreRun")
}

func (*demoBuildHandlers) PreRun(ctx context.Context, rtx *rotini.Context) {
	fmt.Fprintln(rtx.Stdout, "demoBuildHandlers PreRun")
}

func (*demoBuildHandlers) Run(ctx context.Context, rtx *rotini.Context) {
	fmt.Fprintln(rtx.Stdout, "demoBuildHandlers Run")
}

func (*demoBuildHandlers) PostRun(ctx context.Context, rtx *rotini.Context) {
	fmt.Fprintln(rtx.Stdout, "demoBuildHandlers PostRun")
}

func (*demoBuildHandlers) CascadingPostRun(ctx context.Context, rtx *rotini.Context) {
	fmt.Fprintln(rtx.Stdout, "demoBuildHandlers CascadingPostRun")
}
