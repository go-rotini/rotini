package rth

import (
	"context"

	"github.com/go-rotini/rotini"
)

type mycliHandlers struct{}

var _ rotini.CommandHandlers = (*mycliHandlers)(nil)

func (*mycliHandlers) CascadingPreRun(ctx context.Context, rtx *rotini.Context) {
}

func (*mycliHandlers) PreRun(ctx context.Context, rtx *rotini.Context) {
}

func (*mycliHandlers) Run(ctx context.Context, rtx *rotini.Context) {
}

func (*mycliHandlers) PostRun(ctx context.Context, rtx *rotini.Context) {
}

func (*mycliHandlers) CascadingPostRun(ctx context.Context, rtx *rotini.Context) {
}
