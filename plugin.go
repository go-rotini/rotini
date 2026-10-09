package rotini

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// Plugin dispatch resolves a co-located plugin binary (declared in the spec as plugins or
// plugin_discovery) and execs it with the program's streams. Canceling the run's context kills
// the plugin.

// PluginErrorKind classifies a plugin-dispatch failure: the plugin binary could
// not be located, it exceeded its declared timeout, or it could not be spawned.
type PluginErrorKind int

const (
	// PluginNotFound: no binary was found next to the executable, in the
	// plugin path, or on PATH.
	PluginNotFound PluginErrorKind = iota
	// PluginTimeout: the plugin ran past its declared timeout and was killed.
	PluginTimeout
	// PluginStartFailed: the binary was found but could not be started (a fork,
	// exec or pipe failure). The plugin's own non-zero exit is not an error kind.
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

// PluginError reports a failure by rotini to carry out a plugin dispatch. The plugin's own
// non-zero exit is not a PluginError; its exit code passes through unchanged.
//
//	var re *rotini.PluginError
//	if errors.As(err, &re) && re.Kind == rotini.PluginTimeout {
//	    fmt.Fprintf(os.Stderr, "%s timed out after %s\n", re.Name, re.Timeout)
//	}
//
// A missing binary is [CategoryUsage] when discovered (a mistyped token) and [CategoryInternal]
// when declared (an install problem); a spawn failure is [CategoryInternal]; a timeout is
// [CategoryNone].
type PluginError struct {
	Name    string          // the declared plugin name (or discovery token)
	Binary  string          // the plugin binary that was sought or spawned
	Kind    PluginErrorKind // what went wrong
	Timeout time.Duration   // the elapsed deadline, for Kind == PluginTimeout (else 0)
	Cause   error           // the underlying OS/exec error, reachable via errors.As (may be nil)
	Msg     string          // the human-readable failure

	// Candidates are the names the dispatching command knows (its sub-commands, their aliases
	// and its declared plugins), set when a discovered token matched no binary, so a mistyped
	// sub-command can be ranked against them; nil otherwise. Name is the token.
	Candidates []string

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

// PluginDispatch is a resolved plugin invocation, declared or discovered: Def.Binary run with
// Args. Dir is the command's plugin path, searched after the host binary's own directory and
// before PATH; empty means none.
type PluginDispatch struct {
	Def  PluginDef
	Args []string
	Dir  string
	// Discovered marks a plugin-discovery dispatch rather than a declared plugin, which
	// decides the error category when the binary cannot be resolved.
	Discovered bool
}

// execPlugin locates and runs the co-located plugin binary, passing stdio through, honoring
// the run context and any timeout, and returning the plugin's exit code. Rotini's own dispatch
// failures are recorded as errors and routed through the reporter; the plugin's non-zero exit
// passes through unchanged. chain is the resolved path; its last command is the one that dispatched, whose names are a
// discovered token's candidates.
func (p *Program) execPlugin(ctx context.Context, rtx *Context, chain []Command, r *PluginDispatch) (int, error) {
	path, err := resolvePluginBinary(r.Def.Binary, r.Dir, rtx.osView())
	if err != nil {
		cat := CategoryInternal
		var candidates []string
		if r.Discovered {
			cat = CategoryUsage
			if len(chain) > 0 {
				candidates = childCommandNames(chain[len(chain)-1])
			}
		}
		return p.pluginFailure(ctx, rtx, &PluginError{
			Name: r.Def.Name, Binary: r.Def.Binary, Kind: PluginNotFound,
			Cause: err, Msg: err.Error(), Candidates: candidates, cat: cat,
		})
	}

	if r.Def.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, r.Def.Timeout)
		defer cancel()
	}

	cmd := exec.CommandContext(ctx, path, r.Args...)
	// All three streams come from the Program, so redirecting them redirects the plugin too.
	// By default p.stdin is os.Stdin, which exec.Cmd passes as a raw descriptor, so an
	// interactive plugin still gets the terminal.
	cmd.Stdin = p.stdin
	cmd.Stdout = p.stdout
	cmd.Stderr = p.stderr
	// The child gets the run's environment and directory when they were injected; otherwise it
	// inherits the process's.
	if view := rtx.osView(); view.injected() {
		cmd.Env, cmd.Dir = view.environ(), view.base()
	}

	switch err := cmd.Run(); {
	case err == nil:
		return 0, nil
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		// cat is left unset: a timeout is operational, so CategoryNone.
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

// pluginFailure records a plugin dispatch error and settles the run. A missing, timed-out or
// unspawnable plugin is environmental, so it is a recorded error, not a fault.
func (p *Program) pluginFailure(ctx context.Context, rtx *Context, re *PluginError) (int, error) {
	rtx.RecordError(re)
	return p.settle(ctx, rtx)
}

// PluginBinary reports the executable the named plugin of cmd would run, and whether it
// resolves. It searches where dispatch searches, in the same order: next to the host binary,
// then the command's plugin path, then PATH. Use it, not exec.LookPath, to check which
// plugins are installed.
//
// name may be a declared plugin's name or alias, or a discovered plugin's token. It returns
// "", false when cmd declares no such plugin and has no discovery, or when the binary is not
// found. It reads the filesystem on every call.
func (cmd Command) PluginBinary(name string) (string, bool) {
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
	path, err := resolvePluginBinary(binary, dir, cmd.view)
	if err != nil {
		return "", false
	}
	return path, true
}

// resolvePluginBinary finds the plugin binary: first next to the running executable, then in
// dir (the command's plugin path, when set), then on PATH. The error names only the locations
// actually searched.
func resolvePluginBinary(name, dir string, view *osView) (string, error) {
	exts := view.executableExts()
	searched := []string{}
	if exe, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exe)
		if p, ok := executableAt(exeDir, name, exts); ok {
			return p, nil
		}
		searched = append(searched, "next to the binary "+exeDir)
	}
	if dir != "" {
		if p, ok := executableAt(dir, name, exts); ok {
			return p, nil
		}
		searched = append(searched, "the plugin path "+dir)
	}
	if p, ok := view.lookPath(name); ok {
		return p, nil
	}
	searched = append(searched, "PATH")
	return "", fmt.Errorf("%q not found; searched %s", name, strings.Join(searched, ", then "))
}

