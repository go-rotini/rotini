package rotini

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

// osView is the environment, working directory and home directory one run reads: every
// process-global read rotini makes on a program's behalf goes through it. The nil view, and a
// view with no environment, read the process environment live; a view without a directory
// resolves relative paths against the process working directory. A view is never changed
// after it is built, so runs may share one.
type osView struct {
	env      map[string]string // nil: the process environment, read live
	list     []string          // the environment as KEY=value, duplicates removed
	dir      string            // absolute; "" is the process working directory
	foldCase bool              // names match case-insensitively (Windows)
}

// newOSView indexes env (exec.Cmd.Env's KEY=value form; later duplicates win). A nil env reads
// the process environment.
func newOSView(env []string, dir, goos string) *osView {
	v := &osView{dir: dir, foldCase: goos == "windows"}
	if env == nil {
		return v
	}
	v.env = make(map[string]string, len(env))
	order := make([]string, 0, len(env))
	names := make(map[string]string, len(env)) // key → the spelling last given
	for _, kv := range env {
		name, val, ok := strings.Cut(kv, "=")
		if !ok || name == "" {
			continue // Windows' "=C:=C:\" entries have no name
		}
		key := v.key(name)
		if _, seen := v.env[key]; !seen {
			order = append(order, key)
		}
		v.env[key] = val
		names[key] = name
	}
	v.list = make([]string, 0, len(order))
	for _, key := range order {
		v.list = append(v.list, names[key]+"="+v.env[key])
	}
	return v
}

// withEnviron returns a copy of v with env as its environment.
func (v *osView) withEnviron(env []string) *osView {
	dir, goos := "", runtime.GOOS
	if v != nil {
		dir = v.dir
	}
	return newOSView(env, dir, goos)
}

// withDir returns a copy of v resolving relative paths against dir.
func (v *osView) withDir(dir string) *osView {
	out := &osView{foldCase: runtime.GOOS == "windows"}
	if v != nil {
		*out = *v
	}
	out.dir = dir
	return out
}

// resolved returns v with a relative directory made absolute against the process working
// directory, the form a run keeps.
func (v *osView) resolved() *osView {
	if v == nil || v.dir == "" || filepath.IsAbs(v.dir) {
		return v
	}
	abs, err := filepath.Abs(v.dir)
	if err != nil {
		return v
	}
	return v.withDir(abs)
}

// injected reports whether v replaces anything the process would supply.
func (v *osView) injected() bool { return v != nil && (v.env != nil || v.dir != "") }

func (v *osView) key(name string) string {
	if v.foldCase {
		return strings.ToUpper(name)
	}
	return name
}

// lookup reads one variable.
func (v *osView) lookup(name string) (string, bool) {
	if v == nil || v.env == nil {
		return os.LookupEnv(name)
	}
	val, ok := v.env[v.key(name)]
	return val, ok
}

// getenv is lookup without the presence report.
func (v *osView) getenv(name string) string {
	val, _ := v.lookup(name)
	return val
}

// environ returns the whole environment as KEY=value, a copy.
func (v *osView) environ() []string {
	if v == nil || v.env == nil {
		return os.Environ()
	}
	return slices.Clone(v.list)
}

// base is the injected directory, or "" for the process working directory.
func (v *osView) base() string {
	if v == nil {
		return ""
	}
	return v.dir
}

// getwd is the run's working directory.
func (v *osView) getwd() (string, error) {
	if d := v.base(); d != "" {
		return d, nil
	}
	wd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("working directory: %w", err)
	}
	return wd, nil
}

// abs resolves a relative path against the run's directory. With no directory injected the
// path is returned as is: the process resolves it against its own working directory.
func (v *osView) abs(p string) string { return joinDir(v.base(), p) }

// joinDir joins a relative p onto dir; an empty dir or p, or an absolute p, is returned as is.
func joinDir(dir, p string) string {
	if dir == "" || p == "" || filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(dir, p)
}

// home is the user's home directory, by os.UserHomeDir's rules over the run's environment.
func (v *osView) home() (string, error) {
	if v == nil || v.env == nil {
		h, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("home directory: %w", err)
		}
		return h, nil
	}
	return homeFrom(runtime.GOOS, v.getenv)
}

