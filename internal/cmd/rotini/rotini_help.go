package rotini

import (
	"context"
	"fmt"
	"strings"

	"github.com/go-rotini/rotini"
)

var _ rotini.Handlers = (*rotiniHelpHandlers)(nil)

type rotiniHelpHandlers struct {
	rotini.DefaultCascadingPreRun
	rotini.DefaultPreRun
	rotini.DefaultPostRun
	rotini.DefaultCascadingPostRun
}

func (*rotiniHelpHandlers) Run(ctx context.Context, rtx *rotini.Context) {
	if answerHelp(rtx, func(in RotiniHelpInputs) bool { return in.RotiniHelp.Flags.Help }) {
		return
	}

	inputs, err := rotini.Collect[RotiniHelpInputs](rtx)
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
