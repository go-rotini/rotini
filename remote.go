package rotini

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// Plugin dispatch: resolving a co-located `<program>-<name>` binary, optionally
// verifying it, and exec'ing it with stream and signal passthrough — the git-style
// sub-command model, declared in the spec as remote_commands / remote_discovery.

// RemoteErrorKind classifies a remote-dispatch failure: the plugin binary could
// not be located, it exceeded its declared timeout, or it could not be spawned.
type RemoteErrorKind int

const (
	// RemoteBinaryNotFound: no binary was found next to the executable, in the
	// discovery path, or on PATH.
	RemoteBinaryNotFound RemoteErrorKind = iota
	// RemoteTimeout: the plugin ran past its declared timeout and was killed.
	RemoteTimeout
	// RemoteSpawnFailed: the binary was found but could not be started (a fork/
	// exec or pipe failure — NOT the plugin's own non-zero exit, which passes
	// through untouched).
	RemoteSpawnFailed
)

// String renders the kind as a short, stable label.
func (k RemoteErrorKind) String() string {
	switch k {
	case RemoteBinaryNotFound:
		return "binary-not-found"
	case RemoteTimeout:
		return "timeout"
	case RemoteSpawnFailed:
		return "spawn-failed"
	default:
		return "unknown"
	}
}

// RemoteError reports a rotini-authored failure carrying out a remote/plugin
// dispatch (NOT the plugin's own non-zero exit, which rotini passes through —
// the plugin already spoke for itself). It is the remote channel's typed,
// errors.As-able error, so a funnel can special-case a timeout or a missing
// plugin without matching the message:
//
//	var re *rotini.RemoteError
//	if errors.As(err, &re) && re.Kind == rotini.RemoteTimeout {
//	    fmt.Fprintf(os.Stderr, "%s timed out after %s\n", re.Name, re.Timeout)
//	}
//
// Category follows D4: a missing binary is the user's typo when DISCOVERED
// ([CategoryUsage]) and an install/wiring problem when DECLARED
// ([CategoryInternal]); a spawn failure is [CategoryInternal]; a timeout is
// deliberately [CategoryNone] — operational, neither party's fault — but still
// As-able here so a funnel that wants to treat it specially can. The underlying
// OS/exec Cause stays reachable via errors.As (nil for a synthesized
// not-found).
type RemoteError struct {
	Name    string          // the remote command name (or discovery token)
	Binary  string          // the plugin binary that was sought or spawned
	Kind    RemoteErrorKind // what went wrong
	Timeout time.Duration   // the elapsed deadline, for Kind == RemoteTimeout (else 0)
	Cause   error           // the underlying OS/exec error, reachable via errors.As (may be nil)
	Msg     string          // the human-readable failure

	cat Category // how CategoryOf classifies it (CategoryNone for a timeout)
}

// Error renders the plugin-dispatch failure as a single, user-facing line.
func (e *RemoteError) Error() string { return e.Msg }

// Unwrap exposes the Cause (when present) and the category sentinel
// ([ErrUsage]/[ErrInternal]) so errors.Is/As reach both; a timeout adds no
// sentinel, so [CategoryOf] reports [CategoryNone].
func (e *RemoteError) Unwrap() []error {
	var out []error
	if e.Cause != nil {
		out = append(out, e.Cause)
	}
	switch e.cat {
	case CategoryUsage:
		out = append(out, ErrUsage)
	case CategoryInternal:
		out = append(out, ErrInternal)
	}
	return out
}

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
// diagnostics (binary not found, timeout, spawn failure) are recorded as errors
// and routed through the funnel (a plugin's environment is the
// end-user's, not a rotini fault — see [Program.remoteFailure]); the plugin's
// own non-zero exit passes through untouched (the plugin already spoke for itself).
func (p *Program) execRemote(ctx context.Context, rtx *Context, r *RemoteDispatch) (int, error) {
	path, err := resolveRemoteBinary(r.Def.Binary, r.Dir)
	if err != nil {
		// A DISCOVERED token's missing binary is the user's typo (Usage); a
		// DECLARED remote's is an install/wiring problem (Internal).
		cat := CategoryInternal
		if r.Discovered {
			cat = CategoryUsage
		}
		return p.remoteFailure(ctx, rtx, &RemoteError{
			Name: r.Def.Name, Binary: r.Def.Binary, Kind: RemoteBinaryNotFound,
			Cause: err, Msg: err.Error(), cat: cat,
		})
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
		// Deliberately CategoryNone (cat unset): a timeout is operational —
		// neither the user's command nor the author's wiring is "wrong" — but
		// still As-able as a *RemoteError so a funnel can special-case it.
		return p.remoteFailure(ctx, rtx, &RemoteError{
			Name: r.Def.Name, Binary: r.Def.Binary, Kind: RemoteTimeout, Timeout: r.Def.Timeout,
			Msg: fmt.Sprintf("%s: timed out after %s", r.Def.Name, r.Def.Timeout),
		})
	default:
		if ee, ok := errors.AsType[*exec.ExitError](err); ok {
			return ee.ExitCode(), err
		}
		return p.remoteFailure(ctx, rtx, &RemoteError{
			Name: r.Def.Name, Binary: r.Def.Binary, Kind: RemoteSpawnFailed,
			Cause: err, Msg: fmt.Sprintf("%s: %v", r.Def.Name, err), cat: CategoryInternal,
		})
	}
}

// remoteFailure records a rotini-authored remote dispatch error and reports it
// through the funnel. A missing / timed-out / unspawnable plugin is
// ENVIRONMENTAL — the engineer who built this binary cannot control whether the
// consumer installed the plugin being dispatched to — so it is the end-user's
// recorded error, not rotini's "should never happen" fault (a panic).
func (p *Program) remoteFailure(ctx context.Context, rtx *Context, re *RemoteError) (int, error) {
	rtx.RecordError(re)
	return p.settle(ctx, rtx)
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
