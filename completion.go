package rotini

import (
	"errors"
	"io/fs"
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

// FlagValueCompleter is an optional interface a command's handler may implement to supply
// dynamic completion candidates for one of its flags' values. Completion resolves the handler
// of the command that declares the flag and, if it implements this interface, calls
// CompleteFlagValue with the flag's logical name and the word being typed. A nil return falls
// back to the flag's static enum; a non-nil return, empty included, is authoritative.
//
// rtx carries the resolved chain, the completion words in [Context.Argv], and every service
// bound on the Program, so a completer can reach a bound API client or the filesystem.
//
// The line is half-typed, so a completer cannot [Collect]: required inputs are missing and
// validation would fail. To read what the user has said so far — a --kubeconfig on the line, or
// the environment variable that flag falls back to — overlay the lenient layers, which bind
// without validating. Flags of an ancestor (a root's global flags, say) are read with that
// ancestor's type at that ancestor's frame:
//
//	var in AppInputs
//	rotini.AtFrame(0, func(_ context.Context, rtx *rotini.Context) {
//		env, _ := rotini.ParseEnv[AppInputs](rtx)
//		argv, _ := rotini.ParseArgv[AppInputs](rtx)
//		in = rotini.OverlayInputs(env, argv) // argv wins, as it would at run time
//	})(context.Background(), rtx)
//
// It is entirely opt-in, a panic in it is not recovered, and it may be called on every
// keystroke — so it must be read-only and fast.
//
// A candidate may carry a one-line description after a tab, "value\tdescription", the same
// wire shape command and flag candidates use: zsh, fish and powershell render it beside the
// value, and bash strips it.
type FlagValueCompleter interface {
	CompleteFlagValue(rtx *Context, flag, partial string) []string
}

// ArgValueCompleter is the positional-argument counterpart of [FlagValueCompleter]: completion
// calls CompleteArgValue on the invoked command's handler with the argument's logical name and
// the word being typed. The same contract applies — a nil return falls back to the static
// enum, and the optional "value\tdescription" shape is available.
type ArgValueCompleter interface {
	CompleteArgValue(rtx *Context, arg, partial string) []string
}

// completionContext is the dispatch-faithful reading of the words preceding the one being
// completed: the resolved chain, how many positionals the leaf has consumed, and whether a
// "--" terminator ended flag handling. It mirrors resolveChain so completion predicts exactly
// what dispatch would do with the same words.
type completionContext struct {
	chain           []ResolvedCommand
	positionals     int
	afterTerminator bool
	remote          bool // a remote/plugin token was hit: the rest belongs to the dispatched binary
}

// complete returns the candidates for the word currently being typed — the last element of
// words, the rest being context. It completes flag values, flag names, sub-command and
// remote-command names, and positional values, all filtered by the typed prefix and excluding
// hidden inputs. An empty result lets the shell apply its own default.
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

	// Until the first positional is consumed the word may also be a sub-command, remote
	// command or discovered plugin; afterwards dispatch no longer descends.
	var names []string
	if !cc.afterTerminator && cc.positionals == 0 {
		names = dispatchableNames(cur)
	}
	names = append(names, argValueCandidates(handlers, rtx, cc, words, partial)...)
	return filterPrefix(names, partial)
}

// completePendingFlagValue handles the separate-word form — "--flag <TAB>", and bash's
// "--flag = val" splitting — where the word being completed is the preceding flag's value. An
// empty result falls back to the shell's file completion, never to sub-command names, which
// dispatch would read as this flag's value.
func completePendingFlagValue(cc completionContext, context, words []string, partial string, handlers any, rtx *Context) ([]string, bool) {
	if cc.afterTerminator {
		return nil, false
	}
	name, ok := pendingValueFlag(context)
	if !ok {
		return nil, false
	}
	fd, owner, found := findFlag(cc.chain, name)
	if !found || !takesSeparateValue(fd) {
		return nil, false
	}
	return filterPrefix(flagValueCandidates(handlers, rtx, cc.chain, words, owner, fd, partial), partial), true
}

// completeFlagWord handles a word beginning with "-": either a flag name, or a flag's value in
// the inline "--flag=value" form. Only declared, non-hidden flags are offered — rotini
// auto-adds none — and the whole chain contributes, since ancestor flags resolve on descendants.
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

// dispatchableNames lists everything the next positional word could dispatch to: sub-commands
// and remote commands with their aliases, and the plugins discovery finds.
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
	for _, p := range DiscoveredPlugins(cur) {
		names = append(names, p.Name)
	}
	return names
}

