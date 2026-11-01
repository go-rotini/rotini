package rotini

import (
	"context"
	"fmt"

	"github.com/go-rotini/rotini"
	"github.com/go-rotini/rotini/internal/codegen"
)

var _ rotini.HandlerE = (*rotiniTreeHandler)(nil)

type rotiniTreeHandler struct {
	rotini.NoCascadingPreRunE
	rotini.NoPreRunE
	rotini.NoPostRunE
	rotini.NoCascadingPostRunE
}

func (*rotiniTreeHandler) Run(ctx context.Context, rtx *rotini.Context) error {
	inputs, err := rtx.Inputs[RotiniTreeInputs]()
	if err != nil {
		return inputError(err)
	}

	rtx.SetDependencyIfAbsent(treeDep, codegen.NewProcessor(rtx.Version()).Tree)
	tree, err := rtx.MustDependency(treeDep)(inputs.RotiniTree.Arguments.SpecFilePath)
	if err != nil {
		return problems(rtx, err)
	}
	if _, err := fmt.Fprint(rtx.Stdout, tree); err != nil {
		return writeError(err)
	}

	return nil
}
