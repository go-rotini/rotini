package rotini

import (
	"fmt"
	"slices"
	"strings"
)

// frame is one resolved command node on the invocation path: the root, then
// each descended sub-command.
type frame struct {
	name        string
	handler     string
	summary     string
	description string
	flags       []FlagDef
	arguments   []ArgDef
	commands    []CommandDef
	remotes     []RemoteDef
}

func rootFrame(def Definition) frame {
	return frame{
		name: def.Name, handler: def.Handler, summary: def.Summary, description: def.Description,
		flags: def.Flags, arguments: def.Arguments, commands: def.Commands, remotes: def.RemoteCommands,
	}
}

func cmdFrame(c CommandDef) frame {
	return frame{
		name: c.Name, handler: c.Handler, summary: c.Summary, description: c.Description,
		flags: c.Flags, arguments: c.Arguments, commands: c.Commands,
	}
}

// usageError is a parse-time failure caused by bad input; it maps to exit
// code 2 (the conventional CLI usage-error code).
type usageError struct{ msg string }

func (e *usageError) Error() string { return e.msg }

// Parser turns a resolved command's raw argument vector into a name-scoped store
// of parsed values: it binds flags/arguments against the command's declared defs,
// applies defaults, and validates (required, enum). The runtime auto-binds a
// default *Parser under the "parser" service key, which [Parse] retrieves before
// reflectively binding the result into the caller's typed inputs struct. A CLI
// can swap it via Program.Bind("parser", …); a handler that wants a different
// flag syntax entirely can ignore [Parse] and read [Rtx.Args] instead.
type Parser struct{}

// parse binds argv to the resolved chain and validates it, returning the parsed
// store or the first usage error.
func (*Parser) parse(chain []frame, argv []string) (*parsedInputs, error) {
	store, err := parseInto(chain, argv)
	if err != nil {
		return nil, err
	}
	if err := validate(chain, store); err != nil {
		return nil, err
	}
	return store, nil
}

// resolveChain walks argv against def to find the invoked command path without
// validating inputs. It descends sub-commands by name/alias, skips flags (and a
// flag's separate value, so it is never mistaken for a command), and stops at the
// first positional argument. If a token names a remote/co-located command it
// returns the chain so far plus a non-nil remoteDispatch the runtime should exec
// instead of dispatching the chain.
//
// Resolution is intentionally lenient — unknown flags, missing values, and bad
// input are not errors here. Parsing and validation are opt-in, performed by the
// handler via [Parse].
func resolveChain(def Definition, argv []string) ([]frame, *remoteDispatch) {
	chain := []frame{rootFrame(def)}
	for i := 0; i < len(argv); i++ {
		tok := argv[i]
		if tok == "--" {
			break // the rest are positional; no further command descent
		}
		if isFlag(tok) {
			name, _, hasInline := splitFlag(tok)
			// Skip a separate value token so it is not mistaken for a command.
			if fd, _, ok := findFlag(chain, name); ok && fd.Type != "bool" && !hasInline {
				i++
			}
			continue
		}
		cur := chain[len(chain)-1]
		if child, ok := findChild(cur, tok); ok {
			chain = append(chain, cmdFrame(child))
			continue
		}
		if rd, ok := findRemote(cur, tok); ok {
			return chain, &remoteDispatch{def: rd, args: append([]string{}, argv[i+1:]...)}
		}
		break // first positional argument; stop descending
	}
	return chain, nil
}

