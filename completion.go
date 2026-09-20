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

// FlagValueCompleter is an optional interface a command's handler may implement to supply
// dynamic completion candidates for one of its flags' values. Completion resolves the handler
// of the command that declares the flag and, if it implements this interface, calls
// CompleteFlagValue with the flag's logical name and the word being typed. A nil return falls
// back to the flag's static enum; a non-nil return, empty included, is authoritative.
//
// rtx carries the resolved chain, the completion words in [Context.Args], and every service
// bound on the Program, so a completer can reach a bound API client or the filesystem.
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
	if !found || !takesValue(fd) {
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
	return append(names, DiscoveredPlugins(cur)...)
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

// DiscoveredPlugins returns the token each plugin discovered for cmd is invoked by — "foo" for
// an executable "<prefix>foo" found next to the binary, in the discovery path, or on PATH —
// deduped and sorted, with any name colliding with a declared sub-command, remote command or
// alias removed. It returns nil when cmd has no discovery or discovery is hidden.
//
// It is the data feed for surfacing runtime plugins in help, which codegen cannot know about.
// rotini renders nothing itself; a help handler formats the result however it likes:
//
//	chain := rtx.Chain()
//	for _, name := range rotini.DiscoveredPlugins(chain[len(chain)-1]) {
//		fmt.Fprintf(out, "  %s\n", name)
//	}
//
// It touches the filesystem on every call and is best-effort: an unreadable directory
// contributes nothing rather than erroring. See [DiscoveryDiagnostics] to learn whether the
// author-configured path itself failed.
func DiscoveredPlugins(cmd ResolvedCommand) []string {
	plugins, _ := discoveredFor(cmd)
	return plugins
}

// DiscoveryDiagnostics returns the problems encountered while scanning cmd's author-configured
// discovery path — typically that it is missing or unreadable — and nil when there is no
// discovery, none is configured, or the path scanned cleanly. The incidental locations, next
// to the binary and the entries of $PATH, are deliberately not reported: a missing $PATH entry
// is normal, not a misconfiguration.
//
// It is the data feed for a doctor or completion handler that wants to tell the author their
// discovery path is wrong; rotini prints no warning itself, which would corrupt completion
// output. Each error carries the offending path and cause, so a caller can classify with
// errors.Is(err, fs.ErrNotExist).
func DiscoveryDiagnostics(cmd ResolvedCommand) []error {
	_, problems := discoveredFor(cmd)
	return problems
}

// discoveredFor is the shared core of [DiscoveredPlugins] and [DiscoveryDiagnostics]: the
// collision-filtered plugin tokens plus any problems scanning the configured path.
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
		rtx.Args = words
	}
}

// discoverPlugins lists the post-prefix names of `<prefix>*` executables found next to the
// host binary, in d.Path, and on PATH, deduped and sorted. It also returns any errors scanning
// d.Path; failures scanning the incidental locations are ignored as normal.
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
		if fd, _, found := findFlag(cc.chain, name); found && takesValue(fd) {
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
