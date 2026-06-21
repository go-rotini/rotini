package rotini

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// rotiniVersionCommand is the hidden entry every rotini program answers (mirroring
// [completeCommand]): `<binary> __rotini` prints the rotini version the binary was built
// against, so a host can negotiate same-major compatibility before dispatching to it (the
// binary arm of D-W9.4). It is always answerable; the host PROBE is opt-in ([RemoteVerify.Version]).
const rotiniVersionCommand = "__rotini"

// rotiniVersionReport is the line `<binary> __rotini` prints: a self-identifying marker
// plus the rotini version ([rotiniLibraryVersion]; blank when undeterminable).
func rotiniVersionReport() string { return "rotini " + rotiniLibraryVersion() }

// parseRotiniVersionReport extracts the version from a [rotiniVersionReport] line,
// returning "" when the output is not a recognizable report (so the host skips the check).
func parseRotiniVersionReport(out string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(out), "\n")
	rest, ok := strings.CutPrefix(strings.TrimSpace(line), "rotini ")
	if !ok {
		return ""
	}
	return strings.TrimSpace(rest)
}

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
	// RemoteVerificationFailed: an opt-in pre-dispatch trust check rejected the
	// binary (a [RemoteVerify.SHA256] content-hash mismatch, or a [RemoteVerify.Signature]
	// keyless signature that is missing, unverifiable, or does not match the expected
	// identity) — the binary was found but is not trusted, so it is NOT run.
	RemoteVerificationFailed
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
	case RemoteVerificationFailed:
		return "verification-failed"
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
// and routed through the OnError funnel (a plugin's environment is the
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

	// Opt-in pre-dispatch trust (D-W9.3/D-W9.4): a content-hash pin and/or a same-major
	// version handshake, both BEFORE the binary runs. A failure aborts the dispatch and is
	// recorded through the OnError funnel (the binary is the consumer's environment).
	if r.Def.Verify != nil {
		if err := verifyRemoteBinary(ctx, path, r.Def); err != nil {
			rtx.RecordError(err)
			return p.settle(ctx, rtx)
		}
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
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return ee.ExitCode(), err
		}
		return p.remoteFailure(ctx, rtx, &RemoteError{
			Name: r.Def.Name, Binary: r.Def.Binary, Kind: RemoteSpawnFailed,
			Cause: err, Msg: fmt.Sprintf("%s: %v", r.Def.Name, err), cat: CategoryInternal,
		})
	}
}

// remoteFailure records a rotini-authored remote dispatch error and reports it
// through the OnError funnel. A missing / timed-out / unspawnable plugin is
// ENVIRONMENTAL — the engineer who built this binary cannot control whether the
// consumer installed the plugin being dispatched to — so it is the end-user's
// error (OnError), not rotini's "should never happen" fault (OnPanic).
func (p *Program) remoteFailure(ctx context.Context, rtx *Context, re *RemoteError) (int, error) {
	rtx.RecordError(re)
	return p.settle(ctx, rtx)
}

// remoteVersionProbeTimeout caps the `__rotini` handshake probe so a misbehaving binary
// can't hang dispatch; it is independent of the command's own run timeout.
const remoteVersionProbeTimeout = 5 * time.Second

// verifyRemoteBinary runs the opt-in pre-dispatch checks for a resolved plugin at path
// (def.Verify is non-nil): the content-hash pin first (cheap, local), then the keyless
// signature (local, sidecar bundle), then the same-major version handshake (spawns). It
// returns the typed failure to record (a [*RemoteError] for a hash/signature mismatch, a
// binary-arm [*CompositionVersionError] for a cross-major), or nil to proceed.
func verifyRemoteBinary(ctx context.Context, path string, def RemoteDef) error {
	v := def.Verify
	if v.SHA256 != "" {
		sum, err := fileSHA256(path)
		if err != nil {
			return &RemoteError{
				Name: def.Name, Binary: def.Binary, Kind: RemoteVerificationFailed, Cause: err,
				Msg: fmt.Sprintf("%s: cannot hash %s for verification: %v", def.Name, def.Binary, err), cat: CategoryInternal,
			}
		}
		if !sha256Matches(v.SHA256, sum) {
			return &RemoteError{
				Name: def.Name, Binary: def.Binary, Kind: RemoteVerificationFailed,
				Msg: fmt.Sprintf("%s: binary %s failed sha256 verification — pinned %s, got sha256:%s", def.Name, def.Binary, normalizeSHA256(v.SHA256), sum), cat: CategoryInternal,
			}
		}
	}
	if v.Signature != nil {
		if err := verifyRemoteSignature(path, def); err != nil {
			return err
		}
	}
	if v.Version {
		return verifyRemoteVersion(ctx, path, def)
	}
	return nil
}

