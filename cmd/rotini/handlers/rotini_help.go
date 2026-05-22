package handlers

import (
	"context"

	"github.com/go-rotini/rotini"
)

type rotiniHelpHandlers struct{}

var _ rotini.CommandHandlers = (*rotiniHelpHandlers)(nil)

func (*rotiniHelpHandlers) CascadingPreRun(ctx context.Context, rtx rotini.Context) {
}

func (*rotiniHelpHandlers) PreRun(ctx context.Context, rtx rotini.Context) {
}

func (*rotiniHelpHandlers) Run(ctx context.Context, rtx rotini.Context) {
}

func (*rotiniHelpHandlers) PostRun(ctx context.Context, rtx rotini.Context) {
}

func (*rotiniHelpHandlers) CascadingPostRun(ctx context.Context, rtx rotini.Context) {
}
