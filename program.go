package rotini

import (
	"context"
	"os"
)

type CommandParser func(args []string) (CommandHandlers, error)

type CommandExecutor func(handlers CommandHandlers) error

type program struct {
	ctx      context.Context
	args     []string
	handlers ProgramHandlers
}

func NewProgram(h ProgramHandlers) *program {
	return &program{
		ctx:      context.Background(),
		args:     os.Args[1:],
		handlers: h,
		// parser: rtk.DefaultCommandParser
		// executor: rtk.DefaultCommandExecutor
	}
}

func (p *program) WithArguments(args []string) *program {
	if args != nil {
		p.args = args
	}
	return p
}

func (p *program) Execute() {
	// parse p.args, find "last" command to get the command handlers that need to be run
	// run the command handlers
}
