package rotini

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
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
// (rtx.Chain()), the completion words (rtx.Args), and every service bound on the
// Program, so a completer can reach a bound API client, the filesystem, etc.
//
// It is entirely opt-in — rotini generates no stub for it and adds nothing if it is
// absent — and, like any user callback, a panic in it is the caller's
// bug, not recovered. It may be called on every keystroke, so it must be read-only and
// fast.
//
// A returned candidate MAY carry a one-line description after a tab —
// "value\tdescription", the same wire shape command and flag candidates use:
// zsh/fish/powershell render it beside the value, bash strips it. Bare values
// stay bare; nothing breaks when descriptions are absent.
type FlagValueCompleter interface {
	CompleteFlagValue(rtx *Context, flag, partial string) []string
}

// ArgValueCompleter is the positional-argument counterpart of
// [FlagValueCompleter]: a command's handler may implement it to supply dynamic
// completion candidates for one of its arguments' values. When the hidden
// __complete entry is completing a positional, it resolves the handler of the
// command being invoked and, if it implements this interface, calls
// CompleteArgValue with the argument's logical Name and the word being typed. A
// nil return falls back to the argument's static enum; a non-nil (possibly
// empty) return is authoritative. The same opt-in, read-only, called-on-every-
// keystroke contract as FlagValueCompleter applies — including the optional
// "value\tdescription" candidate shape.
type ArgValueCompleter interface {
	CompleteArgValue(rtx *Context, arg, partial string) []string
}

// completionContext is the dispatch-faithful reading of the words preceding the
// one being completed: the resolved command chain, how many positional
// arguments the leaf has already consumed, and whether a "--" terminator has
// ended flag handling. It mirrors resolveChain (value-aware flag skipping,
// descent stops at the first positional) so completion predicts exactly what
// dispatch would do with the same words.
type completionContext struct {
	chain           []ResolvedCommand
	positionals     int
	afterTerminator bool
	remote          bool // a remote/plugin token was hit: the rest belongs to the dispatched binary
}

// complete returns the completion candidates for the word currently being typed
// (the last element of words; the rest are the preceding context). It completes
// flag values (dynamic completer, else enum), flag names (when the word starts
// with "-", including the inline "--flag=val" form), sub-command and
// remote-command names, and positional-argument values (dynamic completer, else
// enum) — all filtered by the typed prefix and excluding hidden inputs. An
// empty result lets the shell apply its own default (typically file names).
func complete(def Definition, words []string, handlers any, rtx *Context) []string {
	if len(words) == 0 {
		words = []string{""}
	}
	partial := words[len(words)-1]
	context := words[:len(words)-1]

	cc := walkContext(def, context)
	if cc.remote {
		return nil // the remote binary owns its own argument surface
	}
	cur := cc.chain[len(cc.chain)-1]
	if cur.Passthrough {
		return nil // raw tokens past the boundary: let the shell fall back to files
	}

	if cands, handled := completePendingFlagValue(cc, context, words, partial, handlers, rtx); handled {
		return cands
	}
	if cands, handled := completeFlagWord(cc, words, partial, handlers, rtx); handled {
		return cands
	}

	// Completing a positional word. Until the first positional is consumed it may
	// also be a sub-command / remote-command / discovered plugin; afterwards
	// dispatch no longer descends, so only argument values remain.
	var names []string
	if !cc.afterTerminator && cc.positionals == 0 {
		names = dispatchableNames(cur)
	}
	names = append(names, argValueCandidates(handlers, rtx, cc, words, partial)...)
	return filterPrefix(names, partial)
}

// completePendingFlagValue handles the separate-word form — "--flag <TAB>", and
// bash's "--flag = val" splitting — where the word being completed is the VALUE of
// the preceding flag.
//
// The handler of the command that declares the flag may supply dynamic candidates
// ([FlagValueCompleter]); a nil return (or no completer) falls back to the flag's
// static enum, and an empty result lets the shell fall back to file completion —
// never to sub-command names, which dispatch would read as this flag's value.
func completePendingFlagValue(cc completionContext, context, words []string, partial string, handlers any, rtx *Context) ([]string, bool) {
	if cc.afterTerminator {
		return nil, false
	}
	name, ok := pendingValueFlag(context)
	if !ok {
		return nil, false
	}
	fd, owner, found := findFlag(cc.chain, name)
	if !found || !takesValue(fd) {
		return nil, false
	}
	return filterPrefix(flagValueCandidates(handlers, rtx, cc.chain, words, owner, fd, partial), partial), true
}

