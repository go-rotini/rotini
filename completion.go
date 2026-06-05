package rotini

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
)

// completeCommand is the hidden sub-command the generated shell scripts call to
// ask the binary for completion candidates.
const completeCommand = "__complete"

// FlagValueCompleter is an optional interface a command's handler may implement to
// supply dynamic completion candidates for one of its flags' values. When the hidden
// __complete entry is completing a flag value, it resolves the handler of the command
// that *declares* that flag (the same handler reflection dispatch uses) and, if it
// implements this interface, calls CompleteFlagValue with the flag's logical Name and
// the word being typed. A nil return falls back to the flag's static enum; a non-nil
// (possibly empty) return is authoritative. rtx carries the resolved chain
// (rtx.Chain()), the completion words (rtx.Args()), and every service bound on the
// Program, so a completer can reach a bound API client, the filesystem, etc.
//
// It is entirely opt-in — rotini generates no stub for it and adds nothing if it is
// absent — and, like any user callback, a panic in it is the caller's
// bug, not recovered. It may be called on every keystroke, so it must be read-only and
// fast.
type FlagValueCompleter interface {
	CompleteFlagValue(rtx *Context, flag, partial string) []string
}

// complete returns the completion candidates for the word currently being typed
// (the last element of words; the rest are the preceding context). It completes
// flag values (enum), flag names (when the word starts with "-"), and otherwise
// sub-command / remote-command names — all filtered by the typed prefix.
func complete(def Definition, words []string, handlers any, rtx *Context) []string {
	if len(words) == 0 {
		words = []string{""}
	}
	partial := words[len(words)-1]
	context := words[:len(words)-1]

	// Resolve the command chain from the context words, leniently skipping
	// anything that isn't a known sub-command (flags, flag values, arguments).
	chain := []ResolvedCommand{rootFrame(def)}
	for _, w := range context {
		if isFlag(w) {
			continue
		}
		if child, ok := findChild(chain[len(chain)-1], w); ok {
			chain = append(chain, cmdFrame(child))
		}
	}
	cur := chain[len(chain)-1]

	// Completing the value of the preceding flag. The handler of the command that
	// declares the flag may supply dynamic candidates (FlagValueCompleter); a nil
	// return (or no completer) falls back to the flag's static enum.
	if len(context) > 0 {
		if prev := context[len(context)-1]; isFlag(prev) {
			name, _, _ := splitFlag(prev)
			if fd, owner, ok := findFlag(chain, name); ok && fd.Type != "bool" {
				if cands, dyn := dynamicFlagValues(handlers, rtx, chain, words, owner, fd.Name, partial); dyn {
					return filterPrefix(cands, partial)
				}
				if len(fd.Enum) > 0 {
					return filterPrefix(fd.Enum, partial)
				}
			}
		}
	}

	// Completing a flag name. Only declared flags are offered — rotini auto-adds no
	// flags, so `-h`/`--help` appear here exactly when the CLI declares them (as the
	// companion does on every command), not by framework injection.
	if strings.HasPrefix(partial, "-") {
		var ids []string
		for i := len(chain) - 1; i >= 0; i-- {
			for _, f := range chain[i].Flags {
				ids = append(ids, f.Identifiers...)
			}
		}
		return filterPrefix(ids, partial)
	}

	// Completing a sub-command or remote-command name: the declared children +
	// remotes, plus the runtime-discovered plugins (minus declared collisions).
	var names []string
	for _, c := range cur.Commands {
		names = append(names, c.Name)
		names = append(names, c.Aliases...)
	}
	for _, r := range cur.Remotes {
		names = append(names, r.Name)
		names = append(names, r.Aliases...)
	}
	names = append(names, DiscoveredPlugins(cur)...)
	return filterPrefix(names, partial)
}

// DiscoveredPlugins returns the names of the plugins discovered for cmd's
// remote-discovery config — the token each plugin is invoked by (e.g. "foo" for an
// executable "<prefix>foo" found next to the binary, in the discovery path, or on
// PATH) — with any name that collides with a declared sub-command, remote command, or
// alias removed (the declared one wins, exactly as dispatch resolves it). It returns
// nil when cmd has no discovery or discovery is hidden. Results are deduped and sorted.
//
// This is the data feed for surfacing runtime plugins in help. A program's help is
// generated at codegen time and cannot know which plugins exist at runtime, so a help
// handler that wants to list them reads the relevant command's discovery from
// [Context.Chain] and calls this, then formats the result however it likes — rotini
// renders nothing itself (Pillar 1). For example, in a `--help` handler:
//
//	chain := rtx.Chain()
//	for _, name := range rotini.DiscoveredPlugins(chain[len(chain)-1]) {
//		fmt.Fprintf(out, "  %s\n", name)
//	}
//
// It touches the filesystem (reading the candidate directories) on every call and is
// best-effort: an unreadable directory contributes nothing rather than erroring. To learn
// whether the author-configured discovery path itself failed, call [DiscoveryDiagnostics].
func DiscoveredPlugins(cmd ResolvedCommand) []string {
	plugins, _ := discoveredFor(cmd)
	return plugins
}

