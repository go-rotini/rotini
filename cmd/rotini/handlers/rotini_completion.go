package handlers

import (
	"context"

	"github.com/go-rotini/rotini"
)

type rotiniCompletionHandlers struct{}

var _ rotini.CommandHandlers = (*rotiniHandlers)(nil)

func (*rotiniCompletionHandlers) CascadingPreRun(ctx context.Context, rtx rotini.Context) {
}

func (*rotiniCompletionHandlers) PreRun(ctx context.Context, rtx rotini.Context) {
}

func (*rotiniCompletionHandlers) Run(ctx context.Context, rtx rotini.Context) {
}

func (*rotiniCompletionHandlers) PostRun(ctx context.Context, rtx rotini.Context) {
}

func (*rotiniCompletionHandlers) CascadingPostRun(ctx context.Context, rtx rotini.Context) {
}