// completeFlagWord handles a word that begins with "-": either a flag NAME, or a
// flag's value in the inline "--flag=value" form.
//
// Only declared, non-hidden flags are offered — rotini auto-adds no flags, so
// -h/--help appear here exactly when the CLI declares them, never by injection. The
// whole chain contributes, since ancestor flags resolve on descendants at runtime.
func completeFlagWord(cc completionContext, words []string, partial string, handlers any, rtx *Context) ([]string, bool) {
	if cc.afterTerminator || !strings.HasPrefix(partial, "-") {
		return nil, false
	}

	if name, val, hasInline := splitFlag(partial); hasInline {
		fd, owner, found := findFlag(cc.chain, name)
		if !found || !takesValue(fd) {
			return nil, true // a flag that takes no value has nothing to offer after "="
		}
		cands := flagValueCandidates(handlers, rtx, cc.chain, words, owner, fd, val)
		out := make([]string, 0, len(cands))
		for _, c := range cands {
			out = append(out, name+"="+c)
		}
		return filterPrefix(out, partial), true
	}

	var ids []string
	for _, v := range slices.Backward(cc.chain) {
		for _, f := range v.Flags {
			if f.Hidden {
				continue
			}
			for _, id := range f.Identifiers {
				ids = append(ids, withDescription(id, f.Summary))
			}
		}
	}
	return filterPrefix(ids, partial), true
}

// dispatchableNames lists everything the next positional word could dispatch to:
// sub-commands and their aliases, declared remote commands and theirs, and the
// plugins discovery finds on PATH.
func dispatchableNames(cur ResolvedCommand) []string {
	var names []string
	for _, c := range cur.Commands {
		if c.Hidden {
			continue
		}
		names = append(names, withDescription(c.Name, c.Summary))
		for _, a := range c.Aliases {
			names = append(names, withDescription(a, c.Summary))
		}
	}
	for _, r := range cur.Remotes {
		names = append(names, withDescription(r.Name, r.Summary))
		for _, a := range r.Aliases {
			names = append(names, withDescription(a, r.Summary))
		}
	}
	return append(names, DiscoveredPlugins(cur)...)
}

// walkContext resolves the words preceding the completed one with dispatch's
// semantics (see resolveChain), leniently: unknown tokens are positionals, not
// errors. bash splits "--flag=value" on "=" into three words; the literal "="
// token glues such a value back onto its flag.
func walkContext(def Definition, context []string) completionContext {
	cc := completionContext{chain: []ResolvedCommand{rootFrame(def)}}
	for i := 0; i < len(context); i++ {
		tok := context[i]
		if cc.afterTerminator {
			cc.positionals++
			continue
		}
		if tok == "--" {
			cc.afterTerminator = true
			continue
		}
		if tok == "=" {
			if i > 0 && isFlag(context[i-1]) {
				i++ // the glued value (when present) belongs to the preceding flag
			}
			continue
		}
		if isFlag(tok) {
			name, _, hasInline := splitFlag(tok)
			// Skip a separate value token so it is not mistaken for a command —
			// unless it is the "=" glue, which the next iteration handles.
			if fd, _, ok := findFlag(cc.chain, name); ok && takesValue(fd) && !hasInline {
				if i+1 < len(context) && context[i+1] != "=" {
					i++
				}
			}
			continue
		}
		cur := cc.chain[len(cc.chain)-1]
		if cc.positionals == 0 {
			if child, ok := findChild(cur, tok); ok {
				cc.chain = append(cc.chain, cmdFrame(child))
				continue
			}
			if _, ok := findRemote(cur, tok); ok || cur.Discovery != nil {
				cc.remote = true
				return cc
			}
		}
		cc.positionals++
	}
	return cc
}

// pendingValueFlag reports the flag whose value the next word supplies, when
// the context ends with a flag awaiting one: a bare flag token, or a flag
// followed by bash's "=" split token.
func pendingValueFlag(context []string) (string, bool) {
	if len(context) == 0 {
		return "", false
	}
	last := context[len(context)-1]
	if last == "=" && len(context) >= 2 && isFlag(context[len(context)-2]) {
		name, _, _ := splitFlag(context[len(context)-2])
		return name, true
	}
	if isFlag(last) {
		if name, _, hasInline := splitFlag(last); !hasInline {
			return name, true
		}
	}
	return "", false
}

// flagValueCandidates returns the candidates for one flag's value: the owning
// handler's dynamic completer when it answers, else a map flag's declared key
// vocabulary (completed up to the '='), else the flag's static enum, else
// nothing (so the shell falls back to its default completion).
func flagValueCandidates(handlers any, rtx *Context, chain []ResolvedCommand, words []string, owner string, fd FlagDef, partial string) []string {
	// An '@' on a from:file flag is a path in progress — offer nothing, so the
	// shell falls back to its own file completion.
	if strings.HasPrefix(partial, "@") && slices.Contains(fd.From, "file") {
		return nil
	}
	if cands, dyn := dynamicFlagValues(handlers, rtx, chain, words, owner, fd.Name, partial); dyn {
		return cands
	}
	if len(fd.KeyPaths) > 0 && isMapType(fd.Type) && !strings.Contains(partial, "=") {
		keys := make([]string, len(fd.KeyPaths))
		for i, k := range fd.KeyPaths {
			keys[i] = k + "=" // the value past the '=' is the user's to write
		}
		return keys
	}
	return fd.Enum
}