// walkContext resolves the words preceding the completed one with dispatch's semantics,
// leniently: unknown tokens are positionals, not errors. bash splits "--flag=value" into three
// words, so the literal "=" token glues such a value back onto its flag.
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
			// Skip a separate value word so it is not mistaken for a command — unless it is
			// the "=" glue, which the next iteration handles.
			if i+1 < len(context) && context[i+1] != "=" {
				i += flagTokenWidth(cc.chain, context, i)
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

// pendingValueFlag reports the flag whose value the next word supplies, when the context ends
// with one awaiting a value.
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

// flagValueCandidates returns the candidates for one flag's value: the owning handler's dynamic
// completer when it answers, else a map flag's declared key vocabulary, else the static enum.
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

// argValueCandidates returns the candidates for the argument the completed word would bind to
// — the leaf's next positional index, a trailing variadic absorbing everything past the end.
// The leaf handler's dynamic completer wins when it answers, else the static enum.
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
// name colliding with a declared sub-command, remote command or alias removed. It returns nil
// when cmd has no discovery or discovery is hidden.
//
// It is the data feed for surfacing runtime plugins in help or a `plugin list`, which codegen
// cannot know about. rotini renders nothing itself; a handler formats the result however it
// likes:
//
//	chain := rtx.Chain()
//	for _, p := range rotini.DiscoveredPlugins(chain[len(chain)-1]) {
//		fmt.Fprintf(out, "  %s\t%s\n", p.Name, p.Path)
//	}
//
// It touches the filesystem on every call and is best-effort: an unreadable directory
// contributes nothing rather than erroring. See [DiscoveryDiagnostics] to learn whether the
// author-configured path itself failed.
func DiscoveredPlugins(cmd ResolvedCommand) []DiscoveredPlugin {
	plugins, _ := discoveredFor(cmd)
	return plugins
}

// DiscoveryDiagnostics returns the problems encountered while scanning cmd's author-configured
// discovery path — typically that it is unreadable, or not a directory — and nil when there is
// no discovery, none is configured, or the path scanned cleanly. A path that does not exist is
// not a problem: it is where plugins go once one is installed, and before that it is empty. The
// incidental locations, next to the binary and the entries of $PATH, are deliberately not
// reported: a missing $PATH entry is normal, not a misconfiguration.
//
// It is the data feed for a doctor or completion handler that wants to tell the author their
// discovery path is wrong; rotini prints no warning itself, which would corrupt completion
// output. Each error carries the offending path and cause, so a caller can classify with
// errors.Is(err, fs.ErrPermission).
func DiscoveryDiagnostics(cmd ResolvedCommand) []error {
	_, problems := discoveredFor(cmd)
	return problems
}

// discoveredFor is the shared core of [DiscoveredPlugins] and [DiscoveryDiagnostics]: the
// collision-filtered plugin tokens plus any problems scanning the configured path.
func discoveredFor(cmd ResolvedCommand) ([]DiscoveredPlugin, []error) {
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
	all, problems := discoverPlugins(d, cmd.PluginPath)
	var out []DiscoveredPlugin
	for _, plugin := range all {
		if !declared[plugin.Name] {
			out = append(out, plugin)
		}
	}
	return out, problems
}

// dynamicFlagValues asks the declaring command's handler for candidates, when it implements
// [FlagValueCompleter]. It reports true only when a completer ran and returned a non-nil
// slice; otherwise the caller falls back to the static enum. handlers is the aggregate handler
// set, nil in purely structural callers.
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

// dynamicArgValues is dynamicFlagValues' positional counterpart, asking the chain leaf's
// handler, since positionals always bind to the leaf.
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
	completer, ok := reflect.TypeAssert[T](m.Call(nil)[0])
	return completer, ok
}

// seedCompletionContext hands the resolved chain and completion words to the context a dynamic
// completer receives.
func seedCompletionContext(rtx *Context, chain []ResolvedCommand, words []string) {
	if rtx != nil {
		rtx.chain = chain
		rtx.Argv = words
	}
}