// DiscoveryDiagnostics returns the problems encountered while scanning cmd's
// author-configured discovery path (remote_discovery.path) for plugins — typically the
// path is missing or unreadable. It returns nil when cmd has no discovery, discovery is
// hidden, none is configured, or the configured path scanned cleanly. Incidental
// locations — next to the binary and the entries of $PATH — are deliberately NOT reported:
// a missing $PATH entry is normal, not a misconfiguration.
//
// It is the data feed for a health/`doctor` or completion handler that wants to tell the
// author their discovery path is wrong. rotini surfaces the problem as data and prints no
// warning itself — auto-printing here would both corrupt completion output and impose
// behavior (Pillar 1). The handler decides whether and how to report it. Each error
// carries the offending path and cause, so a caller can classify with
// errors.Is(err, fs.ErrNotExist). Like [DiscoveredPlugins] it touches the filesystem on
// each call.
func DiscoveryDiagnostics(cmd ResolvedCommand) []error {
	_, problems := discoveredFor(cmd)
	return problems
}

// discoveredFor returns cmd's discovered plugin tokens (collision-filtered against its
// declared sub-commands and remotes) plus any problems scanning the author-configured
// discovery path. It is the shared core of [DiscoveredPlugins] and [DiscoveryDiagnostics].
func discoveredFor(cmd ResolvedCommand) ([]string, []error) {
	d := cmd.Discovery
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
	for _, r := range cmd.Remotes {
		declared[r.Name] = true
		for _, a := range r.Aliases {
			declared[a] = true
		}
	}
	all, problems := discoverPlugins(d)
	var out []string
	for _, plugin := range all {
		if !declared[plugin] {
			out = append(out, plugin)
		}
	}
	return out, problems
}

// dynamicFlagValues asks the handler of the command that declares the flag (owner) for
// completion candidates, when it implements FlagValueCompleter. It returns (candidates,
// true) only when a completer ran and returned a non-nil slice — the authoritative
// result; otherwise (nil, false), so the caller falls back to the static enum. handlers
// is the aggregate handler set (nil in pure-structural callers/tests); the owner's
// handler is resolved by the same reflection dispatch uses. rtx is seeded with the
// resolved chain and completion words so the completer can inspect them and reach bound
// services.
func dynamicFlagValues(handlers any, rtx *Context, chain []ResolvedCommand, words []string, owner, flag, partial string) ([]string, bool) {
	if handlers == nil {
		return nil, false
	}
	var handlerName string
	for _, fr := range chain {
		if fr.Name == owner {
			handlerName = fr.Handler
			break
		}
	}
	if handlerName == "" {
		return nil, false
	}
	m := reflect.ValueOf(handlers).MethodByName(handlerName)
	if !m.IsValid() || m.Type().NumIn() != 0 || m.Type().NumOut() != 1 {
		return nil, false
	}
	completer, ok := m.Call(nil)[0].Interface().(FlagValueCompleter)
	if !ok {
		return nil, false
	}
	if rtx != nil {
		rtx.chain = chain
		rtx.args = words
	}
	cands := completer.CompleteFlagValue(rtx, flag, partial)
	if cands == nil {
		return nil, false
	}
	return cands, true
}

// discoverPlugins lists the names (the part after the prefix) of `<prefix>*`
// executables found next to the host binary, in d.Path, and on PATH — the candidates
// plugin discovery exposes — deduped and sorted. It also returns any errors scanning the
// author-configured d.Path (a missing or unreadable path); failures scanning the
// incidental locations (the binary's own dir, $PATH entries) are ignored as normal.
func discoverPlugins(d *RemoteDiscoveryDef) ([]string, []error) {
	if d.Prefix == "" {
		return nil, nil
	}
	seen := map[string]bool{}
	var out []string
	var problems []error
	scan := func(dir string, report bool) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			if report {
				problems = append(problems, err)
			}
			return
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			name := e.Name()
			if name == d.Prefix || !strings.HasPrefix(name, d.Prefix) {
				continue
			}
			plugin := strings.TrimPrefix(name, d.Prefix)
			if seen[plugin] {
				continue
			}
			seen[plugin] = true
			out = append(out, plugin)
		}
	}
	if exe, err := os.Executable(); err == nil {
		scan(filepath.Dir(exe), false)
	}
	if d.Path != "" {
		scan(d.Path, true) // the author-configured path: a scan failure is a real diagnostic
	}
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if dir != "" {
			scan(dir, false)
		}
	}
	sort.Strings(out)
	return out, problems
}

func filterPrefix(candidates []string, prefix string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(candidates))
	for _, c := range candidates {
		if c != "" && strings.HasPrefix(c, prefix) && !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	sort.Strings(out)
	return out
}
