package rotini

import (
	"context"
)

type CommandHandlers interface {
	CascadingPreRun(ctx context.Context, rtx Context)
	PreRun(ctx context.Context, rtx Context)
	Run(ctx context.Context, rtx Context)
	PostRun(ctx context.Context, rtx Context)
	CascadingPostRun(ctx context.Context, rtx Context)
}

type RotiniSpec struct {
	Name     string
	Commands []RotiniCommand
	Flags    []RotiniFlag
}

type RotiniCommand struct {
	Name    string
	Aliases []string
	Flags   []RotiniFlag
}

type RotiniFlag struct {
	Name string
}
