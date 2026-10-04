package rotini

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"
)

// Plugin dispatch: resolving a co-located `<program>-<name>` binary and exec'ing it with the
// program's streams — canceling the run's context kills the plugin — the git-style
// sub-command model, declared in the spec as plugins / plugin_discovery.

// PluginErrorKind classifies a plugin-dispatch failure: the plugin binary could
// not be located, it exceeded its declared timeout, or it could not be spawned.
type PluginErrorKind int

const (
	// PluginNotFound: no binary was found next to the executable, in the
	// plugin path, or on PATH.
	PluginNotFound PluginErrorKind = iota
	// PluginTimeout: the plugin ran past its declared timeout and was killed.
	PluginTimeout
	// PluginStartFailed: the binary was found but could not be started (a fork/
	// exec or pipe failure — NOT the plugin's own non-zero exit, which passes
	// through untouched).
	PluginStartFailed
)

// String renders the kind as a short, stable label.
func (k PluginErrorKind) String() string {
	switch k {
	case PluginNotFound:
		return "binary-not-found"
	case PluginTimeout:
		return "timeout"
	case PluginStartFailed:
		return "spawn-failed"
	default:
		return "unknown"
	}
}

// PluginError reports a rotini-authored failure carrying out a plugin dispatch — not the
// plugin's own non-zero exit, which passes through untouched. It is typed so a reporter can
// special-case a timeout or a missing plugin without matching the message:
//
//	var re *rotini.PluginError
//	if errors.As(err, &re) && re.Kind == rotini.PluginTimeout {
//	    fmt.Fprintf(os.Stderr, "%s timed out after %s\n", re.Name, re.Timeout)
//	}
//
// A missing binary is [CategoryUsage] when discovered (the user's typo) and [CategoryInternal]
// when declared (an install problem); a spawn failure is [CategoryInternal]; a timeout is
// deliberately [CategoryNone], operational and neither party's fault, but still As-able here.
type PluginError struct {
	Name    string          // the declared plugin name (or discovery token)
	Binary  string          // the plugin binary that was sought or spawned
	Kind    PluginErrorKind // what went wrong
	Timeout time.Duration   // the elapsed deadline, for Kind == PluginTimeout (else 0)
	Cause   error           // the underlying OS/exec error, reachable via errors.As (may be nil)
	Msg     string          // the human-readable failure

	cat Category // how CategoryOf classifies it (CategoryNone for a timeout)
}

// Error renders the plugin-dispatch failure as a single, user-facing line.
func (e *PluginError) Error() string { return e.Msg }

// Unwrap exposes the Cause (when present) and the category sentinel
// ([ErrUsage]/[ErrInternal]) so errors.Is/As reach both; a timeout adds no
// sentinel, so [CategoryOf] reports [CategoryNone].
func (e *PluginError) Unwrap() []error {
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

// PluginDispatch is a resolved declared plugin invocation: Def.Binary run with Args. Dir is
// the command's plugin path, searched after the host binary's own directory and before PATH,
// for declared and discovered plugins alike; empty means no plugin path. The default
// resolver produces one for declared and discovered plugins.
type PluginDispatch struct {
	Def  PluginDef
	Args []string
	Dir  string
	// Discovered marks a plugin-discovery dispatch rather than a declared plugin command,
	// which decides the error category when the binary cannot be resolved.
	Discovered bool
}

// execPlugin locates and runs the co-located plugin binary, passing stdio through, honoring
// the run context and any timeout, and returning the plugin's exit code. rotini-authored
// diagnostics are recorded as errors and routed through the reporter; the plugin's own non-zero
// exit passes through untouched.
func (p *Program) execPlugin(ctx context.Context, rtx *Context, r *PluginDispatch) (int, error) {
	path, err := resolvePluginBinary(r.Def.Binary, r.Dir)
	if err != nil {
		// A DISCOVERED token's missing binary is the user's typo (Usage); a
		// DECLARED plugin's is an install/wiring problem (Internal).
		cat := CategoryInternal
		if r.Discovered {
			cat = CategoryUsage
		}
		return p.pluginFailure(ctx, rtx, &PluginError{
			Name: r.Def.Name, Binary: r.Def.Binary, Kind: PluginNotFound,
			Cause: err, Msg: err.Error(), cat: cat,
		})
	}

	if r.Def.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, r.Def.Timeout)
		defer cancel()
	}

	cmd := exec.CommandContext(ctx, path, r.Args...)
	// All three streams come from the Program, not from the process, so a host that redirects
	// them — a test, a REPL feeding a plugin, an embedding program — redirects the plugin too.
	//
	// By default p.stdin IS os.Stdin, and exec.Cmd hands an *os.File to the child as a raw
	// descriptor, so an interactive plugin still gets the real terminal.
	cmd.Stdin = p.stdin
	cmd.Stdout = p.stdout
	cmd.Stderr = p.stderr

	switch err := cmd.Run(); {
	case err == nil:
		return 0, nil
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		// Deliberately CategoryNone (cat unset): a timeout is operational —
		// neither the user's command nor the author's wiring is "wrong" — but
		// still As-able as a *PluginError so a reporter can special-case it.
		return p.pluginFailure(ctx, rtx, &PluginError{
			Name: r.Def.Name, Binary: r.Def.Binary, Kind: PluginTimeout, Timeout: r.Def.Timeout,
			Msg: fmt.Sprintf("%s: timed out after %s", r.Def.Name, r.Def.Timeout),
		})
	default:
		if ee, ok := errors.AsType[*exec.ExitError](err); ok {
			return ee.ExitCode(), err
		}
		return p.pluginFailure(ctx, rtx, &PluginError{
			Name: r.Def.Name, Binary: r.Def.Binary, Kind: PluginStartFailed,
			Cause: err, Msg: fmt.Sprintf("%s: %v", r.Def.Name, err), cat: CategoryInternal,
		})
	}
}

