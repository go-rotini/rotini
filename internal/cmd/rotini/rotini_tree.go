package rotini

import (
	"context"
	"fmt"

	"github.com/go-rotini/rotini"
	"github.com/go-rotini/rotini/internal/codegen"
)

var _ rotini.Handler = (*rotiniTreeHandler)(nil)

type rotiniTreeHandler struct {
	rotini.NoCascadingPreRun
	rotini.NoPreRun
	rotini.NoPostRun
	rotini.NoCascadingPostRun
}

func (*rotiniTreeHandler) Run(ctx context.Context, rtx *rotini.Context) {
	inputs, err := rtx.Inputs[RotiniTreeInputs]()
	if err != nil {
		haltWithInputError(rtx, err)
		return
	}

	rtx.SetDependencyIfAbsent(treeDep, codegen.NewProcessor(rtx.Version()).Tree)
	tree, err := rtx.MustGetDependency(treeDep)(inputs.RotiniTree.Arguments.SpecFilePath)
	if err != nil {
		haltWithProblems(rtx, err)
		return
	}
	if _, err := fmt.Fprint(rtx.Stdout, tree); err != nil {
		haltWithWriteError(rtx, err)
	}
}
