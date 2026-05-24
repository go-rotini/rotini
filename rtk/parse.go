// Package rtk is rotini's opt-in toolkit: the rotini-flavored functionality a CLI
// can choose to use on top of the core runtime, but does not have to. Core rotini
// (the github.com/go-rotini/rotini package) guarantees only two things — codegen
// of the handler files, and command resolution + lifecycle dispatch. Everything
// else is opt-in and lives here: parsing the resolved command's arguments into a
// typed inputs struct ([Parse]/[Inputs]), rendering standard help ([Usage]), and
// generating shell completion scripts ([CompletionScript]).
//
// A handler reaches for rtk when it wants rotini's conventions; a handler that
// disagrees ignores rtk entirely and reads the raw argument vector via
// rotini.Context.Args, parsing however it likes.
package rtk

import (
	"fmt"
	"slices"
	"strings"

	"github.com/go-rotini/rotini"
)

// parsedInputs is one invocation's parsed argv, keyed by command-name scope.
// [Parse] builds it via [parseInto] and reads it back through the reflective
// binder. Keying by command name — not the parent-prefixed path — is what lets a
// statically-composed child read its inputs through its own generated types
// unchanged.
type parsedInputs struct {
	scopes map[string]scopeInputs
}

// scopeInputs holds one command scope's parsed values: flag values by logical
// name (a repeatable flag keeps every value) and the positional arguments.
type scopeInputs struct {
	flags map[string][]string
	args  []string
}

// usageError is a parse-time failure caused by bad input; handlers conventionally
// map it to exit code 2 (the usual CLI usage-error code).
type usageError struct{ msg string }

func (e *usageError) Error() string { return e.msg }

// parseInto binds argv to an already-resolved chain, strictly: an unrecognized
// flag, or a flag missing its value, is a usageError. Command tokens already in
// the chain are consumed; everything after the leaf command (and after "--") is a
// positional argument of the leaf. Declared defaults are applied. parseInto does
// not check required inputs or enums — that is [validate]'s job — so a handler can
// inspect what was supplied before deciding how strict to be.
func parseInto(chain []rotini.ResolvedCommand, argv []string) (*parsedInputs, error) {
	store := &parsedInputs{scopes: map[string]scopeInputs{}}
	leaf := chain[len(chain)-1].Name
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
			if c, ok := findChild(chain[depth-1], tok); ok && c.Name == chain[depth].Name {
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
func validate(chain []rotini.ResolvedCommand, store *parsedInputs) error {
	leaf := chain[len(chain)-1]
	si := store.scopes[leaf.Name]

	// A stray positional on a command that branches but takes no arguments is a
	// mistyped sub-command, not an argument.
	if len(leaf.Commands) > 0 && len(leaf.Arguments) == 0 && len(si.args) > 0 {
		return &usageError{msg: unknownCommandMsg(leaf, si.args[0])}
	}

	if err := requiredErrors(chain, store); err != nil {
		return err
	}

	for _, f := range chain {
		fsi := store.scopes[f.Name]
		for _, fd := range f.Flags {
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

	for i, ad := range leaf.Arguments {
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

// applyDefaults fills in declared flag and trailing-argument defaults for inputs
// the user did not provide, so handlers and required-checks see them.
func applyDefaults(chain []rotini.ResolvedCommand, store *parsedInputs) {
	for _, f := range chain {
		var si scopeInputs
		touched := false
		for _, fd := range f.Flags {
			if fd.Default == "" {
				continue
			}
			if !touched {
				si = store.scopes[f.Name]
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
			store.scopes[f.Name] = si
		}
	}

	leaf := chain[len(chain)-1]
	si := store.scopes[leaf.Name]
	changed := false
	for i := len(si.args); i < len(leaf.Arguments); i++ {
		if leaf.Arguments[i].Default == "" {
			break // can't fill a gap before a defaultless argument
		}
		si.args = append(si.args, leaf.Arguments[i].Default)
		changed = true
	}
	if changed {
		store.scopes[leaf.Name] = si
	}
}

// requiredErrors reports any required flags or arguments (across the resolved
// chain / on the leaf) that were neither provided nor defaulted.
func requiredErrors(chain []rotini.ResolvedCommand, store *parsedInputs) error {
	var missing []string
	for _, f := range chain {
		si := store.scopes[f.Name]
		for _, fd := range f.Flags {
			if fd.Required {
				if _, ok := si.flags[fd.Name]; !ok {
					missing = append(missing, flagLabel(fd))
				}
			}
		}
	}
	leaf := chain[len(chain)-1]
	si := store.scopes[leaf.Name]
	for i, ad := range leaf.Arguments {
		if ad.Required && i >= len(si.args) {
			missing = append(missing, "<"+ad.Name+">")
		}
	}
	if len(missing) > 0 {
		return &usageError{msg: "missing required " + plural("input", len(missing)) + ": " + strings.Join(missing, ", ")}
	}
	return nil
}

func flagLabel(f rotini.FlagDef) string {
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
// "--config=x"). Bare "-" and "--" are not flags. It mirrors the core runtime's
// resolver so binding agrees with the command the runtime dispatched.
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
func findFlag(chain []rotini.ResolvedCommand, name string) (rotini.FlagDef, string, bool) {
	for i := len(chain) - 1; i >= 0; i-- {
		for _, f := range chain[i].Flags {
			for _, id := range f.Identifiers {
				if id == name {
					return f, chain[i].Name, true
				}
			}
		}
	}
	return rotini.FlagDef{}, "", false
}

// findChild returns the sub-command of f matching tok by name or alias.
func findChild(f rotini.ResolvedCommand, tok string) (rotini.CommandDef, bool) {
	for _, c := range f.Commands {
		if c.Name == tok {
			return c, true
		}
		for _, a := range c.Aliases {
			if a == tok {
				return c, true
			}
		}
	}
	return rotini.CommandDef{}, false
}

// unknownCommandMsg builds the error for a mistyped sub-command, appending a
// "did you mean" suggestion when a close match exists.
func unknownCommandMsg(cur rotini.ResolvedCommand, tok string) string {
	msg := fmt.Sprintf("unknown command %q for %q", tok, cur.Name)
	if s := suggest(cur.Commands, tok); s != "" {
		msg += fmt.Sprintf("\n\nDid you mean %q?", s)
	}
	return msg
}

// suggest returns the closest command name to tok within edit distance 2.
func suggest(candidates []rotini.CommandDef, tok string) string {
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
