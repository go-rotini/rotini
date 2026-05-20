package handlers

import (
	"context"

	"github.com/go-rotini/rotini/internal/rotinigen"
	"github.com/go-rotini/rotini/rtk"
)

type rotiniValidateHandlers struct{}

var _ rotinigen.RotiniValidateHandlers = (*rotiniValidateHandlers)(nil)

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
