package rotini

import (
	"context"
	"fmt"

	"github.com/go-rotini/rotini"
	"github.com/go-rotini/rotini/internal/codegen"
)

var _ rotini.HandlerE = (*rotiniFmtHandler)(nil)

// formatDep is the codegen entry point `rotini fmt` calls. The handler registers the real one
// with SetDependencyIfAbsent, so a test that registered a double first keeps it.
var formatDep = rotini.NewDependency[codegen.FormatFn]("rotini.fmt")

type rotiniFmtHandler struct {
	rotini.NoCascadingPreRunE
	rotini.NoPreRunE
	rotini.NoPostRunE
	rotini.NoCascadingPostRunE
}

func (*rotiniFmtHandler) Run(ctx context.Context, rtx *rotini.Context) error {
	inputs, err := rtx.Inputs[RotiniFmtInputs]()
	if err != nil {
		return inputError(err)
	}
	flags := inputs.RotiniFmt.Flags
	files := inputs.RotiniFmt.Arguments.Files
	if len(files) == 0 {
		spec, conf, err := resolveInputs(rtx, "", "")
		if err != nil {
			return err
		}
		files = append(files, spec)
		if conf != "" {
			files = append(files, conf)
		}
	}

	rtx.SetDependencyIfAbsent(formatDep, codegen.FormatFiles)
	changed, err := rtx.MustDependency(formatDep)(files, flags.Kind, flags.Check)
	if flags.Check {
		for _, f := range changed {
			fmt.Fprintln(rtx.Stderr, "would reformat", f)
		}
	} else {
		for _, f := range changed {
			if _, werr := fmt.Fprintln(rtx.Stdout, "formatted", f); werr != nil {
				return writeError(werr)
			}
		}
	}
	if err != nil {
		return problems(rtx, err)
	}
	if flags.Check && len(changed) > 0 {
		noun := "files"
		if len(changed) == 1 {
			noun = "file"
		}
		fmt.Fprintf(rtx.Stderr, "check: %d %s not formatted\n", len(changed), noun)
		rtx.HaltWithCode(exitWouldChange)
		return nil
	}
	rtx.HaltWithCode(0)
	return nil
}