// homeFrom applies os.UserHomeDir's rules for goos to the environment getenv reads.
func homeFrom(goos string, getenv func(string) string) (string, error) {
	name, shown := "HOME", "$HOME"
	switch goos {
	case "windows":
		name, shown = "USERPROFILE", "%userprofile%"
	case "plan9":
		name, shown = "home", "$home"
	}
	if h := getenv(name); h != "" {
		return h, nil
	}
	switch goos {
	case "android":
		return "/sdcard", nil
	case "ios":
		return "/", nil
	}
	return "", errors.New(shown + " is not defined")
}

// xdgConfigHome is the XDG base config directory on every platform: $XDG_CONFIG_HOME when
// set, else ~/.config.
func (v *osView) xdgConfigHome() (string, error) {
	if d := v.getenv("XDG_CONFIG_HOME"); d != "" {
		return d, nil
	}
	h, err := v.home()
	if err != nil {
		return "", err
	}
	return filepath.Join(h, ".config"), nil
}

// xdgConfigDir is xdgConfigHome/<app>.
func (v *osView) xdgConfigDir(app string) (string, error) {
	if app == "" || app == "." || app == ".." || strings.ContainsAny(app, `/\`) || strings.ContainsRune(app, 0) {
		return "", fmt.Errorf("invalid app name %q", app)
	}
	base, err := v.xdgConfigHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, app), nil
}

// expandPluginPath expands $VAR and ${VAR} references from the run's environment, then a
// leading ~ to its home directory, as a shell would; a relative result is resolved against
// the run's directory. When the home directory cannot be found the ~ is left in place.
func (v *osView) expandPluginPath(dir string) string {
	if dir == "" {
		return ""
	}
	dir = os.Expand(dir, v.getenv)
	if dir == "~" || strings.HasPrefix(dir, "~/") || strings.HasPrefix(dir, "~"+string(filepath.Separator)) {
		if home, err := v.home(); err == nil {
			dir = filepath.Join(home, dir[1:])
		}
	}
	return v.abs(dir)
}

// errUnsetPathVariable reports a ${VAR:?message} reference whose variable is unset or empty.
var errUnsetPathVariable = errors.New("required variable is not set")

// expandPath runs a configuration path through shell-style expansion over the run's
// environment: a leading ~ or ~user, then $VAR and ${VAR}, with the ${VAR-x}, ${VAR:-x},
// ${VAR:?message}, ${VAR+x} and ${VAR:+x} forms. Expansion is one pass; a stray $ or an
// unclosed ${ is kept as written.
func (v *osView) expandPath(p string) (string, error) {
	if p == "" {
		return "", nil
	}
	p, err := v.expandTilde(p)
	if err != nil {
		return "", err
	}
	return v.expandVars(p)
}

func (v *osView) expandTilde(p string) (string, error) {
	if p[0] != '~' {
		return p, nil
	}
	end := strings.IndexAny(p, `/\`)
	if end < 0 {
		end = len(p)
	}
	if end == 1 {
		home, err := v.home()
		if err != nil {
			return "", fmt.Errorf("expand ~: %w", err)
		}
		return home + p[1:], nil
	}
	name := p[1:end]
	u, err := user.Lookup(name)
	if err != nil {
		return "", fmt.Errorf("expand ~%s: %w", name, err)
	}
	return u.HomeDir + p[end:], nil
}

func (v *osView) expandVars(s string) (string, error) {
	if !strings.ContainsRune(s, '$') {
		return s, nil
	}
	var b strings.Builder
	for i := 0; i < len(s); {
		switch {
		case s[i] != '$':
			b.WriteByte(s[i])
			i++
		case i+1 < len(s) && s[i+1] == '{':
			end := strings.IndexByte(s[i:], '}')
			if end < 0 {
				b.WriteString(s[i:])
				i = len(s)
				continue
			}
			out, err := v.evalBraced(s[i+2 : i+end])
			if err != nil {
				return "", err
			}
			b.WriteString(out)
			i += end + 1
		case i+1 < len(s) && isVarFirstByte(s[i+1]):
			end := i + 1
			for end < len(s) && (isVarFirstByte(s[end]) || (s[end] >= '0' && s[end] <= '9')) {
				end++
			}
			b.WriteString(v.getenv(s[i+1 : end]))
			i = end
		default:
			b.WriteByte(s[i])
			i++
		}
	}
	return b.String(), nil
}

// evalBraced evaluates the inside of one ${...} reference.
func (v *osView) evalBraced(expr string) (string, error) {
	idx, op := -1, ""
	for _, o := range []string{":-", ":+", ":?", "-", "+"} {
		if i := strings.Index(expr, o); i >= 0 {
			idx, op = i, o
			break
		}
	}
	if idx < 0 {
		return v.getenv(expr), nil
	}
	name, rhs := expr[:idx], expr[idx+len(op):]
	val, set := v.lookup(name)
	switch op {
	case ":-":
		if !set || val == "" {
			return rhs, nil
		}
	case "-":
		if !set {
			return rhs, nil
		}
	case ":?":
		if !set || val == "" {
			return "", fmt.Errorf("%w: %s: %s", errUnsetPathVariable, name, rhs)
		}
	case ":+":
		if set && val != "" {
			return rhs, nil
		}
		return "", nil
	case "+":
		if set {
			return rhs, nil
		}
		return "", nil
	}
	return val, nil
}

// isVarFirstByte reports whether c can start a $VAR name.
func isVarFirstByte(c byte) bool {
	return c == '_' || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')
}

// executableExts is the run's executable extensions (see executableExts): PATHEXT on Windows.
func (v *osView) executableExts() []string {
	return executableExts(runtime.GOOS, v.getenv("PATHEXT"))
}

// lookPath finds an executable named name on the run's PATH. With nothing injected it is
// exec.LookPath over the process PATH. Otherwise it searches the injected PATH the same way:
// a name with a separator is checked as a path (against the run's directory when relative),
// relative PATH entries are skipped, and outside Windows the file must be executable.
func (v *osView) lookPath(name string) (string, bool) {
	if !v.injected() {
		p, err := exec.LookPath(name)
		return p, err == nil
	}
	exts := v.executableExts()
	if strings.ContainsAny(name, `/\`) {
		p := v.abs(name)
		return runnableAt(filepath.Dir(p), filepath.Base(p), exts)
	}
	for _, dir := range filepath.SplitList(v.getenv("PATH")) {
		if !filepath.IsAbs(dir) {
			continue
		}
		if p, ok := runnableAt(dir, name, exts); ok {
			return p, true
		}
	}
	return "", false
}

// runnableAt is executableAt that also requires an execute permission bit outside Windows,
// as exec.LookPath does.
func runnableAt(dir, name string, exts []string) (string, bool) {
	p, ok := executableAt(dir, name, exts)
	if !ok || runtime.GOOS == "windows" {
		return p, ok
	}
	if fi, err := os.Stat(p); err != nil || fi.Mode()&0o111 == 0 {
		return "", false
	}
	return p, true
}

// bindChainView gives each frame of a run's chain the run's view, which
// [Command.PluginBinary] and [Command.DiscoveredPlugins] read through. An injected environment
// re-expands each declared plugin path; a frame a custom resolver built without one keeps its
// own.
func bindChainView(chain []Command, view *osView) {
	if view == nil {
		return
	}
	for i := range chain {
		chain[i].view = view
		if view.injected() && chain[i].pluginPathRaw != "" {
			chain[i].PluginPath = view.expandPluginPath(chain[i].pluginPathRaw)
		}
	}
}

// pluginDispatchFor returns the plugin dispatch to run, its directory re-expanded from the
// dispatching command's declared plugin path when the run's environment is injected.
func pluginDispatchFor(chain []Command, pd *PluginDispatch, view *osView) *PluginDispatch {
	if !view.injected() || len(chain) == 0 || chain[len(chain)-1].pluginPathRaw == "" {
		return pd
	}
	out := *pd
	out.Dir = view.expandPluginPath(chain[len(chain)-1].pluginPathRaw)
	return &out
}
