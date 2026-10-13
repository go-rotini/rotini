package app

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/go-rotini/rotini"
)

type appQuoteHandler struct {
	rotini.NoCascadingPreRun
	rotini.NoPreRun
	rotini.NoPostRun
	rotini.NoCascadingPostRun
}

// Run prints the arguments the program received, each in brackets, so a driver that runs a
// completed line can see how the shell passed the inserted value.
func (*appQuoteHandler) Run(ctx context.Context, rtx *rotini.Context) {
	if _, err := fmt.Fprintf(rtx.Stdout, "argv: [%s]\n", strings.Join(os.Args[1:], "] [")); err != nil {
		rtx.HaltWith(err)
	}
}
