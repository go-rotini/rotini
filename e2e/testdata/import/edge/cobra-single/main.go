// Command single is a Cobra program with no sub-commands.
package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

// NewRootCmd builds the program's only command.
func NewRootCmd() *cobra.Command {
	c := &cobra.Command{
		Use:     "single FILE",
		Short:   "Count the lines of a file",
		Version: "1.0.0",
		Args:    cobra.ExactArgs(1),
		RunE:    func(_ *cobra.Command, args []string) error { fmt.Println(args); return nil },
	}
	c.Flags().BoolP("words", "w", false, "count words instead")
	c.Flags().StringSlice("skip", nil, "line prefixes to skip")
	return c
}

func main() { _ = NewRootCmd().Execute() }
