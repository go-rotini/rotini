package rotini

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
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
// rtx carries the resolved chain, the completion words in [Context.Argv] (the last is the word
// being typed), the program's base context in [Context.Context], and the dependencies
// registered with [Program.WithDependency]. The completer runs as the command that declares the
// flag, so [Context.Command] and the inputs methods anchor there, an ancestor's included.
//
// The line is half-typed, so [Context.Inputs] would fail validation. [Context.PartialInputs]
// reads what has been typed so far, leniently, over the environment and the defaults:
//
//	in, _, _ := rtx.PartialInputs[DeployInputs]()
//	return servicesIn(rtx.Context(), in.Deploy.Flags.Region)
//
// A panic in a completer is not recovered. It may run on every keystroke, so it must be
// read-only and fast; a completer that calls the network derives a deadline from
// [Context.Context].
//
// Candidates are offered in the order returned. A candidate may carry a one-line description
// after a tab, "value\tdescription", which every shell shows beside the value (bash on the
// second TAB) unless the conf's `descriptions_env` switches descriptions off.
// [Context.SetCompletionOptions] asks the shell not to add a space after the inserted value.
type FlagValueCompleter interface {
	CompleteFlagValue(rtx *Context, flag, partial string) []string
}

// ArgValueCompleter is the positional-argument counterpart of [FlagValueCompleter]: completion
// calls CompleteArgValue on the invoked command's handler with the argument's logical name and
// the word being typed. The same contract applies: a nil return falls back to the static
// enum, candidates keep their order, and a candidate may carry a "value\tdescription" suffix.
type ArgValueCompleter interface {
	CompleteArgValue(rtx *Context, arg, partial string) []string
}

// completionContext is the dispatch-faithful reading of the words preceding the one being
// completed: the resolved chain, the leaf's positionals, the flags already set, and where flags
// stopped. It mirrors resolveChain and the parser so completion predicts exactly what dispatch
// would do with the same words.
type completionContext struct {
	chain       []Command
	positionals int
	// args are the leaf's positional words, in order.
	args            []string
	afterTerminator bool
	plugin          bool // a plugin token was hit: the rest belongs to the dispatched binary
	// shortCircuit records a short-circuit flag ([FlagDef.ShortCircuit]) already on the line:
	// the run it starts replaces the command's, so nothing further is offered.
	shortCircuit bool
	// operands records that flags have stopped without a "--": the leaf takes options first and
	// its first argument was typed, or its passthrough argument has started. Every later word is
	// an argument, so no flag, flag value or sub-command is offered; the argument's own
	// completer, enum and hint still apply.
	operands bool
	// set records, per chain frame, the logical names of the flags already on the line, keyed
	// by the frame that declares each flag.
	set []map[string]bool
	// pending is the flag whose value the word being completed is, or nil.
	pending *pendingFlag
}

// pendingFlag is a flag awaiting its value in the word being completed.
type pendingFlag struct {
	fd  FlagDef
	idx int // the chain frame that declares it
	// mapValue is bash's split of a map entry, "--flag key = <word>": the word is the value
	// after the key, which completion has nothing to offer for.
	mapValue bool
}

// completionAnswer is the candidate list for one request, in the order to offer it.
type completionAnswer struct {
	cands []string
	// keepOrder reports that the order is the program's, not alphabetical: a completer's own
	// order, an enum's declared order, or unset required flags first.
	keepOrder bool
}

// complete returns the candidates for the word currently being typed — the last element of
// words, the rest being context — in the order to offer them. See completeAnswer.
func complete(def Definition, words []string, lookup HandlerLookup, rtx *Context) []string {
	return completeAnswer(def, words, lookup, rtx).cands
}

// completeAnswer completes flag values, flag names, sub-command and plugin names, and
// positional values, all filtered by the typed prefix and excluding hidden and deprecated
// spellings, flags already set, and the other members of a set exclusive group. An empty
// result lets the shell apply its own default.
func completeAnswer(def Definition, words []string, lookup HandlerLookup, rtx *Context) completionAnswer {
	if len(words) == 0 {
		words = []string{""}
	}
	if bashGlue(words) {
		// bash would replace the "=" with a candidate, losing it: only the hint applies.
		return completionAnswer{}
	}
	partial := words[len(words)-1]
	cc := walkContext(def, words[:len(words)-1])
	bindChainView(cc.chain, rtx.osView())
	if cc.plugin {
		return completionAnswer{} // the plugin binary owns its own argument surface
	}
	if cc.shortCircuit {
		return completionAnswer{} // a short-circuit flag replaces the run; nothing more belongs on the line
	}
	cur := cc.chain[len(cc.chain)-1]
	if cur.Passthrough {
		return completionAnswer{} // raw tokens past the boundary: let the shell fall back to files
	}

	if cc.pending != nil {
		return completePendingFlagValue(cc, words, partial, lookup, rtx)
	}
	if !cc.afterTerminator && !cc.operands && strings.HasPrefix(partial, "-") {
		return completeFlagWord(cc, words, partial, lookup, rtx)
	}

	// Until the first positional is consumed the word may also be a sub-command, plugin
	// command or discovered plugin; afterwards dispatch no longer descends. Sub-command names
	// come first, sorted, then the argument's candidates in their own order.
	var names []string
	if !cc.afterTerminator && !cc.operands && cc.positionals == 0 {
		names = filterPrefix(dispatchableNames(cur), partial)
		sort.Strings(names)
	}
	vals := argValueCandidates(def, lookup, rtx, cc, words, partial)
	return merged(names, vals, partial)
}

