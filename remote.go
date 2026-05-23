package rotini

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// remoteDispatch is a resolved remote/co-located sub-command invocation: the
// plugin binary def.Binary run with args (everything after the command name).
type remoteDispatch struct {
	def  RemoteDef
	args []string
}

// execRemote locates and runs the co-located plugin binary, passing stdio
// through, honoring any timeout, and returning the plugin's exit code.
func (p *program) execRemote(r *remoteDispatch) int {
	path, err := resolveRemoteBinary(r.def.Binary)
	if err != nil {
		fmt.Fprintf(p.stderr, "%s: %s\n", p.def.Name, err)
		return 1
	}

	ctx := p.ctx
	if r.def.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, r.def.Timeout)
		defer cancel()
	}

	cmd := exec.CommandContext(ctx, path, r.args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = p.stdout
	cmd.Stderr = p.stderr

	switch err := cmd.Run(); {
	case err == nil:
		return 0
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		fmt.Fprintf(p.stderr, "%s: %s: timed out after %s\n", p.def.Name, r.def.Name, r.def.Timeout)
		return 1
	default:
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return ee.ExitCode()
		}
		fmt.Fprintf(p.stderr, "%s: %s: %s\n", p.def.Name, r.def.Name, err)
		return 1
	}
}

// resolveRemoteBinary finds the plugin binary: first adjacent to the running
// executable (the git/kubectl convention), then anywhere on PATH.
func resolveRemoteBinary(name string) (string, error) {
	if exe, err := os.Executable(); err == nil {
		adjacent := filepath.Join(filepath.Dir(exe), name)
		if fi, statErr := os.Stat(adjacent); statErr == nil && !fi.IsDir() {
			return adjacent, nil
		}
	}
	if p, err := exec.LookPath(name); err == nil {
		return p, nil
	}
	return "", fmt.Errorf("%q not found (looked next to the binary and on PATH)", name)
}
