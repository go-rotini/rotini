package app

import (
	"context"

	"github.com/go-rotini/rotini"
)

type appDeployHandler struct {
	rotini.NoCascadingPreRun
	rotini.NoPreRun
	rotini.NoPostRun
	rotini.NoCascadingPostRun
}

func (*appDeployHandler) Run(ctx context.Context, rtx *rotini.Context) {}

// CompleteArgValue answers in an order of its own, with a description for each service.
func (*appDeployHandler) CompleteArgValue(rtx *rotini.Context, arg, partial string) []string {
	return []string{"web\tthe web tier", "api\tthe public API", "db\tthe database"}
}