// parseInto binds argv to an already-resolved chain, strictly: an unrecognized
// flag, or a flag missing its value, is a usageError. Command tokens already in
// the chain are consumed; everything after the leaf command (and after "--") is a
// positional argument of the leaf. Declared defaults are applied. parseInto does
// not check required inputs or enums — that is [validate]'s job — so a handler
// can inspect what was supplied before deciding how strict to be.
func parseInto(chain []frame, argv []string) (*parsedInputs, error) {
	store := &parsedInputs{scopes: map[string]scopeInputs{}}
	leaf := chain[len(chain)-1].name
	depth := 1 // index of the next chain frame we might descend into
	startedArgs := false

	addFlag := func(scope, name, value string) {
		si := store.scopes[scope]
		if si.flags == nil {
			si.flags = map[string][]string{}
		}
		si.flags[name] = append(si.flags[name], value)
		store.scopes[scope] = si
	}
	addArg := func(value string) {
		si := store.scopes[leaf]
		si.args = append(si.args, value)
		store.scopes[leaf] = si
	}

	for i := 0; i < len(argv); i++ {
		tok := argv[i]

		if tok == "--" { // explicit end of flags; the rest are positional
			startedArgs = true
			continue
		}

		if isFlag(tok) {
			name, inline, hasInline := splitFlag(tok)
			fdef, scope, ok := findFlag(chain, name)
			if !ok {
				return nil, &usageError{msg: fmt.Sprintf("unknown flag %q", name)}
			}
			var value string
			switch {
			case fdef.Type == "bool":
				value = "true"
				if hasInline {
					value = inline
				}
			case hasInline:
				value = inline
			default:
				i++
				if i >= len(argv) {
					return nil, &usageError{msg: fmt.Sprintf("flag %q needs a value", name)}
				}
				value = argv[i]
			}
			addFlag(scope, fdef.Name, value)
			continue
		}

		// A non-flag token: consume it as the next command in the chain if it is
		// one, otherwise it (and everything after) is a positional of the leaf.
		if !startedArgs && depth < len(chain) {
			if c, ok := findChild(chain[depth-1], tok); ok && c.Name == chain[depth].name {
				depth++
				continue
			}
		}
		startedArgs = true
		addArg(tok)
	}

	applyDefaults(chain, store)
	return store, nil
}

// validate enforces the declarative constraints on the resolved chain against a
// parsed store: a stray positional on a branch-only command is a mistyped
// sub-command; required flags/arguments must be present (or defaulted); and any
// value for a flag or argument that declares an Enum must be one of its members.
// It is the strict half of [Parse]; a handler that wants laxer behavior can bind
// inputs without it.
func validate(chain []frame, store *parsedInputs) error {
	leaf := chain[len(chain)-1]
	si := store.scopes[leaf.name]

	// A stray positional on a command that branches but takes no arguments is a
	// mistyped sub-command, not an argument.
	if len(leaf.commands) > 0 && len(leaf.arguments) == 0 && len(si.args) > 0 {
		return &usageError{msg: unknownCommandMsg(leaf, si.args[0])}
	}

	if err := requiredErrors(chain, store); err != nil {
		return err
	}

	for _, f := range chain {
		fsi := store.scopes[f.name]
		for _, fd := range f.flags {
			if len(fd.Enum) == 0 {
				continue
			}
			for _, v := range fsi.flags[fd.Name] {
				if !slices.Contains(fd.Enum, v) {
					return &usageError{msg: fmt.Sprintf("invalid value %q for %s (one of: %s)", v, flagLabel(fd), strings.Join(fd.Enum, ", "))}
				}
			}
		}
	}

	for i, ad := range leaf.arguments {
		if len(ad.Enum) == 0 || i >= len(si.args) {
			continue
		}
		vals := si.args[i : i+1]
		if ad.Variadic {
			vals = si.args[i:]
		}
		for _, v := range vals {
			if !slices.Contains(ad.Enum, v) {
				return &usageError{msg: fmt.Sprintf("invalid value %q for <%s> (one of: %s)", v, ad.Name, strings.Join(ad.Enum, ", "))}
			}
		}
	}
	return nil
}

// findRemote returns the remote sub-command of f matching tok by name or alias.
func findRemote(f frame, tok string) (RemoteDef, bool) {
	for _, r := range f.remotes {
		if r.Name == tok {
			return r, true
		}
		for _, a := range r.Aliases {
			if a == tok {
				return r, true
			}
		}
	}
	return RemoteDef{}, false
}

