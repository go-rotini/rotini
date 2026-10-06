package rotini

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"sort"
	"strconv"
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
// rtx carries the resolved chain, the completion words in [Context.Argv], and the dependencies
// registered with [Program.WithDependency].
//
// The line is half-typed, so [Context.Inputs] would fail validation. To read what has been
// typed so far, merge the lenient per-channel layers, which bind without validating. Flags of
// an ancestor are read with that ancestor's inputs type, running as that ancestor:
//
//	var in AppInputs
//	rotini.AsCommand(0, func(_ context.Context, rtx *rotini.Context) {
//		env, _ := rtx.EnvInputs[AppInputs]()
//		argv, _ := rtx.ArgvInputs[AppInputs]()
//		in = rotini.MergeInputs(env, argv) // argv wins, as it would at run time
//	})(context.Background(), rtx)
//
// A panic in a completer is not recovered. It may run on every keystroke, so it must be
// read-only and fast.
//
// A candidate may carry a one-line description after a tab, "value\tdescription": zsh, fish
// and powershell render it beside the value, and bash strips it.
type FlagValueCompleter interface {
	CompleteFlagValue(rtx *Context, flag, partial string) []string
}

// ArgValueCompleter is the positional-argument counterpart of [FlagValueCompleter]: completion
// calls CompleteArgValue on the invoked command's handler with the argument's logical name and
// the word being typed. The same contract applies: a nil return falls back to the static
// enum, and a candidate may carry a "value\tdescription" suffix.
type ArgValueCompleter interface {
	CompleteArgValue(rtx *Context, arg, partial string) []string
}

// completionContext is the dispatch-faithful reading of the words preceding the one being
// completed: the resolved chain, how many positionals the leaf has consumed, and whether a
// "--" terminator ended flag handling. It mirrors resolveChain so completion predicts exactly
// what dispatch would do with the same words.
type completionContext struct {
	chain           []Command
	positionals     int
	afterTerminator bool
	plugin          bool // a plugin token was hit: the rest belongs to the dispatched binary
	// shortCircuit records a short-circuit flag ([FlagDef.ShortCircuit]) already on the line:
	// the run it starts replaces the command's, so nothing further is offered.
	shortCircuit bool
}

// complete returns the candidates for the word currently being typed — the last element of
// words, the rest being context. It completes flag values, flag names, sub-command and
// plugin names, and positional values, all filtered by the typed prefix and excluding
// hidden inputs. An empty result lets the shell apply its own default.
func complete(def Definition, words []string, handlers any, rtx *Context) []string {
	if len(words) == 0 {
		words = []string{""}
	}
	partial := words[len(words)-1]
	context := words[:len(words)-1]

	cc := walkContext(def, context)
	if cc.plugin {
		return nil // the plugin binary owns its own argument surface
	}
	if cc.shortCircuit {
		return nil // a short-circuit flag replaces the run; nothing more belongs on the line
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

	// Until the first positional is consumed the word may also be a sub-command, plugin
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
// the inline "--flag=value" form. Only declared, non-hidden flags are offered, and the whole
// chain contributes, since ancestor flags resolve on descendants.
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
// and declared plugins with their aliases, and the plugins discovery finds.
func dispatchableNames(cur Command) []string {
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
	for _, r := range cur.Plugins {
		names = append(names, withDescription(r.Name, r.Summary))
		for _, a := range r.Aliases {
			names = append(names, withDescription(a, r.Summary))
		}
	}
	for _, p := range cur.DiscoveredPlugins() {
		names = append(names, p.Name)
	}
	return names
}

// walkContext resolves the words preceding the completed one with dispatch's semantics,
// leniently: unknown tokens are positionals, not errors. bash splits "--flag=value" into three
// words, so the literal "=" token glues such a value back onto its flag.
func walkContext(def Definition, context []string) completionContext {
	cc := completionContext{chain: []Command{rootFrame(def)}}
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
			if name, val, inline := splitFlag(tok); !cc.shortCircuit {
				if fd, _, ok := findFlag(cc.chain, name); ok && fd.ShortCircuit {
					on, err := strconv.ParseBool(val)
					cc.shortCircuit = !inline || (err == nil && on)
				} else if !ok {
					cc.shortCircuit = bundledShortCircuit(cc.chain, tok)
				}
			}
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
			if _, ok := findPlugin(cur, tok); ok || cur.PluginDiscovery != nil {
				cc.plugin = true
				return cc
			}
		}
		cc.positionals++
	}
	return cc
}

