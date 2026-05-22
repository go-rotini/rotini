package rth

import (
	"context"

	"github.com/go-rotini/rotini"
)

type rotiniGenerateHandlers struct{}

var _ rotini.CommandHandlers = (*rotiniGenerateHandlers)(nil)

func (*rotiniGenerateHandlers) CascadingPreRun(ctx context.Context, rtx rotini.Context) {
}

func (*rotiniGenerateHandlers) PreRun(ctx context.Context, rtx rotini.Context) {
}

func (*rotiniGenerateHandlers) Run(ctx context.Context, rtx rotini.Context) {
}

func (*rotiniGenerateHandlers) PostRun(ctx context.Context, rtx rotini.Context) {
}

func (*rotiniGenerateHandlers) CascadingPostRun(ctx context.Context, rtx rotini.Context) {
}
