package rotini

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// RemoteDispatch is a resolved remote/co-located sub-command invocation: the
// plugin binary Def.Binary run with Args (everything after the command name).
// Dir is an extra directory to search first (from remote_discovery.path),
// empty for a declared remote command. The default resolver produces one for
// declared remote_commands and discovered plugins; a custom [Resolver] may
// return its own in [Resolution.Remote].
type RemoteDispatch struct {
	Def  RemoteDef
	Args []string
	Dir  string
}

// execRemote locates and runs the co-located plugin binary, passing stdio
// through, honoring the run context (so a signal/cancellation kills the subprocess)
// and any timeout, and returning the plugin's exit code.
func (p *Program) execRemote(ctx context.Context, r *RemoteDispatch) (int, error) {
	path, err := resolveRemoteBinary(r.Def.Binary, r.Dir)
	if err != nil {
		fmt.Fprintf(p.stderr, "%s: %s\n", p.def.Name, err)
		return 1, err
	}

	if r.Def.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, r.Def.Timeout)
		defer cancel()
	}

	cmd := exec.CommandContext(ctx, path, r.Args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = p.stdout
	cmd.Stderr = p.stderr

	switch err := cmd.Run(); {
	case err == nil:
		return 0, nil
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		fmt.Fprintf(p.stderr, "%s: %s: timed out after %s\n", p.def.Name, r.Def.Name, r.Def.Timeout)
		return 1, err
	default:
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return ee.ExitCode(), err
		}
		fmt.Fprintf(p.stderr, "%s: %s: %s\n", p.def.Name, r.Def.Name, err)
		return 1, err
	}
}

// resolveRemoteBinary finds the plugin binary: first adjacent to the running
// executable (the git/kubectl convention), then in dir (the remote_discovery.path,
// when set), then anywhere on PATH.
func resolveRemoteBinary(name, dir string) (string, error) {
	if exe, err := os.Executable(); err == nil {
		if p, ok := executableAt(filepath.Dir(exe), name); ok {
			return p, nil
		}
	}
	if dir != "" {
		if p, ok := executableAt(dir, name); ok {
			return p, nil
		}
	}
	if p, err := exec.LookPath(name); err == nil {
		return p, nil
	}
	return "", fmt.Errorf("%q not found (looked next to the binary, in the discovery path, and on PATH)", name)
}

// executableAt reports the path dir/name when it exists as a non-directory file.
func executableAt(dir, name string) (string, bool) {
	p := filepath.Join(dir, name)
	if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
		return p, true
	}
	return "", false
}