// merged appends a value list to sorted names, dropping values whose name is already offered.
func merged(names []string, vals valueCandidates, partial string) completionAnswer {
	ans := vals.answer(partial)
	if len(names) == 0 {
		return ans
	}
	out := filterPrefix(append(names, ans.cands...), partial)
	return completionAnswer{cands: out, keepOrder: len(out) > 1 && (ans.keepOrder || !slices.IsSorted(out))}
}

// valueCandidates are the candidates for a value, with where they came from, which decides
// their order.
type valueCandidates struct {
	cands []string
	// dynamic marks a completer's answer, whose order is the completer's.
	dynamic bool
	// sorted marks a vocabulary with no order of its own (map keys, command names); otherwise
	// the declared order is kept.
	sorted bool
}

// answer filters the candidates by prefix and orders them: sorted, or kept as given.
func (v valueCandidates) answer(prefix string) completionAnswer {
	out := filterPrefix(v.cands, prefix)
	if v.sorted {
		sort.Strings(out)
		return completionAnswer{cands: out}
	}
	return completionAnswer{cands: out, keepOrder: len(out) > 1 && (v.dynamic || !slices.IsSorted(out))}
}

// completePendingFlagValue completes the separate-word form — "--flag <TAB>", and bash's
// "--flag = val" splitting — where the word being completed is the preceding flag's value. An
// empty result falls back to the shell's file completion, never to sub-command names, which
// dispatch would read as this flag's value.
func completePendingFlagValue(cc completionContext, words []string, partial string, lookup HandlerLookup, rtx *Context) completionAnswer {
	p := cc.pending
	if p.mapValue || !takesValue(p.fd) {
		return completionAnswer{}
	}
	return flagValueCandidates(lookup, rtx, cc.chain, words, p.idx, p.fd, partial).answer(partial)
}

// completeFlagWord completes a word beginning with "-": either a flag name, or a flag's value in
// the inline "--flag=value" form. Only declared, visible, current flags are offered, and the
// whole chain contributes, since ancestor flags resolve on descendants. Unset required flags
// come first.
func completeFlagWord(cc completionContext, words []string, partial string, lookup HandlerLookup, rtx *Context) completionAnswer {
	if name, val, hasInline := splitFlag(partial); hasInline {
		fd, idx, negated, found := findFlagMatch(cc.chain, name)
		if !found || negated || !takesValue(fd) {
			return completionAnswer{} // a flag that takes no value has nothing to offer after "="
		}
		vals := flagValueCandidates(lookup, rtx, cc.chain, words, idx, fd, val)
		inline := make([]string, len(vals.cands))
		for i, c := range vals.cands {
			inline[i] = name + "=" + c
		}
		vals.cands = inline
		return vals.answer(partial)
	}

	var required, rest []string
	claimed := map[string]bool{} // a spelling a nearer frame declares, which shadows an ancestor's
	for i, v := range slices.Backward(cc.chain) {
		hidden, needed := groupEffects(v, cc.setAt(i))
		var mine []string
		for _, f := range v.Flags {
			mine = append(append(mine, f.Identifiers...), negatedIdentifiers(f)...)
			if !offerFlag(f, cc.setAt(i), hidden) {
				continue
			}
			var ids []string
			for _, id := range flagSpellings(f) {
				if !claimed[id] {
					ids = append(ids, withDescription(id, f.Summary))
				}
			}
			if !f.ShortCircuit && !cc.setAt(i)[f.Name] && (f.Required || needed[f.Name]) {
				required = append(required, ids...)
			} else {
				rest = append(rest, ids...)
			}
		}
		for _, id := range mine {
			claimed[id] = true
		}
	}
	required = filterPrefix(required, partial)
	sort.Strings(required)
	rest = filterPrefix(rest, partial)
	sort.Strings(rest)
	out := filterPrefix(append(required, rest...), partial)
	return completionAnswer{cands: out, keepOrder: !slices.IsSorted(out)}
}

