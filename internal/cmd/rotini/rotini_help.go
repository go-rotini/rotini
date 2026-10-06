package rotini

import (
	"context"
	"fmt"
	"strings"

	"github.com/go-rotini/rotini"
)

var _ rotini.Handler = (*rotiniHelpHandler)(nil)

type rotiniHelpHandler struct {
	rotini.NoCascadingPreRun
	rotini.NoPreRun
	rotini.NoPostRun
	rotini.NoCascadingPostRun
}

func (*rotiniHelpHandler) Run(ctx context.Context, rtx *rotini.Context) {
	inputs, err := rtx.Inputs[RotiniHelpInputs]()
	if err != nil {
		haltWithInputError(rtx, err)
		return
	}

	topic := inputs.RotiniHelp.Arguments.Command
	help, err := Help(topic...)
	if err != nil {
		hits := suggestor.Suggest(strings.Join(topic, " "), commandNames())
		rtx.HaltWith(withSuggestion(rotini.UsageError(err), hits))
		return
	}

	fmt.Fprintln(rtx.Stdout, help)
	rtx.HaltWithCode(0)
}
