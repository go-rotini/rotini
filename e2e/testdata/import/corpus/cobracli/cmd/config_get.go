package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var getCmd = &cobra.Command{
	Use:   "get <key> [default]",
	Short: "Print one setting",
	Example: `  acmecli config get port
  acmecli config get region us-east-1`,
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println(viper.Get(args[0]))
		return nil
	},
}

func init() {
	configCmd.AddCommand(getCmd)
}