// argValueCandidates returns the candidates for the positional argument the
// completed word would bind to — the argument at the leaf's next positional
// index (the trailing variadic argument absorbs everything past the end). The
// leaf handler's dynamic completer (ArgValueCompleter) wins when it answers,
// else the argument's static enum; hidden arguments offer nothing.
func argValueCandidates(handlers any, rtx *Context, cc completionContext, words []string, partial string) []string {
	cur := cc.chain[len(cc.chain)-1]
	args := cur.Arguments
	idx := cc.positionals
	if idx >= len(args) {
		if len(args) == 0 || !args[len(args)-1].Variadic {
			return nil
		}
		idx = len(args) - 1
	}
	ad := args[idx]
	if ad.Hidden {
		return nil
	}
	if cands, dyn := dynamicArgValues(handlers, rtx, cc.chain, words, ad.Name, partial); dyn {
		return cands
	}
	return ad.Enum
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
	var handlerName string
	for _, fr := range chain {
		if fr.Name == owner {
			handlerName = fr.Handler
			break
		}
	}
	completer, ok := resolveHandler[FlagValueCompleter](handlers, handlerName)
	if !ok {
		return nil, false
	}
	seedCompletionContext(rtx, chain, words)
	cands := completer.CompleteFlagValue(rtx, flag, partial)
	if cands == nil {
		return nil, false
	}
	return cands, true
}

// dynamicArgValues is dynamicFlagValues' positional counterpart: it asks the
// handler of the command being invoked (the chain leaf — positionals always
// bind to the leaf) for candidates, when it implements ArgValueCompleter.
func dynamicArgValues(handlers any, rtx *Context, chain []ResolvedCommand, words []string, arg, partial string) ([]string, bool) {
	completer, ok := resolveHandler[ArgValueCompleter](handlers, chain[len(chain)-1].Handler)
	if !ok {
		return nil, false
	}
	seedCompletionContext(rtx, chain, words)
	cands := completer.CompleteArgValue(rtx, arg, partial)
	if cands == nil {
		return nil, false
	}
	return cands, true
}

// resolveHandler resolves handlerName on the aggregate handler set — the same
// reflection dispatch uses — and reports whether the handler implements T.
func resolveHandler[T any](handlers any, handlerName string) (T, bool) {
	var zero T
	if handlers == nil || handlerName == "" {
		return zero, false
	}
	m := reflect.ValueOf(handlers).MethodByName(handlerName)
	if !m.IsValid() || m.Type().NumIn() != 0 || m.Type().NumOut() != 1 {
		return zero, false
	}
	completer, ok := m.Call(nil)[0].Interface().(T)
	return completer, ok
}

// seedCompletionContext hands the resolved chain and completion words to the
// context a dynamic completer receives, so it can inspect them and reach bound
// services.
func seedCompletionContext(rtx *Context, chain []ResolvedCommand, words []string) {
	if rtx != nil {
		rtx.chain = chain
		rtx.Args = words
	}
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
		// A candidate may carry a "\t<description>" suffix (see withDescription);
		// the prefix matches — and duplicates collapse — on the NAME part only.
		name := c
		if before, _, ok := strings.Cut(c, "\t"); ok {
			name = before
		}
		if name != "" && strings.HasPrefix(name, prefix) && !seen[name] {
			seen[name] = true
			out = append(out, c)
		}
	}
	sort.Strings(out)
	return out
}

// withDescription suffixes a completion candidate with its one-line description
// as "name\tdescription" — the wire protocol descriptions ride on. zsh/fish/
// powershell render the description beside the name; the bash script strips it.
// An empty summary leaves the candidate bare, and only the summary's first line
// rides (the protocol is line-based). Dynamic completers (FlagValueCompleter /
// ArgValueCompleter) may return the same shape; bare values stay bare. Any ANSI
// styling in the summary is stripped (E6-S2): a styled summary is legitimate on
// the help-list surface, but escape sequences would corrupt the shell's
// completion rendering — the wire is the honest plain-text projection.
func withDescription(name, summary string) string {
	if summary == "" {
		return name
	}
	if i := strings.IndexByte(summary, '\n'); i >= 0 {
		summary = summary[:i]
	}
	summary = Strip(summary)
	if summary = strings.TrimSpace(summary); summary == "" {
		return name
	}
	return name + "\t" + summary
}
