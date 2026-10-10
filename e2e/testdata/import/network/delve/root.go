// Package delveimport exposes Delve's command tree for the network corpus.
package delveimport

import (
	"github.com/go-delve/delve/cmd/dlv/cmds"
	"github.com/spf13/cobra"
)

// Root builds Delve's tree as the dlv binary does.
func Root() *cobra.Command { return cmds.New(false) }
