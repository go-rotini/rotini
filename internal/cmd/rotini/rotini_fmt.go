package rotini

import (
	"context"
	"fmt"

	"github.com/go-rotini/rotini"
	"github.com/go-rotini/rotini/internal/codegen"
)

var _ rotini.Handler = (*rotiniFmtHandler)(nil)

// formatDep is the codegen entry point `rotini fmt` calls. The handler registers the real one
// with SetDependencyIfAbsent, so a test that registered a double first keeps it.
var formatDep = rotini.NewDependency[codegen.FormatFn]("rotini.fmt")

type rotiniFmtHandler struct {
	rotini.NoCascadingPreRun
	rotini.NoPreRun
	rotini.NoPostRun
	rotini.NoCascadingPostRun
}

func (*rotiniFmtHandler) Run(ctx context.Context, rtx *rotini.Context) {
	inputs, err := rtx.Inputs[RotiniFmtInputs]()
	if err != nil {
		haltWithInputError(rtx, err)
		return
	}
	flags := inputs.RotiniFmt.Flags
	files := inputs.RotiniFmt.Arguments.Files
	if len(files) == 0 {
		spec, conf, ok := resolveInputs(rtx, "", "")
		if !ok {
			return
		}
		files = append(files, spec)
		if conf != "" {
			files = append(files, conf)
		}
	}

	rtx.SetDependencyIfAbsent(formatDep, codegen.FormatFiles)
	changed, err := rtx.MustGetDependency(formatDep)(files, flags.Kind, flags.Check)
	if flags.Check {
		for _, f := range changed {
			fmt.Fprintln(rtx.Stderr, "would reformat", f)
		}
	} else {
		for _, f := range changed {
			if _, werr := fmt.Fprintln(rtx.Stdout, "formatted", f); werr != nil {
				haltWithWriteError(rtx, werr)
				return
			}
		}
	}
	if err != nil {
		haltWithProblems(rtx, err)
		return
	}
	if flags.Check && len(changed) > 0 {
		noun := "files"
		if len(changed) == 1 {
			noun = "file"
		}
		fmt.Fprintf(rtx.Stderr, "check: %d %s not formatted\n", len(changed), noun)
		rtx.HaltWithCode(exitWouldChange)
		return
	}
	rtx.HaltWithCode(0)
}
