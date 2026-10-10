package main

import (
	"flag"

	"github.com/spf13/cobra"
)

var verbose = flag.Bool("verbose", false, "log more")

var rootCmd = &cobra.Command{Use: "flagparse", Run: func(*cobra.Command, []string) {}}

func init() {
	flag.Parse()
}

func main() {
	_ = rootCmd.Execute()
	_ = verbose
}
