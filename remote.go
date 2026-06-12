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
	// Discovered marks a plugin-discovery dispatch (an unmatched token mapped
	// to <prefix><token>) as opposed to a declared remote command. It decides
	// the error CATEGORY when the binary cannot be resolved: a discovered
	// token is the user's typo (CategoryUsage — pair it with a Suggestor in a
	// custom funnel), while a declared remote's missing binary is an
	// install/wiring problem (CategoryInternal).
	Discovered bool
}

// execRemote locates and runs the co-located plugin binary, passing stdio
// through, honoring the run context (so a signal/cancellation kills the subprocess)
// and any timeout, and returning the plugin's exit code. rotini-authored
// diagnostics (binary not found, timeout, spawn failure) are routed through
// the ErrorFn funnel — the single sink for every framework
// diagnostic — with the exit floored to 1; the plugin's own non-zero exit
// passes through untouched (the plugin already spoke for itself).
func (p *Program) execRemote(ctx context.Context, rtx *Context, r *RemoteDispatch) (int, error) {
	path, err := resolveRemoteBinary(r.Def.Binary, r.Dir)
	if err != nil {
		if r.Discovered {
			return p.wiringFailure(ctx, rtx, UsageError(err)) // the user's typo, not a wiring bug
		}
		return p.wiringFailure(ctx, rtx, InternalError(err))
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
		// Deliberately untagged (CategoryNone): a timeout is operational —
		// neither the user's command nor the author's wiring is "wrong".
		return p.wiringFailure(ctx, rtx, fmt.Errorf("%s: timed out after %s", r.Def.Name, r.Def.Timeout))
	default:
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return ee.ExitCode(), err
		}
		return p.wiringFailure(ctx, rtx, InternalError(fmt.Errorf("%s: %w", r.Def.Name, err)))
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
