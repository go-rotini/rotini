package rotini

import (
	"context"
	"fmt"

	"github.com/go-rotini/rotini/internal/rotini"
)

var _ rotini.Handlers = (*rotiniHandlers)(nil)

type rotiniHandlers struct {
	rotini.DefaultCascadingPreRun
	rotini.DefaultPreRun
	rotini.DefaultPostRun
	rotini.DefaultCascadingPostRun
}

func (*rotiniHandlers) CascadingPreRun(ctx context.Context, rtx *rotini.Context) {
	fmt.Fprintln(rtx.Stdout, "rotiniHandlers CascadingPreRun")
}

func (*rotiniHandlers) PreRun(ctx context.Context, rtx *rotini.Context) {
	fmt.Fprintln(rtx.Stdout, "rotiniHandlers PreRun")
}

func (*rotiniHandlers) Run(ctx context.Context, rtx *rotini.Context) {
	fmt.Fprintln(rtx.Stdout, "rotiniHandlers Run")
}

func (*rotiniHandlers) PostRun(ctx context.Context, rtx *rotini.Context) {
	fmt.Fprintln(rtx.Stdout, "rotiniHandlers PostRun")
}

func (*rotiniHandlers) CascadingPostRun(ctx context.Context, rtx *rotini.Context) {
	fmt.Fprintln(rtx.Stdout, "rotiniHandlers CascadingPostRun")
}
