package rotini

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/go-rotini/rotini"
)

// GoToolPlugin finds a plugin that the run's module declares as a Go tool, such as rotini-mcp
// beside `go tool rotini`: both live in Go's build cache, apart from each other and off PATH.
// `go tool -n` prints the tool's path, building it if needed.
func GoToolPlugin(binary, dir string) (string, bool) {
	cmd := exec.Command("go", "tool", "-n", binary)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(out)), true
}

// Report is the default reporter, followed by how to install a first-party plugin the run
// couldn't find.
func Report(ctx context.Context, rtx *rotini.Context, out rotini.Outcome) {
	rotini.DefaultReporter(ctx, rtx, out)
	for _, err := range out.Errors {
		if hint := pluginInstallHint(err); hint != "" {
			_, _ = fmt.Fprintln(rtx.Stderr, hint)
			return
		}
	}
}

// pluginInstallHint says how to install the plugin err couldn't find, when it is rotini-mcp;
// "" otherwise.
func pluginInstallHint(err error) string {
	var pe *rotini.PluginError
	if !errors.As(err, &pe) || pe.Kind != rotini.PluginNotFound || pe.Binary != "rotini-mcp" {
		return ""
	}
	return "Install it beside rotini: go install github.com/go-rotini/mcp/cmd/rotini-mcp@latest\n" +
		"or, for `go tool rotini`, add it to the module: go get -tool github.com/go-rotini/mcp/cmd/rotini-mcp"
}