// discoverPlugins lists the `<prefix>*` executables found next to the host binary, in
// pluginPath, and on PATH — each under its post-prefix name, at the first path it was found —
// deduped and sorted by name. It also returns any errors scanning pluginPath — the
// author-configured location, where a failure is a real misconfiguration; failures scanning the
// incidental locations are ignored as normal.
func discoverPlugins(d *RemoteDiscoveryDef, pluginPath string) ([]DiscoveredPlugin, []error) {
	if d.Prefix == "" {
		return nil, nil
	}
	seen := map[string]bool{}
	var out []DiscoveredPlugin
	var problems []error
	scan := func(dir string, report bool) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			// A plugin directory that does not exist yet is the normal state before the
			// first plugin is installed, not a misconfiguration.
			if report && !errors.Is(err, fs.ErrNotExist) {
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
			out = append(out, DiscoveredPlugin{Name: plugin, Path: filepath.Join(dir, name)})
		}
	}
	if exe, err := os.Executable(); err == nil {
		scan(filepath.Dir(exe), false)
	}
	if pluginPath != "" {
		scan(pluginPath, true) // the author-configured path: a scan failure is a real diagnostic
	}
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if dir != "" {
			scan(dir, false)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, problems
}

// filterPrefix keeps the candidates whose name starts with prefix, dropping empty names and
// duplicate names, and returns them sorted.
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

// withDescription suffixes a candidate with its one-line description as "name\tdescription",
// the wire protocol descriptions ride on. An empty summary leaves the candidate bare, and only
// the summary's first line rides, since the protocol is line-based. ANSI styling is stripped:
// escape sequences would corrupt the shell's completion rendering.
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

// completionDirectivePrefix marks the directive line in __complete's output. A candidate could
// in principle start with a colon, so the marker is a word no plausible value begins with.
//
// The protocol is private between a generated script and the binary from the same generate
// pass, which is what makes it safe to extend (see COMPATIBILITY.md).
const completionDirectivePrefix = ":rotini:"

// completionHint returns the directive line for the word being completed, or "" when the input
// declares no hint and the shell should apply its own default.
//
//	:rotini:file            complete file paths
//	:rotini:file yaml yml   complete file paths with these extensions
//	:rotini:directory       complete directories only
//	:rotini:none            complete NOTHING — suppress the shell's file fallback
//
// It answers the same question complete() does — which input's value is being typed — and is
// kept separate so the candidate list stays a plain list of strings.
func completionHint(def Definition, words []string) string {
	if len(words) == 0 {
		return ""
	}
	partial := words[len(words)-1]
	context := words[:len(words)-1]

	cc := walkContext(def, context)
	if cc.remote {
		return "" // the remote binary owns its own argument surface
	}
	cur := cc.chain[len(cc.chain)-1]
	if cur.Passthrough || cc.afterTerminator {
		return ""
	}

	// "--flag <TAB>": the word is the preceding flag's value.
	if name, ok := pendingValueFlag(context); ok {
		if fd, _, found := findFlag(cc.chain, name); found && takesSeparateValue(fd) {
			return directiveFor(fd.Complete)
		}
		return ""
	}

	// "--flag=<TAB>": the word carries its own flag.
	if strings.HasPrefix(partial, "-") {
		if name, _, hasInline := splitFlag(partial); hasInline {
			if fd, _, found := findFlag(cc.chain, name); found && takesValue(fd) {
				return directiveFor(fd.Complete)
			}
		}
		return "" // a flag NAME is being typed; paths are not candidates
	}

	// Otherwise the word binds to a positional — but only once dispatch can no longer
	// descend, since before that it may still be a sub-command name.
	if cc.positionals == 0 && len(dispatchableNames(cur)) > 0 {
		return ""
	}
	if ad, ok := positionalAt(cur.Arguments, cc.positionals); ok {
		return directiveFor(ad.Complete)
	}
	return ""
}

// positionalAt returns the argument the next positional binds to, a trailing variadic
// absorbing everything past the declared end.
func positionalAt(args []ArgDef, idx int) (ArgDef, bool) {
	if idx < len(args) {
		return args[idx], true
	}
	if len(args) > 0 && args[len(args)-1].Variadic {
		return args[len(args)-1], true
	}
	return ArgDef{}, false
}

// directiveFor renders one Completion as its wire line, or "" for the zero value.
func directiveFor(c Completion) string {
	switch c.Kind {
	case "file":
		if len(c.Extensions) == 0 {
			return completionDirectivePrefix + "file"
		}
		return completionDirectivePrefix + "file " + strings.Join(c.Extensions, " ")
	case "directory":
		return completionDirectivePrefix + "directory"
	case "none":
		return completionDirectivePrefix + "none"
	}
	return ""
}
