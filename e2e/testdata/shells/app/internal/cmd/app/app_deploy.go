package app

import (
	"context"
	"strings"

	"github.com/go-rotini/rotini"
)

type appDeployHandler struct {
	rotini.NoCascadingPreRun
	rotini.NoPreRun
	rotini.NoPostRun
	rotini.NoCascadingPostRun
}

func (*appDeployHandler) Run(ctx context.Context, rtx *rotini.Context) {}

// CompleteArgValue answers in an order of its own, with a description for each service. Past
// the first letter it also chooses what the shell falls back to: yaml files after "./",
// directories after "s" or "d", nothing after "b", and every file after "c".
func (*appDeployHandler) CompleteArgValue(rtx *rotini.Context, arg, partial string) []string {
	services := []string{"web\tthe web tier", "api\tthe public API", "db\tthe database"}
	switch {
	case strings.HasPrefix(partial, "./"):
		rtx.SetCompletionFallback(rotini.Completion{Kind: "file", Extensions: []string{"yaml", "yml"}})
	case strings.HasPrefix(partial, "s"), strings.HasPrefix(partial, "d"):
		rtx.SetCompletionFallback(rotini.Completion{Kind: "directory"})
	case strings.HasPrefix(partial, "b"):
		rtx.SetCompletionFallback(rotini.Completion{Kind: "none"})
	case strings.HasPrefix(partial, "c"):
		rtx.SetCompletionFallback(rotini.Completion{Kind: "file"})
	}
	return services
}
