package rth

import (
	"context"

	"github.com/go-rotini/rotini"
)

type rotiniInitializeHandlers struct{}

var _ rotini.CommandHandlers = (*rotiniInitializeHandlers)(nil)

func (*rotiniInitializeHandlers) CascadingPreRun(ctx context.Context, rtx rotini.Context) {
}

func (*rotiniInitializeHandlers) PreRun(ctx context.Context, rtx rotini.Context) {
}

func (*rotiniInitializeHandlers) Run(ctx context.Context, rtx rotini.Context) {
}

func (*rotiniInitializeHandlers) PostRun(ctx context.Context, rtx rotini.Context) {
}

func (*rotiniInitializeHandlers) CascadingPostRun(ctx context.Context, rtx rotini.Context) {
}
