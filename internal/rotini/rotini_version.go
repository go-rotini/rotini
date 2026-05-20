package rotini

import (
	"context"

	"github.com/go-rotini/rotini/internal/rotinigen"
	"github.com/go-rotini/rotini/rtk"
)

type rotiniVersionHandlers struct{}

var _ rotinigen.RotiniVersionHandlers = (*rotiniVersionHandlers)(nil)

func (*rotiniVersionHandlers) CascadingPreRun(ctx context.Context, rtx rtk.Context) {
}

func (*rotiniVersionHandlers) PreRun(ctx context.Context, rtx rtk.Context) {
}

func (*rotiniVersionHandlers) Run(ctx context.Context, rtx rtk.Context) {
}

func (*rotiniVersionHandlers) PostRun(ctx context.Context, rtx rtk.Context) {
}

func (*rotiniVersionHandlers) CascadingPostRun(ctx context.Context, rtx rtk.Context) {
}