// offerFlag reports whether flag f, of a frame whose set flags are set, is offered by name:
// not hidden or deprecated, not hidden by an exclusive group, and not already set unless it
// takes several values.
func offerFlag(f FlagDef, set, hidden map[string]bool) bool {
	switch {
	case f.Hidden, f.Deprecated != "", hidden[f.Name]:
		return false
	case set[f.Name]:
		return repeatableFlag(f)
	}
	return true
}

// repeatableFlag reports whether setting f again adds to it rather than replacing it: a list,
// map or count flag, or an object flag, which merges per-field occurrences.
func repeatableFlag(f FlagDef) bool {
	return strings.HasPrefix(f.Type, "[]") || isMapType(f.Type) || f.Type == "count" || f.ObjectSchema != ""
}

// flagSpellings lists the identifiers completion offers for f: its identifiers, then the
// negated forms of a negatable flag, leaving out deprecated identifiers and their negated
// forms. They are still accepted when typed.
func flagSpellings(f FlagDef) []string {
	var ids []string
	for _, id := range f.Identifiers {
		if !slices.Contains(f.DeprecatedIdentifiers, id) {
			ids = append(ids, id)
		}
	}
	if f.Negatable {
		live := f
		live.Identifiers = ids
		ids = append(ids, negatedIdentifiers(live)...)
	}
	return ids
}

// groupEffects reads a frame's flag groups and dependencies against the flags already set
// there: hidden are the other members of an exclusive group (mutually_exclusive or one_of) one
// of whose members is set, and needed are the flags the groups and dependencies require now.
func groupEffects(frame Command, set map[string]bool) (hidden, needed map[string]bool) {
	hidden, needed = map[string]bool{}, map[string]bool{}
	anySet := func(names []string) bool {
		return slices.ContainsFunc(names, func(n string) bool { return set[n] })
	}
	for _, g := range frame.FlagGroups {
		switch g.Kind {
		case FlagGroupMutuallyExclusive, FlagGroupOneOf:
			if anySet(g.Flags) {
				for _, n := range g.Flags {
					hidden[n] = !set[n]
				}
			} else if g.Kind == FlagGroupOneOf {
				for _, n := range g.Flags {
					needed[n] = true
				}
			}
		case FlagGroupAtLeastOne:
			if !anySet(g.Flags) {
				for _, n := range g.Flags {
					needed[n] = true
				}
			}
		case FlagGroupRequiredTogether:
			if anySet(g.Flags) {
				for _, n := range g.Flags {
					needed[n] = true
				}
			}
		}
	}
	for _, d := range frame.FlagDependencies {
		if set[d.When] {
			for _, n := range d.Requires {
				needed[n] = true
			}
		}
	}
	return hidden, needed
}

