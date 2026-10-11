package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the server",
	Long:  `Start the HTTP server on the configured port.`,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("serve called on port", viper.GetInt("port"))
	},
}

func init() {
	rootCmd.AddCommand(serveCmd)
	serveCmd.Flags().IntP("port", "p", 8080, "port to listen on")
	_ = viper.BindPFlag("port", serveCmd.Flags().Lookup("port"))
}