// executableAt reports the path of the executable named name in dir, when one exists as a
// non-directory file. On Windows a program is a file with an executable extension, so
// "host-sync" is found as host-sync.exe (or any other PATHEXT extension), the way the shell
// finds it; elsewhere the file is taken as named.
func executableAt(dir, name string, exts []string) (string, bool) {
	for _, candidate := range executableFileNames(name, exts) {
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

// DiscoveredPlugin is one plugin discovery found: the token it is invoked by and the binary that
// token runs.
type DiscoveredPlugin struct {
	// Name is the token a user types — "foo" for an executable "<prefix>foo".
	Name string
	// Path is the executable dispatch would run for Name right now: the first "<prefix>foo"
	// in search order (next to the binary, then the plugin path, then PATH), so a copy
	// shadowed by an earlier one is not the one listed.
	Path string
}

// DiscoveredPlugins returns each plugin discovered for cmd — an executable "<prefix>foo" found
// next to the binary, in the plugin path, or on PATH — deduped and sorted by name, with any
// name colliding with a declared sub-command, declared plugin or alias removed. It returns nil
// when cmd has no discovery or discovery is hidden. Rotini renders nothing; a handler lists
// the result itself:
//
//	chain := rtx.CommandChain()
//	for _, p := range chain[len(chain)-1].DiscoveredPlugins() {
//		fmt.Fprintf(out, "  %s\t%s\n", p.Name, p.Path)
//	}
//
// It reads the filesystem on every call and is best-effort: an unreadable directory
// contributes nothing. [Command.PluginDiscoveryErrors] reports failures of the configured
// plugin path.
func (cmd Command) DiscoveredPlugins() []DiscoveredPlugin {
	plugins, _ := discoveredFor(cmd)
	return plugins
}

// PluginDiscoveryErrors returns the problems encountered scanning cmd's configured plugin
// path — typically that it is unreadable or not a directory — and nil when there is no
// discovery, no plugin path, discovery is hidden, or the path scanned cleanly. A path that
// does not exist is not a problem. The directory of the binary and the $PATH entries are not
// reported. Rotini prints no warning itself, since that would corrupt completion output. Each
// error carries the path and cause, so errors.Is(err, fs.ErrPermission) classifies it.
func (cmd Command) PluginDiscoveryErrors() []error {
	_, problems := discoveredFor(cmd)
	return problems
}

// discoveredFor is the shared core of [Command.DiscoveredPlugins] and
// [Command.PluginDiscoveryErrors]: the collision-filtered plugin tokens plus any problems
// scanning the configured path.
func discoveredFor(cmd Command) ([]DiscoveredPlugin, []error) {
	d := cmd.PluginDiscovery
	if d == nil || d.Hidden {
		return nil, nil
	}
	declared := map[string]bool{}
	for _, c := range cmd.Commands {
		declared[c.Name] = true
		for _, a := range c.Aliases {
			declared[a] = true
		}
	}
	for _, r := range cmd.Plugins {
		declared[r.Name] = true
		for _, a := range r.Aliases {
			declared[a] = true
		}
	}
	all, problems := discoverPlugins(d, cmd.PluginPath, cmd.view)
	var out []DiscoveredPlugin
	for _, plugin := range all {
		if !declared[plugin.Name] {
			out = append(out, plugin)
		}
	}
	return out, problems
}