// pluginFailure records a rotini-authored plugin dispatch error and reports it through the
// reporter. A missing, timed-out or unspawnable plugin is environmental — the author cannot
// control whether the consumer installed it — so it is a recorded error, not a fault.
func (p *Program) pluginFailure(ctx context.Context, rtx *Context, re *PluginError) (int, error) {
	rtx.RecordError(re)
	return p.settle(ctx, rtx)
}

// PluginBinary reports the executable the named declared plugin of cmd would run, and
// whether it resolves at all. It searches exactly where dispatch searches, in the same order,
// which is the entire reason it exists.
//
// A plugin host's first extra command is always a doctor — "what is installed, what is missing"
// — and without this it has to reimplement rotini's search order from the outside. That order
// is three steps, the same for both kinds of plugin: next to the host binary, then the
// command's plugin_path, then PATH. Reaching for exec.LookPath, which is the obvious thing,
// reports every plugin installed beside the host binary as missing — the git/kubectl convention
// and the first location rotini tries.
//
// name may be a declared plugin's name or one of its aliases, or a discovered plugin's token.
// It returns "", false when cmd declares no such plugin and has no discovery to fall back on.
//
// Like [Command.DiscoveredPlugins], this touches the filesystem on every call and answers about
// right now: a plugin installed after it returns false will still dispatch.
func (cmd Command) PluginBinary(name string) (string, bool) {
	// The plugin path applies to both kinds, so it is read once rather than per branch.
	dir := cmd.PluginPath
	var binary string
	switch rd, ok := findPlugin(cmd, name); {
	case ok:
		binary = rd.Binary
	case cmd.PluginDiscovery != nil:
		binary = cmd.PluginDiscovery.Prefix + name
	default:
		return "", false
	}
	path, err := resolvePluginBinary(binary, dir)
	if err != nil {
		return "", false
	}
	return path, true
}

// resolvePluginBinary finds the plugin binary: first adjacent to the running
// executable (the git/kubectl convention), then in dir (the command's plugin_path,
// when set), then anywhere on PATH.
//
// dir is the command's PluginPath, and it is the same for both kinds of plugin — a declared
// one and a discovered one are the same binaries in the same place, so either can be
// installed in the plugin path.
//
// The failure message names the locations actually searched, and only those: telling a user
// rotini looked in a directory it never consulted would send them hunting in the wrong place.
func resolvePluginBinary(name, dir string) (string, error) {
	searched := []string{}
	if exe, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exe)
		if p, ok := executableAt(exeDir, name); ok {
			return p, nil
		}
		searched = append(searched, "next to the binary "+exeDir)
	}
	if dir != "" {
		if p, ok := executableAt(dir, name); ok {
			return p, nil
		}
		searched = append(searched, "the plugin path "+dir)
	}
	if p, err := exec.LookPath(name); err == nil {
		return p, nil
	}
	searched = append(searched, "PATH")
	return "", fmt.Errorf("%q not found; searched %s", name, strings.Join(searched, ", then "))
}

// executableAt reports the path of the executable named name in dir, when one exists as a
// non-directory file. On Windows a program is a file with an executable extension, so
// "host-sync" is found as host-sync.exe (or any other PATHEXT extension), the way the shell
// finds it; elsewhere the file is taken as named.
func executableAt(dir, name string) (string, bool) {
	for _, candidate := range executableFileNames(name, executableExts(runtime.GOOS, os.Getenv("PATHEXT"))) {
		p := filepath.Join(dir, candidate)
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p, true
		}
	}
	return "", false
}

// executableExts lists the lower-cased extensions that make a file runnable by name on goos:
// on Windows the PATHEXT list (or its default when unset), elsewhere none, since any file is
// run as named.
func executableExts(goos, pathext string) []string {
	if goos != "windows" {
		return nil
	}
	if pathext == "" {
		pathext = ".com;.exe;.bat;.cmd"
	}
	var out []string
	for ext := range strings.SplitSeq(pathext, ";") {
		if ext = strings.ToLower(strings.TrimSpace(ext)); ext != "" {
			out = append(out, ext)
		}
	}
	return out
}

// executableFileNames lists the file names that would run as name, in lookup order. With no
// executable extensions (every OS but Windows) that is name itself. On Windows it is name
// with each extension added, plus name as given when it already carries one.
func executableFileNames(name string, exts []string) []string {
	if len(exts) == 0 {
		return []string{name}
	}
	var out []string
	if _, ok := trimExecutableExt(name, exts); ok {
		out = append(out, name)
	}
	for _, ext := range exts {
		out = append(out, name+ext)
	}
	return out
}

// trimExecutableExt strips an executable extension from file, reporting whether it had one.
// With no executable extensions (every OS but Windows) every file counts, unchanged.
func trimExecutableExt(file string, exts []string) (string, bool) {
	if len(exts) == 0 {
		return file, true
	}
	ext := strings.ToLower(filepath.Ext(file))
	if ext != "" && slices.Contains(exts, ext) {
		return file[:len(file)-len(ext)], true
	}
	return file, false
}