// keylessBundleSuffix is the sidecar bundle convention for the keyless signature rung: a
// remote binary <path> is signed alongside a <path>.sigstore.json bundle (the format
// cosign / GitHub's actions/attest-build-provenance emit).
const keylessBundleSuffix = ".sigstore.json"

// keylessVerifier verifies a keyless (sigstore) signature bundle for a dispatched binary
// against an expected signer identity, fully offline. It returns nil when the bundle is a
// valid signature over binaryPath by an identity matching (issuer, subject), else an error
// describing the failure.
type keylessVerifier func(binaryPath, bundlePath, issuer, subject string) error

// verifyKeyless is the wired keyless verifier (D-W9.10): rotini VERIFIES, it never signs.
// It is nil until a sigstore-backed verifier is registered — kept out of core so rotini's
// dependency surface stays minimal (sigstore-go is a large graph). A declared signature
// check with no verifier wired fails closed (see verifyRemoteSignature). Tests set it
// directly; the external registration mechanism is wired with the verifier module (TBD).
var verifyKeyless keylessVerifier

// verifyRemoteSignature checks a keyless signature on the resolved binary at path against
// the expected identity (def.Verify.Signature is non-nil). It fails CLOSED: no wired
// verifier, a missing sidecar bundle, or a verification error all abort dispatch with a
// [*RemoteError] ([RemoteVerificationFailed]) — a declared trust check never silently passes.
func verifyRemoteSignature(path string, def RemoteDef) error {
	sig := def.Verify.Signature
	fail := func(msg string, cause error) error {
		return &RemoteError{Name: def.Name, Binary: def.Binary, Kind: RemoteVerificationFailed, Cause: cause, Msg: msg, cat: CategoryInternal}
	}
	if verifyKeyless == nil {
		return fail(fmt.Sprintf("%s: binary %s declares a keyless signature check but no sigstore verifier is wired — import a rotini keyless verifier to enable it", def.Name, def.Binary), nil)
	}
	bundle := path + keylessBundleSuffix
	if _, err := os.Stat(bundle); err != nil {
		return fail(fmt.Sprintf("%s: binary %s is missing its signature bundle %s — keyless verification cannot proceed", def.Name, def.Binary, filepath.Base(bundle)), err)
	}
	if err := verifyKeyless(path, bundle, sig.Issuer, sig.Subject); err != nil {
		return fail(fmt.Sprintf("%s: binary %s failed keyless signature verification (issuer %q, subject %q): %v", def.Name, def.Binary, sig.Issuer, sig.Subject, err), err)
	}
	return nil
}

// verifyRemoteVersion runs `<path> __rotini` and fails on a definite cross-major mismatch
// with the host's rotini version. It is best-effort: an undeterminable host version, a
// remote that doesn't answer the handshake, or an unparseable report all skip the check
// (proceed) rather than block — only a clearly-different major aborts dispatch.
func verifyRemoteVersion(ctx context.Context, path string, def RemoteDef) error {
	host := hostRotiniVersion()
	if host == "" {
		return nil
	}
	probeCtx, cancel := context.WithTimeout(ctx, remoteVersionProbeTimeout)
	defer cancel()
	out, err := exec.CommandContext(probeCtx, path, rotiniVersionCommand).Output()
	if err != nil {
		return nil // not a rotini binary, or one too old to answer __rotini
	}
	remote := parseRotiniVersionReport(string(out))
	if remote == "" || sameMajorVersion(host, remote) {
		return nil
	}
	return &CompositionVersionError{
		Arm: CompositionBinaryArm, Subject: def.Binary, Want: host, Got: remote,
		Msg: fmt.Sprintf("remote %q binary %s was built with rotini %s but this program is rotini %s — a different major may speak an incompatible dispatch protocol; rebuild the plugin against a compatible rotini", def.Name, def.Binary, remote, host),
	}
}

// fileSHA256 returns the hex-encoded SHA-256 of the file at path (no "sha256:" prefix).
func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open binary: %w", err)
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("read binary: %w", err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// normalizeSHA256 lowercases a declared hash and strips an optional "sha256:" prefix, so a
// pin written either way ("sha256:ABC…" or "abc…") compares equal.
func normalizeSHA256(declared string) string {
	return strings.ToLower(strings.TrimPrefix(strings.TrimSpace(declared), "sha256:"))
}

// sha256Matches reports whether a declared pin equals the computed hex digest.
func sha256Matches(declared, hexDigest string) bool {
	return normalizeSHA256(declared) == strings.ToLower(hexDigest)
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
