package handlers

import (
	"context"

	"github.com/go-rotini/rotini"
)

type rotiniVersionHandlers struct{}

var _ rotini.CommandHandlers = (*rotiniVersionHandlers)(nil)

func (*rotiniVersionHandlers) CascadingPreRun(ctx context.Context, rtx rotini.Context) {
}

func (*rotiniVersionHandlers) PreRun(ctx context.Context, rtx rotini.Context) {
}

func (*rotiniVersionHandlers) Run(ctx context.Context, rtx rotini.Context) {
}

func (*rotiniVersionHandlers) PostRun(ctx context.Context, rtx rotini.Context) {
}

func (*rotiniVersionHandlers) CascadingPostRun(ctx context.Context, rtx rotini.Context) {
}
