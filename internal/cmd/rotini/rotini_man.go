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

var _ rotini.ErrorHandler = (*rotiniManHandler)(nil)

type rotiniManHandler struct {
	rotini.NoCascadingPreRunE
	rotini.NoPreRunE
	rotini.NoPostRunE
	rotini.NoCascadingPostRunE
}

func (*rotiniManHandler) Run(ctx context.Context, rtx *rotini.Context) error {
	inputs, err := rtx.Inputs[RotiniManInputs]()
	if err != nil {
		return inputError(err)
	}

	in := inputs.RotiniMan
	if in.Flags.Dir != "" {
		if len(in.Arguments.Command) > 0 {
			return rotini.UsageError(errors.New("--dir writes every page; name a command only to print one page"))
		}
		return writeManPages(rtx, in.Flags.Dir)
	}

	page, err := Man(in.Arguments.Command...)
	if err != nil {
		hits := suggestor.Suggest(strings.Join(in.Arguments.Command, " "), commandNames())
		return withSuggestion(rotini.UsageError(err), hits)
	}

	if _, err := fmt.Fprint(rtx.Stdout, page); err != nil {
		return writeError(err)
	}

	return nil
}

// writeManPages writes every page ManPages lists into dir as <name>.<section>, creating dir as
// needed, and records a success naming the count. It returns the error that fails the run.
func writeManPages(rtx *rotini.Context, dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	pages := ManPages()
	for _, p := range pages {
		path := filepath.Join(dir, p.Name+"."+ManSection)
		if err := os.WriteFile(path, []byte(p.Content), 0o644); err != nil {
			return fmt.Errorf("write %s: %w", path, err)
		}
	}
	rtx.RecordSuccess(fmt.Sprintf("wrote %d man pages to %s", len(pages), dir))
	return nil
}
