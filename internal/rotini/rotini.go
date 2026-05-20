package rotini

import (
	"context"

	"github.com/go-rotini/rotini/internal/rotinigen"
	"github.com/go-rotini/rotini/rtk"
)

type rotiniHandlers struct{}

var _ rotinigen.RotiniHandlers = (*rotiniHandlers)(nil)

func (*rotiniHandlers) CascadingPreRun(ctx context.Context, rtx rtk.Context) {
}

func (*rotiniHandlers) PreRun(ctx context.Context, rtx rtk.Context) {
}

func (*rotiniHandlers) Run(ctx context.Context, rtx rtk.Context) {
}

func (*rotiniHandlers) PostRun(ctx context.Context, rtx rtk.Context) {
}

func (*rotiniHandlers) CascadingPostRun(ctx context.Context, rtx rtk.Context) {
}
