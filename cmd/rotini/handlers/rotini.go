package handlers

import (
	"context"

	"github.com/go-rotini/rotini"
)

type rotiniHandlers struct{}

var _ rotini.CommandHandlers = (*rotiniHandlers)(nil)

func (*rotiniHandlers) CascadingPreRun(ctx context.Context, rtx rotini.Context) {
}

func (*rotiniHandlers) PreRun(ctx context.Context, rtx rotini.Context) {
}

func (*rotiniHandlers) Run(ctx context.Context, rtx rotini.Context) {
}

func (*rotiniHandlers) PostRun(ctx context.Context, rtx rotini.Context) {
}

func (*rotiniHandlers) CascadingPostRun(ctx context.Context, rtx rotini.Context) {
}