// bundledShortCircuit reports whether a bundle of short flags (-xh) sets a short-circuit flag,
// reading it as the parser does: each letter a flag, until one that takes a value, whose rest
// is that value.
func bundledShortCircuit(chain []Command, tok string) bool {
	if len(tok) < 3 || tok[0] != '-' || tok[1] == '-' || strings.Contains(tok, "=") {
		return false
	}
	for _, r := range tok[1:] {
		fd, _, ok := findFlag(chain, "-"+string(r))
		if !ok {
			return false
		}
		if fd.ShortCircuit {
			return true
		}
		if takesValue(fd) {
			return false
		}
	}
	return false
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
func flagValueCandidates(handlers any, rtx *Context, chain []Command, words []string, owner string, fd FlagDef, partial string) []string {
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

// dynamicFlagValues asks the declaring command's handler for candidates, when it implements
// [FlagValueCompleter]. It reports true only when a completer ran and returned a non-nil
// slice; otherwise the caller falls back to the static enum. handlers is the aggregate handler
// set, nil in purely structural callers.
func dynamicFlagValues(handlers any, rtx *Context, chain []Command, words []string, owner, flag, partial string) ([]string, bool) {
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
func dynamicArgValues(handlers any, rtx *Context, chain []Command, words []string, arg, partial string) ([]string, bool) {
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
func seedCompletionContext(rtx *Context, chain []Command, words []string) {
	if rtx != nil {
		markInvoked(chain)
		rtx.chain = chain
		rtx.Argv = words
	}
}

// discoverPlugins lists the `<prefix>*` executables found next to the host binary, in
// pluginPath, and on PATH — each under its post-prefix name, at the first path it was found —
// deduped and sorted by name. It also returns any errors scanning pluginPath — the
// author-configured location, where a failure is a real misconfiguration; failures scanning the
// incidental locations are ignored as normal.
func discoverPlugins(d *PluginDiscoveryDef, pluginPath string) ([]DiscoveredPlugin, []error) {
	if d.Prefix == "" {
		return nil, nil
	}
	seen := map[string]bool{}
	var out []DiscoveredPlugin
	var problems []error
	// On Windows a plugin is host-foo.exe (or another PATHEXT extension), offered as "foo";
	// a file without one cannot be run, so it is not a plugin.
	exts := executableExts(runtime.GOOS, os.Getenv("PATHEXT"))
	scan := func(dir string, report bool) {
		// A configured path that is a FILE is a misconfiguration on every OS. It is checked
		// first because the OSes disagree on how reading it fails: "not a directory" elsewhere,
		// but "path not found" on Windows, which would read as the harmless not-installed case.
		if report {
			if fi, err := fs.Stat(os.DirFS(dir), "."); err == nil && !fi.IsDir() {
				problems = append(problems, &fs.PathError{Op: "readdir", Path: dir, Err: errors.New("not a directory")})
				return
			}
		}
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
			name, runnable := trimExecutableExt(e.Name(), exts)
			if !runnable || name == d.Prefix || !strings.HasPrefix(name, d.Prefix) {
				continue
			}
			plugin := strings.TrimPrefix(name, d.Prefix)
			if seen[plugin] {
				continue
			}
			seen[plugin] = true
			out = append(out, DiscoveredPlugin{Name: plugin, Path: filepath.Join(dir, e.Name())})
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
	summary = StripANSI(summary)
	if summary = strings.TrimSpace(summary); summary == "" {
		return name
	}
	return name + "\t" + summary
}

// completionDirectivePrefix marks the directive line in __complete's output. It is a word no
// plausible candidate begins with, since a bare colon could start a value. The protocol is
// private between a generated script and the binary from the same generate pass, so it may be
// extended.
const completionDirectivePrefix = ":rotini:"

// completionHint returns the directive line for the word being completed, or "" when the input
// declares no hint and the shell should apply its own default.
//
//	:rotini:file            complete file paths
//	:rotini:file yaml yml   complete file paths with these extensions
//	:rotini:directory       complete directories only
//	:rotini:none            complete NOTHING — suppress the shell's file fallback
//
// It is kept separate from complete so the candidate list stays a plain list of strings.
func completionHint(def Definition, words []string) string {
	return directiveFor(completionHintFor(def, words))
}

// completionHintFor returns the declared hint for the word being completed, or the zero
// Completion when there is none. It is the wire-neutral half of completionHint, handed to
// every [CompletionFormat].
func completionHintFor(def Definition, words []string) Completion {
	if len(words) == 0 {
		return Completion{}
	}
	partial := words[len(words)-1]
	context := words[:len(words)-1]

	cc := walkContext(def, context)
	if cc.plugin {
		return Completion{} // the plugin binary owns its own argument surface
	}
	if cc.shortCircuit {
		return Completion{Kind: "none"} // offer nothing, not even the shell's file fallback
	}
	cur := cc.chain[len(cc.chain)-1]
	if cur.Passthrough || cc.afterTerminator {
		return Completion{}
	}

	// "--flag <TAB>": the word is the preceding flag's value.
	if name, ok := pendingValueFlag(context); ok {
		if fd, _, found := findFlag(cc.chain, name); found && takesSeparateValue(fd) {
			return fd.Complete
		}
		return Completion{}
	}

	// "--flag=<TAB>": the word carries its own flag.
	if strings.HasPrefix(partial, "-") {
		if name, _, hasInline := splitFlag(partial); hasInline {
			if fd, _, found := findFlag(cc.chain, name); found && takesValue(fd) {
				return fd.Complete
			}
		}
		return Completion{} // a flag NAME is being typed; paths are not candidates
	}

	// Otherwise the word binds to a positional — but only once dispatch can no longer
	// descend, since before that it may still be a sub-command name.
	if cc.positionals == 0 && len(dispatchableNames(cur)) > 0 {
		return Completion{}
	}
	if ad, ok := positionalAt(cur.Arguments, cc.positionals); ok {
		return ad.Complete
	}
	return Completion{}
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

// CompletionResult is one completion answer, before any wire format: what a program offers for
// the word being completed. It is what a [CompletionFormat] renders.
type CompletionResult struct {
	// Candidates are the offered values, in order — sub-commands, flags, enum values, or what a
	// handler's [FlagValueCompleter] or [ArgValueCompleter] returned — already filtered by the
	// typed prefix, with hidden inputs left out.
	Candidates []CompletionCandidate
	// Hint is the spec's declared `complete:` hint for the input being completed, or the zero
	// value when it declares none. A completer that answers still wins over it: the hint is the
	// fallback for when Candidates is empty, except kind "none", which also means "never offer
	// files" when there are candidates.
	Hint Completion
	// Messages are lines for the shell to show, not offer: what completers added with
	// [Context.AddCompletionMessage], in order, or else, when there are no candidates, the
	// input's static message ([Completion.Message]). Empty when completion messages are off or
	// switched off at run time.
	Messages []string
}

// CompletionCandidate is one offered value and its optional one-line description.
type CompletionCandidate struct {
	Value       string
	Description string
}

// CompletionFormat writes a [CompletionResult] to w in one completion protocol, the wire shape
// a completing host expects. rotini computes the answer; the format only encodes it.
// [PluginCompletion] is the built-in for the plugin hosts kubectl, Docker and Flux.
//
// A format is called once per request and must write only the answer, since the host parses
// all of it.
type CompletionFormat func(w io.Writer, result CompletionResult) error

// Complete answers one shell-completion request in the given format and returns the exit code,
// the completion counterpart of [Program.Run]. It serves hosts that do not call the hidden
// __complete entry; kubectl, for example, runs a separate kubectl_complete-<plugin> with only
// the plugin's words:
//
//	if strings.Contains(filepath.Base(os.Args[0]), "_complete-") {
//		code, _ := cmd.Program.Complete(os.Args[1:], rotini.PluginCompletion)
//		os.Exit(code)
//	}
//
// words are the words after the program's own name; the last is the word being completed,
// empty when the cursor starts a new one, and no words at all completes a new first word. The
// answer is what __complete computes, written to the program's stdout by format. A nil format
// uses rotini's own format, the __complete default, which is private to rotini's generated
// scripts.
//
// The exit code is 0, or 1 when the format fails to write, with its error.
func (p *Program) Complete(words []string, format CompletionFormat) (int, error) {
	if format == nil {
		format = rotiniCompletion
	}
	if len(words) == 0 {
		words = []string{""}
	}
	rtx := p.newRunContext()
	var added []string
	if p.def.CompletionMessages != nil {
		rtx.completionMessages = &added
	}
	result := CompletionResult{Hint: completionHintFor(p.def, words)}
	for _, c := range complete(p.def, words, p.handlers, rtx) {
		value, desc, _ := strings.Cut(c, "\t")
		result.Candidates = append(result.Candidates, CompletionCandidate{Value: value, Description: desc})
	}
	if p.def.CompletionMessages != nil && p.completionMessagesOn(rtx) {
		result.Messages = completionMessages(added, result)
	}
	if err := format(p.stdout, result); err != nil {
		return 1, err
	}
	return 0, nil
}

// completionMessages returns the messages a request shows: the ones completers added, in
// order, else the input's static message when there are no candidates. Each is one plain line;
// an empty one is dropped.
func completionMessages(added []string, result CompletionResult) []string {
	if len(added) == 0 && len(result.Candidates) == 0 && result.Hint.Message != "" {
		added = []string{result.Hint.Message}
	}
	var out []string
	for _, m := range added {
		if m = oneLine(m); m != "" {
			out = append(out, m)
		}
	}
	return out
}

// oneLine makes text a single plain line for a completion message: ANSI styling stripped, and
// line breaks, tabs and runs of spaces collapsed to one space.
func oneLine(text string) string {
	return strings.Join(strings.Fields(StripANSI(text)), " ")
}

// completionMessagesOn reports whether completion messages show for this request: the
// program's own rule when it set one with [Program.WithCompletionMessages], else the declared
// environment variable, which hides them when set to 0, false or off. With neither, they show.
func (p *Program) completionMessagesOn(rtx *Context) bool {
	if p.completionMessages != nil {
		return p.completionMessages(rtx)
	}
	if env := p.def.CompletionMessages.Env; env != "" {
		switch strings.ToLower(strings.TrimSpace(os.Getenv(env))) {
		case "0", "false", "off":
			return false
		}
	}
	return true
}

// WithCompletionMessages sets the program's own rule for whether completion messages show,
// replacing the check of the environment variable the conf's `messages_env` names. It is
// asked once per completion request, before anything is written, and applies to every shell
// and to [PluginCompletion]:
//
//	cmd.Program.WithCompletionMessages(func(rtx *rotini.Context) bool {
//		return !settings.Quiet
//	}).Execute()
//
// It has no effect unless the conf turns completion messages on. A nil fn restores the
// environment variable check.
func (p *Program) WithCompletionMessages(fn func(rtx *Context) bool) *Program {
	p.completionMessages = fn
	return p
}

// completionMessagePrefix marks a message line in rotini's own format.
const completionMessagePrefix = completionDirectivePrefix + "message "

// rotiniCompletion is rotini's own format: what the hidden __complete prints by default, and
// what the generated shell scripts read. One candidate per line ("value\tdescription"), then a
// ":rotini:message <text>" line per message, then the hint's ":rotini:" directive line when the
// input declares one. It is private between a script and the binary from the same generate, so
// it may change.
func rotiniCompletion(w io.Writer, result CompletionResult) error {
	lines := candidateLines(result.Candidates)
	for _, m := range result.Messages {
		lines = append(lines, completionMessagePrefix+m)
	}
	if d := directiveFor(result.Hint); d != "" {
		lines = append(lines, d)
	}
	return writeLines(w, lines)
}

// candidateLines renders candidates as "value" or "value\tdescription", the candidate line both
// built-in formats share.
func candidateLines(candidates []CompletionCandidate) []string {
	lines := make([]string, 0, len(candidates)+1)
	for _, c := range candidates {
		line := c.Value
		if c.Description != "" {
			line += "\t" + c.Description
		}
		lines = append(lines, line)
	}
	return lines
}

// writeLines writes lines, each newline-terminated, in one write.
func writeLines(w io.Writer, lines []string) error {
	var b strings.Builder
	for _, l := range lines {
		b.WriteString(l)
		b.WriteByte('\n')
	}
	if _, err := io.WriteString(w, b.String()); err != nil {
		return fmt.Errorf("write completion: %w", err)
	}
	return nil
}

// The directive numbers of the plugin hosts' completion format, as they appear on the wire: a
// bit set telling the shell what to do once the candidates are shown. The hosts fix the values.
const (
	pluginDirectiveDefault       = 0  // fall back to completing file names
	pluginDirectiveNoFileComp    = 4  // offer no file names
	pluginDirectiveFilterFileExt = 8  // complete file names with these extensions
	pluginDirectiveFilterDirs    = 16 // complete directory names only
)

// pluginMessageMarker starts a candidate line the plugin hosts' completion scripts show as a
// message instead of offering it.
const pluginMessageMarker = "_activeHelp_ "

// PluginCompletion is the [CompletionFormat] the plugin hosts kubectl, Docker and Flux read: one
// candidate per line ("value\tdescription" allowed), then a final ":<directive>" line, a number
// telling the shell what to do next. kubectl reads it from kubectl_complete-<plugin>, and the
// Docker and Flux CLIs from the plugin's own __complete (see [Program.WithCompletion]).
//
// The hint maps onto the directives: kind none to 4, offer no file names; kind directory to 16,
// directory names only; kind file with extensions to 8, file names with those extensions, which
// the format carries as the candidates; and kind file or no hint at all to 0, whose fallback is
// file completion. The hosts read the candidates of the filtering directives as their
// arguments, so a file or directory hint applies only when there are no candidates. The
// directive line is always written, since the hosts read the last line as the directive
// unconditionally. The format is the hosts', and follows them.
//
// Each message is written as a candidate line carrying the hosts' message marker, after the
// regular candidates, which the hosts' completion scripts show as a message where the shell
// can.
func PluginCompletion(w io.Writer, result CompletionResult) error {
	lines := candidateLines(result.Candidates)

	// Messages are not candidates, so they don't stand in the way of a file or directory hint.
	directive := pluginDirectiveDefault
	switch hint := result.Hint; {
	case hint.Kind == "none":
		directive = pluginDirectiveNoFileComp
	case len(lines) > 0:
	case hint.Kind == "directory":
		directive = pluginDirectiveFilterDirs
	case hint.Kind == "file" && len(hint.Extensions) > 0:
		directive = pluginDirectiveFilterFileExt
		lines = append(lines, hint.Extensions...)
	}
	for _, m := range result.Messages {
		lines = append(lines, pluginMessageMarker+m)
	}

	return writeLines(w, append(lines, fmt.Sprintf(":%d", directive)))
}

// WithCompletion sets the [CompletionFormat] the hidden __complete entry answers in, in place
// of rotini's own. It serves a plugin whose host completes it by calling the plugin's
// __complete and reading the host's format, as the Docker CLI (`docker-<name> __complete
// <name> …`) and the Flux CLI (`flux-<name> __complete …`) do with the format
// [PluginCompletion] writes:
//
//	cmd.Program.WithCompletion(rotini.PluginCompletion).Execute()
//
// rotini's generated completion scripts read rotini's own format, so a standalone CLI leaves
// this unset. A host that runs a separately named completer without a __complete word, such
// as kubectl's kubectl_complete-<name>, is served by calling [Program.Complete] from main.
//
// A nil format restores rotini's own.
func (p *Program) WithCompletion(format CompletionFormat) *Program {
	p.completion = format
	return p
}
