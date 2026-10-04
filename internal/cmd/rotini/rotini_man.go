package rotini

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-rotini/rotini"
)

var _ rotini.Handler = (*rotiniManHandler)(nil)

type rotiniManHandler struct {
	rotini.NoCascadingPreRun
	rotini.NoPreRun
	rotini.NoPostRun
	rotini.NoCascadingPostRun
}

func (*rotiniManHandler) Run(ctx context.Context, rtx *rotini.Context) {
	if answerHelp(rtx, func(in RotiniManInputs) bool { return in.RotiniMan.Flags.Help }) {
		return
	}

	inputs, err := rtx.Inputs[RotiniManInputs]()
	if err != nil {
		haltWithInputError(rtx, err)
		return
	}

	in := inputs.RotiniMan
	if in.Flags.Dir != "" {
		if len(in.Arguments.Command) > 0 {
			rtx.HaltWith(rotini.UsageError(errors.New("--dir writes every page; name a command only to print one page")))
			return
		}
		writeManPages(rtx, in.Flags.Dir)
		return
	}

	page, err := Man(in.Arguments.Command...)
	if err != nil {
		hits := suggestor.Suggest(strings.Join(in.Arguments.Command, " "), commandNames())
		rtx.HaltWith(withSuggestion(rotini.UsageError(err), hits))
		return
	}

	fmt.Fprint(rtx.Stdout, page)
}

// writeManPages writes every page ManPages lists into dir as <name>.<section>, creating dir
// when it does not exist, and says what it wrote.
func writeManPages(rtx *rotini.Context, dir string) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		rtx.HaltWith(fmt.Errorf("create %s: %w", dir, err))
		return
	}
	pages := ManPages()
	for _, p := range pages {
		path := filepath.Join(dir, p.Name+"."+ManSection)
		if err := os.WriteFile(path, []byte(p.Content), 0o644); err != nil {
			rtx.HaltWith(fmt.Errorf("write %s: %w", path, err))
			return
		}
	}
	rtx.RecordSuccess(fmt.Sprintf("wrote %d man pages to %s", len(pages), dir))
}
