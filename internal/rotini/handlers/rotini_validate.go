package handlers

import (
	"context"

	"github.com/go-rotini/rotini/internal/rotini/gen"
	"github.com/go-rotini/rotini/rtk"
)

type rotiniValidateHandlers struct{}

var _ gen.RotiniValidateHandlers = (*rotiniValidateHandlers)(nil)

func (*rotiniValidateHandlers) CascadingPreRun(ctx context.Context, rtx rtk.Context) {
}

func (*rotiniValidateHandlers) PreRun(ctx context.Context, rtx rtk.Context) {
}

func (*rotiniValidateHandlers) Run(ctx context.Context, rtx rtk.Context) {
}

func (*rotiniValidateHandlers) PostRun(ctx context.Context, rtx rtk.Context) {
}

func (*rotiniValidateHandlers) CascadingPostRun(ctx context.Context, rtx rtk.Context) {
}
