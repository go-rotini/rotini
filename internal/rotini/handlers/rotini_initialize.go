package handlers

import (
	"context"

	"github.com/go-rotini/rotini/internal/rotini/gen"
	"github.com/go-rotini/rotini/rtk"
)

type rotiniInitializeHandlers struct{}

var _ gen.RotiniInitializeHandlers = (*rotiniInitializeHandlers)(nil)

func (*rotiniInitializeHandlers) CascadingPreRun(ctx context.Context, rtx rtk.Context) {
}

func (*rotiniInitializeHandlers) PreRun(ctx context.Context, rtx rtk.Context) {
}

func (*rotiniInitializeHandlers) Run(ctx context.Context, rtx rtk.Context) {
}

func (*rotiniInitializeHandlers) PostRun(ctx context.Context, rtx rtk.Context) {
}

func (*rotiniInitializeHandlers) CascadingPostRun(ctx context.Context, rtx rtk.Context) {
}
