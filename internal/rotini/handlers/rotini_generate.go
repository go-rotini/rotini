package handlers

import (
	"context"

	"github.com/go-rotini/rotini/internal/rotini/gen"
	"github.com/go-rotini/rotini/rtk"
)

type rotiniGenerateHandlers struct{}

var _ gen.RotiniGenerateHandlers = (*rotiniGenerateHandlers)(nil)

func (*rotiniGenerateHandlers) CascadingPreRun(ctx context.Context, rtx rtk.Context) {
}

func (*rotiniGenerateHandlers) PreRun(ctx context.Context, rtx rtk.Context) {
}

func (*rotiniGenerateHandlers) Run(ctx context.Context, rtx rtk.Context) {
}

func (*rotiniGenerateHandlers) PostRun(ctx context.Context, rtx rtk.Context) {
}

func (*rotiniGenerateHandlers) CascadingPostRun(ctx context.Context, rtx rtk.Context) {
}
