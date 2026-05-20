package handlers

import (
	"context"

	"github.com/go-rotini/rotini/internal/rotini/gen"
	"github.com/go-rotini/rotini/rtk"
)

type rotiniHelpHandlers struct{}

var _ gen.RotiniHelpHandlers = (*rotiniHelpHandlers)(nil)

func (*rotiniHelpHandlers) CascadingPreRun(ctx context.Context, rtx rtk.Context) {
}

func (*rotiniHelpHandlers) PreRun(ctx context.Context, rtx rtk.Context) {
}

func (*rotiniHelpHandlers) Run(ctx context.Context, rtx rtk.Context) {
}

func (*rotiniHelpHandlers) PostRun(ctx context.Context, rtx rtk.Context) {
}

func (*rotiniHelpHandlers) CascadingPostRun(ctx context.Context, rtx rtk.Context) {
}
