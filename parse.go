package rotini

import (
	"fmt"
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
}

func rootFrame(def Definition) frame {
	return frame{
		name: def.Name, handler: def.Handler, summary: def.Summary, description: def.Description,
		flags: def.Flags, arguments: def.Arguments, commands: def.Commands,
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

// parseResult is the outcome of resolving argv against a [Definition].
type parseResult struct {
	chain  []frame // root → leaf
	parsed *parsedInputs
	help   bool // built-in help was requested (-h/--help)
}

// parse resolves argv against def: it walks the command tree by name/alias,
// matches flags by identifier against the resolved chain (leaf→root), and
// collects positional arguments for the leaf command. The parsed inputs are
// keyed by command name so [Inputs] can bind them — including for a statically
// composed child, whose scope tags are relative to its own root.
func parse(def Definition, argv []string) (*parseResult, error) {
	chain := []frame{rootFrame(def)}
	store := &parsedInputs{scopes: map[string]scopeInputs{}}
	help := false
	startedArgs := false

	addFlag := func(scope, name, value string) {
		si := store.scopes[scope]
		if si.flags == nil {
			si.flags = map[string][]string{}
		}
		si.flags[name] = append(si.flags[name], value)
		store.scopes[scope] = si
	}
	addArg := func(scope, value string) {
		si := store.scopes[scope]
		si.args = append(si.args, value)
		store.scopes[scope] = si
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
				if name == "-h" || name == "--help" {
					help = true
					continue
				}
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

		// A non-flag token is a sub-command (until positional args begin) or an
		// argument of the leaf command.
		if !startedArgs {
			cur := chain[len(chain)-1]
			if child, ok := findChild(cur, tok); ok {
				chain = append(chain, cmdFrame(child))
				continue
			}
			// Not a sub-command. If this command branches but takes no
			// arguments, the token is a mistyped command, not an argument.
			if len(cur.commands) > 0 && len(cur.arguments) == 0 {
				return nil, &usageError{msg: unknownCommandMsg(cur, tok)}
			}
		}
		startedArgs = true
		addArg(chain[len(chain)-1].name, tok)
	}

	applyDefaults(chain, store)
	return &parseResult{chain: chain, parsed: store, help: help}, nil
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
