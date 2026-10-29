// Command single is a urfave/cli program with no sub-commands.
package main

import (
	"context"
	"os"

	"github.com/urfave/cli/v3"
)

// Root builds the program's only command.
func Root() *cli.Command {
	return &cli.Command{
		Name:      "single",
		Usage:     "Count the lines of a file",
		Arguments: []cli.Argument{&cli.StringArgs{Name: "file", Min: 1, Max: 1}},
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "words", Aliases: []string{"w"}, Usage: "count words instead"},
			&cli.StringSliceFlag{Name: "skip", Usage: "line prefixes to skip", Sources: cli.EnvVars("SINGLE_SKIP")},
		},
		Action: func(context.Context, *cli.Command) error { return nil },
	}
}

func main() { _ = Root().Run(context.Background(), os.Args) }
