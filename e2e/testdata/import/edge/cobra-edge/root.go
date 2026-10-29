// Package edge is a Cobra CLI built the way real programs build theirs: across functions and
// files, with conditional commands and inherited flag rules.
package edge

import (
	goflag "flag"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var rootCmd = &cobra.Command{
	Use:   "edge",
	Short: "Edge cases",
	Args:  cobra.NoArgs,
	RunE:  func(*cobra.Command, []string) error { return nil },
}

// experimentalCmd is added only when EDGE_EXPERIMENTAL is set.
var experimentalCmd = &cobra.Command{Use: "experimental", Short: "Experimental features", RunE: func(*cobra.Command, []string) error { return nil }}

func init() {
	pf := rootCmd.PersistentFlags()
	pf.StringP("profile", "p", "", "profile to use")
	_ = rootCmd.MarkPersistentFlagRequired("profile")
	pf.Bool("debug", false, "debug output")
	_ = pf.MarkHidden("debug")
	pf.String("legacy", "", "legacy switch")
	_ = pf.MarkDeprecated("legacy", "it does nothing")
	pf.String("log_level", "info", "log level")
	pf.SetNormalizeFunc(func(_ *pflag.FlagSet, name string) pflag.NormalizedName {
		return pflag.NormalizedName(strings.ReplaceAll(name, "_", "-"))
	})
	gofs := goflag.NewFlagSet("klog", goflag.ContinueOnError)
	gofs.Int("v", 0, "log verbosity")
	pf.AddGoFlagSet(gofs)

	addServer(rootCmd)
	rootCmd.AddCommand(newCopy(), newGet())
	if os.Getenv("EDGE_EXPERIMENTAL") != "" {
		rootCmd.AddCommand(experimentalCmd)
	}
}