// dispatchableNames lists everything the next positional word could dispatch to: sub-commands
// and declared plugins with their aliases, and the plugins discovery finds. Hidden and
// deprecated commands and deprecated aliases are left out; they still dispatch when typed.
func dispatchableNames(cur Command) []string {
	names := commandNames(cur)
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

// commandNames lists cur's visible sub-commands and their aliases, without deprecated ones.
func commandNames(cur Command) []string {
	var names []string
	for _, c := range cur.Commands {
		if c.Hidden || c.Deprecated != "" {
			continue
		}
		names = append(names, withDescription(c.Name, c.Summary))
		for _, a := range c.Aliases {
			if !slices.Contains(c.DeprecatedIdentifiers, a) {
				names = append(names, withDescription(a, c.Summary))
			}
		}
	}
	return names
}

// walkContext resolves the words preceding the completed one with dispatch's semantics,
// leniently: unknown tokens are positionals or skipped flags, not errors. Flags are read with
// the parser's own token reader, so clusters (-vx), negated forms and object fields count as
// set exactly as they would at run time. bash splits "--flag=value" into three words, and a
// map entry "key=value" into three more, so the literal "=" word glues such values back on.
// Flags stop where the parser stops them: at "--", at an options_first leaf's first argument,
// and at the leaf's passthrough argument (see completionContext.operands).
func walkContext(def Definition, before []string) completionContext {
	cc := completionContext{chain: []Command{rootFrame(def)}, set: make([]map[string]bool, 1)}
	for i := 0; i < len(before); i++ {
		tok := before[i]
		if cc.afterTerminator || cc.operands {
			cc.positionals++
			cc.args = append(cc.args, tok)
			continue
		}
		if tok == "--" {
			cc.afterTerminator = true
			continue
		}
		if tok == "=" {
			continue // a split "=" with no flag before it
		}
		if isFlag(cc.chain, tok) {
			i = cc.walkFlag(before, i)
			continue
		}
		cur := cc.chain[len(cc.chain)-1]
		if cc.positionals == 0 {
			if child, ok := findChild(cur, tok); ok {
				cc.chain = append(cc.chain, cmdFrame(child))
				cc.set = append(cc.set, nil)
				continue
			}
			if _, ok := findPlugin(cur, tok); ok || cur.PluginDiscovery != nil {
				cc.plugin = true
				return cc
			}
		}
		cc.operands = cur.OptionsFirst || cc.positionals == passthroughArg(cur.Arguments)
		cc.positionals++
		cc.args = append(cc.args, tok)
	}
	return cc
}

// walkFlag reads the flag word before[i] and its value, recording each flag it sets, and
// returns the index of the last word it used. A flag still waiting for its value at the end of
// the context becomes cc.pending.
func (cc *completionContext) walkFlag(before []string, i int) int {
	tok := before[i]
	var last *pendingFlag
	capture := func(idx int, fd FlagDef, value, _ string) error {
		cc.mark(idx, fd, value)
		last = &pendingFlag{fd: fd, idx: idx}
		return nil
	}

	// bash's "--flag = value" split: the value is the flag's inline one.
	if i+1 < len(before) && before[i+1] == "=" {
		if i+2 == len(before) {
			// The completed word is the value. An empty inline value counts as set.
			if _, err := consumeFlagToken(cc.chain, tok+"=", nil, 0, capture); err == nil {
				cc.pending = last
			}
			return i + 1
		}
		if _, err := consumeFlagToken(cc.chain, tok+"="+before[i+2], nil, 0, capture); err != nil {
			return i + 2 // an unknown flag and its value; the run reports it
		}
		return cc.walkMapValue(before, i+2, last)
	}

	extra, err := consumeFlagToken(cc.chain, tok, before, i, capture)
	if err != nil {
		var pe *ParseError
		if errors.As(err, &pe) && pe.Kind == ParseKindNeedsValue && i == len(before)-1 {
			if fd, idx, _, ok := findFlagMatch(cc.chain, pe.Flag); ok {
				cc.pending = &pendingFlag{fd: fd, idx: idx}
			}
		}
		return i // an unknown or malformed flag consumes nothing; the run reports it
	}
	if extra == 0 {
		return i
	}
	return cc.walkMapValue(before, i+extra, last)
}

// walkMapValue handles bash's split of a map entry after a map flag's value word at j:
// "key = value" are three words. It returns the index of the last word the entry uses, and
// marks the completed word as the entry's value when the context ends at the "=".
func (cc *completionContext) walkMapValue(before []string, j int, last *pendingFlag) int {
	if last == nil || !isMapType(last.fd.Type) || j+1 >= len(before) || before[j+1] != "=" {
		return j
	}
	if j+2 == len(before) {
		cc.pending = &pendingFlag{fd: last.fd, idx: last.idx, mapValue: true}
		return j + 1
	}
	return j + 2
}

// mark records that flag fd of chain frame idx is set to value, and whether that starts a
// short-circuit run.
func (cc *completionContext) mark(idx int, fd FlagDef, value string) {
	for len(cc.set) <= idx {
		cc.set = append(cc.set, nil)
	}
	if cc.set[idx] == nil {
		cc.set[idx] = map[string]bool{}
	}
	cc.set[idx][fd.Name] = true
	if fd.ShortCircuit {
		on, err := strconv.ParseBool(value)
		cc.shortCircuit = cc.shortCircuit || fd.Type != "bool" || (err == nil && on)
	}
}

// setAt returns the flags set on chain frame idx.
func (cc *completionContext) setAt(idx int) map[string]bool {
	if idx < len(cc.set) {
		return cc.set[idx]
	}
	return nil
}

// flagValueCandidates returns the candidates for one flag's value: the declaring handler's
// dynamic completer when it answers, else a map flag's declared key vocabulary, else the
// static enum in its declared order.
func flagValueCandidates(lookup HandlerLookup, rtx *Context, chain []Command, words []string, idx int, fd FlagDef, partial string) valueCandidates {
	// An '@' on a from:file flag is a path in progress — offer nothing, so the
	// shell falls back to its own file completion.
	if strings.HasPrefix(partial, "@") && slices.Contains(fd.From, "file") {
		return valueCandidates{}
	}
	if cands, dyn := dynamicFlagValues(lookup, rtx, chain, words, idx, fd.Name, partial); dyn {
		return valueCandidates{cands: cands, dynamic: true}
	}
	if len(fd.KeyPaths) > 0 && isMapType(fd.Type) && !strings.Contains(partial, "=") {
		keys := make([]string, len(fd.KeyPaths))
		for i, k := range fd.KeyPaths {
			keys[i] = k + "=" // the value past the '=' is the user's to write
		}
		return valueCandidates{cands: keys, sorted: true}
	}
	return valueCandidates{cands: enumCandidates(flagEnum(fd))}
}

// argValueCandidates returns the candidates for the argument the completed word would bind to
// — the leaf's next positional index, a trailing variadic absorbing everything past the end.
// The leaf handler's dynamic completer wins when it answers, else command paths for kind
// "command", else the static enum.
func argValueCandidates(def Definition, lookup HandlerLookup, rtx *Context, cc completionContext, words []string, partial string) valueCandidates {
	cur := cc.chain[len(cc.chain)-1]
	ad, ok := positionalAt(cur.Arguments, cc.positionals)
	if !ok || ad.Hidden {
		return valueCandidates{}
	}
	// An @file value is a path: the shell falls back to its own file completion.
	if strings.HasPrefix(partial, "@") && slices.Contains(ad.From, "file") {
		return valueCandidates{}
	}
	if cands, dyn := dynamicArgValues(lookup, rtx, cc.chain, words, ad.Name, partial); dyn {
		return valueCandidates{cands: cands, dynamic: true}
	}
	if ad.Complete.Kind == completeKindCommand {
		var path []string
		if v := variadicIndex(cur.Arguments); v >= 0 && v <= len(cc.args) {
			path = cc.args[v:]
		}
		return valueCandidates{cands: commandPathCandidates(def, path), sorted: true}
	}
	return valueCandidates{cands: enumCandidates(argEnum(ad))}
}

// commandPathCandidates returns the visible sub-commands of the command path names below the
// root, or nothing when a word names no command. The path is root-relative, as the program's
// help is, whichever command the argument belongs to.
func commandPathCandidates(def Definition, path []string) []string {
	cur := rootFrame(def)
	for _, w := range path {
		child, ok := findChild(cur, w)
		if !ok {
			return nil
		}
		cur = cmdFrame(child)
	}
	return commandNames(cur)
}

// enumCandidates returns an enum's offered values in declared order, each with its summary:
// hidden and deprecated values, and aliases, are accepted but not offered.
func enumCandidates(enum enumSet) []string {
	listed := enum.listed()
	if len(enum.described) == 0 {
		return listed
	}
	out := make([]string, len(listed))
	for i, v := range listed {
		out[i] = v
		if d, ok := enum.describe(v); ok {
			out[i] = withDescription(v, d.Summary)
		}
	}
	return out
}

// dynamicFlagValues asks the handler of chain frame idx, which declares the flag, for
// candidates when it implements [FlagValueCompleter], running as that command. It reports true
// only when a completer ran and returned a non-nil slice; otherwise the caller falls back to
// the static enum. lookup is the program's handler lookup, nil in purely structural callers.
func dynamicFlagValues(lookup HandlerLookup, rtx *Context, chain []Command, words []string, idx int, flag, partial string) ([]string, bool) {
	completer, ok := resolveHandler[FlagValueCompleter](lookup, chain[idx].Handler)
	if !ok {
		return nil, false
	}
	defer seedCompletionContext(rtx, chain, words, idx)()
	cands := completer.CompleteFlagValue(rtx, flag, partial)
	if cands == nil {
		return nil, false
	}
	return cands, true
}

// dynamicArgValues is dynamicFlagValues' positional counterpart, asking the chain leaf's
// handler, since positionals always bind to the leaf.
func dynamicArgValues(lookup HandlerLookup, rtx *Context, chain []Command, words []string, arg, partial string) ([]string, bool) {
	leaf := len(chain) - 1
	completer, ok := resolveHandler[ArgValueCompleter](lookup, chain[leaf].Handler)
	if !ok {
		return nil, false
	}
	defer seedCompletionContext(rtx, chain, words, leaf)()
	cands := completer.CompleteArgValue(rtx, arg, partial)
	if cands == nil {
		return nil, false
	}
	return cands, true
}

// resolveHandler resolves handlerName through the program's lookup — the same one dispatch
// uses — and reports whether the handler implements T.
func resolveHandler[T any](lookup HandlerLookup, handlerName string) (T, bool) {
	var zero T
	if lookup == nil || handlerName == "" {
		return zero, false
	}
	h, ok := lookup(handlerName)
	if !ok {
		return zero, false
	}
	completer, ok := any(h).(T)
	return completer, ok
}

// seedCompletionContext hands the resolved chain and completion words to the context a dynamic
// completer receives, running as chain frame idx, and returns the function that restores the
// frame.
func seedCompletionContext(rtx *Context, chain []Command, words []string, idx int) func() {
	if rtx == nil {
		return func() {}
	}
	markInvoked(chain)
	rtx.mu.Lock()
	rtx.chain = chain
	rtx.Argv = words
	rtx.mu.Unlock()
	prev := rtx.setFrame(idx)
	return func() { rtx.setFrame(prev) }
}

// discoverPlugins lists the `<prefix>*` executables found next to the host binary, in
// pluginPath, and on PATH — each under its post-prefix name, at the first path it was found —
// deduped and sorted by name. It also returns any errors scanning pluginPath — the
// author-configured location, where a failure is a real misconfiguration; failures scanning the
// incidental locations are ignored as normal.
func discoverPlugins(d *PluginDiscoveryDef, pluginPath string, view *osView) ([]DiscoveredPlugin, []error) {
	if d.Prefix == "" {
		return nil, nil
	}
	seen := map[string]bool{}
	var out []DiscoveredPlugin
	var problems []error
	// On Windows a plugin is host-foo.exe (or another PATHEXT extension), offered as "foo";
	// a file without one cannot be run, so it is not a plugin.
	exts := view.executableExts()
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
	for _, dir := range filepath.SplitList(view.getenv("PATH")) {
		if dir != "" {
			scan(view.abs(dir), false)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, problems
}

// filterPrefix keeps the candidates whose name starts with prefix, dropping empty names and
// duplicate names (the first one stays), in their given order.
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

// completionDirectivePrefix marks the directive lines in __complete's output. It is a word no
// plausible candidate begins with, since a bare colon could start a value. A generated script
// and a binary from different rotini versions still work together: a script ignores lines it
// doesn't know, and the kind line stays the last line.
const completionDirectivePrefix = ":rotini:"

// completeKindCommand is the [Completion] kind for command paths below the root.
const completeKindCommand = "command"

// completionHint returns the directive line for the word being completed, or "" when the input
// declares no hint and the shell should apply its own default.
//
//	:rotini:file            complete file paths
//	:rotini:file yaml yml   complete file paths with these extensions
//	:rotini:directory       complete directories only
//	:rotini:none            complete NOTHING — suppress the shell's file fallback
//	:rotini:executable      the shell's own program names
//	:rotini:user            the shell's own user names
//	:rotini:group           the shell's own group names
//	:rotini:host            the shell's own host names
//
// Kind "command" goes out as none: rotini offers the command names itself.
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
	if bashGlue(words) {
		words = append(slices.Clone(words), "")
	}
	partial := words[len(words)-1]

	cc := walkContext(def, words[:len(words)-1])
	if cc.plugin {
		return Completion{} // the plugin binary owns its own argument surface
	}
	if cc.shortCircuit {
		return Completion{Kind: "none"} // offer nothing, not even the shell's file fallback
	}
	cur := cc.chain[len(cc.chain)-1]
	if cur.Passthrough {
		return Completion{}
	}
	if cc.afterTerminator || cc.operands {
		// Flags have stopped: the word is an argument, whatever it looks like.
		if ad, ok := positionalAt(cur.Arguments, cc.positionals); ok {
			return ad.Complete
		}
		return Completion{}
	}

	// "--flag <TAB>": the word is the preceding flag's value.
	if p := cc.pending; p != nil {
		switch {
		case p.mapValue, isMapType(p.fd.Type) && strings.Contains(partial, "="):
			return mapValueHint(p.fd)
		case takesValue(p.fd):
			return p.fd.Complete
		}
		return Completion{}
	}

	// "--flag=<TAB>": the word carries its own flag.
	if strings.HasPrefix(partial, "-") {
		if name, val, hasInline := splitFlag(partial); hasInline {
			if fd, _, negated, found := findFlagMatch(cc.chain, name); found && !negated && takesValue(fd) {
				if isMapType(fd.Type) && strings.Contains(val, "=") {
					return mapValueHint(fd)
				}
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

// positionalAt returns the argument the next positional binds to, a variadic absorbing
// everything past its start: how many words follow is unknown while completing, so the fixed
// arguments after a variadic are never the target.
func positionalAt(args []ArgDef, idx int) (ArgDef, bool) {
	if v := variadicIndex(args); v >= 0 && idx >= v {
		return args[v], true
	}
	if idx < len(args) {
		return args[idx], true
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
	case "directory", "none", "executable", "user", "group", "host":
		return completionDirectivePrefix + c.Kind
	case completeKindCommand:
		return completionDirectivePrefix + "none"
	}
	return ""
}

// CompletionResult is one completion answer, before any wire format: what a program offers for
// the word being completed. It is what a [CompletionFormat] renders.
type CompletionResult struct {
	// Candidates are the offered values, in order — sub-commands, flags, enum values, or what a
	// handler's [FlagValueCompleter] or [ArgValueCompleter] returned — already filtered by the
	// typed prefix, with hidden inputs left out. Descriptions are empty when they are switched
	// off ([Program.WithCompletionDescriptions]).
	Candidates []CompletionCandidate
	// Hint is the spec's declared `complete:` hint for the input being completed, or the zero
	// value when it declares none. A completer that answers still wins over it: the hint is the
	// fallback for when Candidates is empty, except kind "none", which also means "never offer
	// files" when there are candidates. Kind "command" is answered in Candidates, and means
	// "none" to the shell.
	Hint Completion
	// Messages are lines for the shell to show, not offer: what completers added with
	// [Context.AddCompletionMessage], in order, or else, when there are no candidates, the
	// input's static message ([Completion.Message]). Empty when completion messages are off or
	// switched off at run time.
	Messages []string
	// NoSpace asks the shell not to add a space after the inserted candidate: every candidate
	// ends in "=" (a map key, whose value comes next), or a completer asked for it with
	// [Context.SetCompletionOptions].
	NoSpace bool
	// KeepOrder asks the shell to show the candidates in the order given, not sorted: a
	// completer's own order, an enum's declared order, or unset required flags first.
	KeepOrder bool
}

// CompletionCandidate is one offered value and its optional one-line description.
type CompletionCandidate struct {
	Value       string
	Description string
}

// CompletionFormat writes a [CompletionResult] to w in one completion protocol, the wire shape
// a completing host expects. Rotini computes the answer; the format only encodes it.
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
//		code, _ := cmd.NewProgram(cmd.Handlers()).Complete(os.Args[1:], rotini.PluginCompletion)
//		os.Exit(code)
//	}
//
// words are the words after the program's own name; the last is the word being completed,
// empty when the cursor starts a new one, and no words at all completes a new first word. The
// answer is what __complete computes, written to the program's stdout by format. A nil format
// uses rotini's own format, the __complete default, which is private to rotini's generated
// scripts.
//
// Completers receive the program's base context ([Program.WithContext]) through
// [Context.Context]. Rotini sets no deadline and traps no signal for a completion request.
//
// The exit code is 0, or 1 when the format fails to write, with its error.
func (p *Program) Complete(words []string, format CompletionFormat) (int, error) {
	ctx := p.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return p.complete(ctx, words, format)
}

// complete is [Program.Complete] under ctx, which completers read from [Context.Context].
func (p *Program) complete(ctx context.Context, words []string, format CompletionFormat) (int, error) {
	if format == nil {
		format = rotiniCompletion
	}
	if len(words) == 0 {
		words = []string{""}
	}
	rtx := p.newRunContext()
	rtx.ctx = ctx
	if rf := p.def.ResponseFiles; rf != nil {
		var fileWord bool
		if words, fileWord = completionWords(words, rf.Prefix, rtx.view); fileWord {
			// A response file's name is being typed: nothing to offer but the shell's files.
			if err := format(p.stdout, CompletionResult{}); err != nil {
				return 1, err
			}
			return 0, nil
		}
	}
	var added []string
	if p.def.CompletionMessages != nil {
		rtx.completionMessages = &added
	}
	var asked CompletionOptions
	rtx.completionOptions = &asked

	answer := completeAnswer(p.def, words, p.lookup, rtx)
	result := CompletionResult{Hint: completionHintFor(p.def, words)}
	describe := p.completionDescriptionsOn(rtx)
	allKeys := len(answer.cands) > 0
	for _, c := range answer.cands {
		value, desc, _ := strings.Cut(c, "\t")
		if !describe {
			desc = ""
		}
		allKeys = allKeys && strings.HasSuffix(value, "=")
		result.Candidates = append(result.Candidates, CompletionCandidate{Value: value, Description: desc})
	}
	rtx.mu.RLock()
	result.NoSpace = asked.NoSpace || allKeys
	result.KeepOrder = asked.KeepOrder || answer.keepOrder
	rtx.mu.RUnlock()
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
	return !switchedOff(rtx, p.def.CompletionMessages.Env)
}

// completionDescriptionsOn reports whether candidates carry their descriptions in this
// request: the program's own rule when it set one with [Program.WithCompletionDescriptions],
// else the declared environment variable, which hides them when set to 0, false or off. With
// neither, they show.
func (p *Program) completionDescriptionsOn(rtx *Context) bool {
	if p.completionDescriptions != nil {
		return p.completionDescriptions(rtx)
	}
	return p.def.CompletionDescriptions == nil || !switchedOff(rtx, p.def.CompletionDescriptions.Env)
}

// switchedOff reports whether the environment variable env, when named, is set to 0, false or
// off, in any case.
func switchedOff(rtx *Context, env string) bool {
	if env == "" {
		return false
	}
	value, _ := rtx.LookupEnv(env)
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "0", "false", "off":
		return true
	}
	return false
}

// WithCompletionMessages sets the program's own rule for whether completion messages show,
// replacing the check of the environment variable the conf's `messages_env` names. It is
// asked once per completion request, before anything is written, and applies to every shell
// and to [PluginCompletion]:
//
//	cmd.NewProgram(cmd.Handlers()).WithCompletionMessages(func(rtx *rotini.Context) bool {
//		return !settings.Quiet
//	}).Execute()
//
// It has no effect unless the conf turns completion messages on. A nil fn restores the
// environment variable check.
func (p *Program) WithCompletionMessages(fn func(rtx *Context) bool) *Program {
	p.completionMessages = fn
	return p
}

// WithCompletionDescriptions sets the program's own rule for whether completion candidates
// carry their descriptions, replacing the check of the environment variable the conf's
// `descriptions_env` names. It is asked once per completion request, before anything is
// written, and applies to every shell and to [PluginCompletion]:
//
//	cmd.NewProgram(cmd.Handlers()).WithCompletionDescriptions(func(rtx *rotini.Context) bool {
//		return !settings.Plain
//	}).Execute()
//
// Descriptions show by default. A nil fn restores the environment variable check.
func (p *Program) WithCompletionDescriptions(fn func(rtx *Context) bool) *Program {
	p.completionDescriptions = fn
	return p
}

// completionMessagePrefix marks a message line in rotini's own format.
const completionMessagePrefix = completionDirectivePrefix + "message "

// completionOptionPrefix marks the options line in rotini's own format.
const completionOptionPrefix = completionDirectivePrefix + "option "

// rotiniCompletion is rotini's own format: what the hidden __complete prints by default, and
// what the generated shell scripts read. One candidate per line ("value\tdescription"), then a
// ":rotini:message <text>" line per message, then a ":rotini:option <word>…" line when the
// answer has options (nospace, keep-order), then the hint's ":rotini:" kind line when the input
// declares one. The kind line is always last, so a script that doesn't know an option line
// still reads the kind.
func rotiniCompletion(w io.Writer, result CompletionResult) error {
	lines := candidateLines(result.Candidates)
	for _, m := range result.Messages {
		lines = append(lines, completionMessagePrefix+m)
	}
	var opts []string
	if result.NoSpace {
		opts = append(opts, "nospace")
	}
	if result.KeepOrder {
		opts = append(opts, "keep-order")
	}
	if len(opts) > 0 {
		lines = append(lines, completionOptionPrefix+strings.Join(opts, " "))
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
	pluginDirectiveNoSpace       = 2  // add no space after the inserted candidate
	pluginDirectiveNoFileComp    = 4  // offer no file names
	pluginDirectiveFilterFileExt = 8  // complete file names with these extensions
	pluginDirectiveFilterDirs    = 16 // complete directory names only
	pluginDirectiveKeepOrder     = 32 // show the candidates in the order given
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
// arguments, so a file or directory hint applies only when there are no candidates. Kinds the
// hosts have no completer for (command, executable, user, group and host) map to 4: the hosts
// offer nothing for them beyond the candidates. NoSpace adds 2 and KeepOrder adds 32. The
// directive line is always written, since the hosts read the last line as the directive.
//
// Each message is written after the regular candidates as a candidate line carrying the hosts'
// message marker, which the hosts' completion scripts show as a message where the shell can.
func PluginCompletion(w io.Writer, result CompletionResult) error {
	lines := candidateLines(result.Candidates)

	// Messages are not candidates, so they don't stand in the way of a file or directory hint.
	directive := pluginDirectiveDefault
	switch hint := result.Hint; {
	case hint.Kind != "" && hint.Kind != "file" && hint.Kind != "directory":
		directive = pluginDirectiveNoFileComp
	case len(lines) > 0:
	case hint.Kind == "directory":
		directive = pluginDirectiveFilterDirs
	case hint.Kind == "file" && len(hint.Extensions) > 0:
		directive = pluginDirectiveFilterFileExt
		lines = append(lines, hint.Extensions...)
	}
	if result.NoSpace {
		directive |= pluginDirectiveNoSpace
	}
	if result.KeepOrder {
		directive |= pluginDirectiveKeepOrder
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
//	cmd.NewProgram(cmd.Handlers()).WithCompletion(rotini.PluginCompletion).Execute()
//
// Rotini's generated completion scripts read rotini's own format, so a standalone CLI leaves
// this unset. A host that runs a separately named completer without a __complete word, such
// as kubectl's kubectl_complete-<name>, is served by calling [Program.Complete] from main.
//
// A nil format restores rotini's own.
func (p *Program) WithCompletion(format CompletionFormat) *Program {
	p.completion = format
	return p
}

// bashGlue reports whether the word being completed is a bare "=": bash's word break when the
// cursor sits right after "--flag=" or a map entry's "key=". The value starts after it, so the
// hint applies to an empty value, and no candidate can be offered without replacing the "=".
func bashGlue(words []string) bool {
	return len(words) > 1 && words[len(words)-1] == "="
}

// mapValueHint is the hint for the value after a map entry's "key=": nothing to offer, and no
// files unless the flag's own hint asks for them.
func mapValueHint(fd FlagDef) Completion {
	h := fd.Complete
	if h.Kind == "" {
		h.Kind = "none"
	}
	return h
}