// applyDefaults fills in declared flag and trailing-argument defaults for inputs
// the user did not provide, so handlers and required-checks see them.
func applyDefaults(chain []frame, store *parsedInputs) {
	for _, f := range chain {
		var si scopeInputs
		touched := false
		for _, fd := range f.flags {
			if fd.Default == "" {
				continue
			}
			if !touched {
				si = store.scopes[f.name]
				if si.flags == nil {
					si.flags = map[string][]string{}
				}
				touched = true
			}
			if _, ok := si.flags[fd.Name]; !ok {
				si.flags[fd.Name] = []string{fd.Default}
			}
		}
		if touched {
			store.scopes[f.name] = si
		}
	}

	leaf := chain[len(chain)-1]
	si := store.scopes[leaf.name]
	changed := false
	for i := len(si.args); i < len(leaf.arguments); i++ {
		if leaf.arguments[i].Default == "" {
			break // can't fill a gap before a defaultless argument
		}
		si.args = append(si.args, leaf.arguments[i].Default)
		changed = true
	}
	if changed {
		store.scopes[leaf.name] = si
	}
}

// requiredErrors reports any required flags or arguments (across the resolved
// chain / on the leaf) that were neither provided nor defaulted.
func requiredErrors(chain []frame, store *parsedInputs) error {
	var missing []string
	for _, f := range chain {
		si := store.scopes[f.name]
		for _, fd := range f.flags {
			if fd.Required {
				if _, ok := si.flags[fd.Name]; !ok {
					missing = append(missing, flagLabel(fd))
				}
			}
		}
	}
	leaf := chain[len(chain)-1]
	si := store.scopes[leaf.name]
	for i, ad := range leaf.arguments {
		if ad.Required && i >= len(si.args) {
			missing = append(missing, "<"+ad.Name+">")
		}
	}
	if len(missing) > 0 {
		return &usageError{msg: "missing required " + plural("input", len(missing)) + ": " + strings.Join(missing, ", ")}
	}
	return nil
}

func flagLabel(f FlagDef) string {
	if len(f.Identifiers) > 0 {
		return f.Identifiers[0]
	}
	return "--" + f.Name
}

func plural(word string, n int) string {
	if n == 1 {
		return word
	}
	return word + "s"
}

// isFlag reports whether tok is a flag token (e.g. "-h", "--watch",
// "--config=x"). Bare "-" and "--" are not flags. (Negative-number arguments
// and clustered short flags like "-abc" are not yet supported.)
func isFlag(tok string) bool {
	return len(tok) > 1 && tok[0] == '-' && tok != "--"
}

// splitFlag splits a flag token into its identifier and an inline "=value".
func splitFlag(tok string) (name, value string, hasValue bool) {
	if eq := strings.IndexByte(tok, '='); eq >= 0 {
		return tok[:eq], tok[eq+1:], true
	}
	return tok, "", false
}

// findFlag searches the resolved chain leaf→root for a flag whose identifiers
// include name, returning its definition and the owning command-name scope.
func findFlag(chain []frame, name string) (FlagDef, string, bool) {
	for i := len(chain) - 1; i >= 0; i-- {
		for _, f := range chain[i].flags {
			for _, id := range f.Identifiers {
				if id == name {
					return f, chain[i].name, true
				}
			}
		}
	}
	return FlagDef{}, "", false
}

// findChild returns the sub-command of f matching tok by name or alias.
func findChild(f frame, tok string) (CommandDef, bool) {
	for _, c := range f.commands {
		if c.Name == tok {
			return c, true
		}
		for _, a := range c.Aliases {
			if a == tok {
				return c, true
			}
		}
	}
	return CommandDef{}, false
}

// unknownCommandMsg builds the error for a mistyped sub-command, appending a
// "did you mean" suggestion when a close match exists.
func unknownCommandMsg(cur frame, tok string) string {
	msg := fmt.Sprintf("unknown command %q for %q", tok, cur.name)
	if s := suggest(cur.commands, tok); s != "" {
		msg += fmt.Sprintf("\n\nDid you mean %q?", s)
	}
	return msg
}

// suggest returns the closest command name to tok within edit distance 2.
func suggest(candidates []CommandDef, tok string) string {
	best, bestDist := "", 3
	for _, c := range candidates {
		if d := levenshtein(tok, c.Name); d < bestDist {
			best, bestDist = c.Name, d
		}
	}
	return best
}

func levenshtein(a, b string) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(b)]
}
