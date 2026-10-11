package cmd

import (
	"github.com/spf13/cobra"
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Read and write settings",
}

func init() {
	rootCmd.AddCommand(configCmd)
}
