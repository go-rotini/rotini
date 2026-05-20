package handlers

import (
	"context"

	"github.com/go-rotini/rotini/internal/rotinigen"
	"github.com/go-rotini/rotini/rtk"
)

type rotiniCompletionHandlers struct{}

var _ rotinigen.RotiniHandlers = (*rotiniHandlers)(nil)

func (*rotiniCompletionHandlers) CascadingPreRun(ctx context.Context, rtx rtk.Context) {
}

func (*rotiniCompletionHandlers) PreRun(ctx context.Context, rtx rtk.Context) {
}

func (*rotiniCompletionHandlers) Run(ctx context.Context, rtx rtk.Context) {
}

func (*rotiniCompletionHandlers) PostRun(ctx context.Context, rtx rtk.Context) {
}

func (*rotiniCompletionHandlers) CascadingPostRun(ctx context.Context, rtx rtk.Context) {
}
