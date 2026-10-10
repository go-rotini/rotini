// Package kubectlimport exposes kubectl's command tree for the network corpus.
package kubectlimport

import (
	"github.com/spf13/cobra"
	"k8s.io/kubectl/pkg/cmd"
)

// Root builds kubectl's tree as the kubectl binary does.
func Root() *cobra.Command { return cmd.NewDefaultKubectlCommand() }
