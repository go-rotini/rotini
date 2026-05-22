package handlers

import (
	"context"

	"github.com/go-rotini/rotini"
)

type rotiniValidateHandlers struct{}

var _ rotini.CommandHandlers = (*rotiniValidateHandlers)(nil)

func (*rotiniValidateHandlers) CascadingPreRun(ctx context.Context, rtx rotini.Context) {
}

func (*rotiniValidateHandlers) PreRun(ctx context.Context, rtx rotini.Context) {
}

func (*rotiniValidateHandlers) Run(ctx context.Context, rtx rotini.Context) {
}

func (*rotiniValidateHandlers) PostRun(ctx context.Context, rtx rotini.Context) {
}

func (*rotiniValidateHandlers) CascadingPostRun(ctx context.Context, rtx rotini.Context) {
}
