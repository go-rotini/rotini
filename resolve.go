package rotini

import (
	"slices"
	"strconv"
	"strings"
)

// Command RESOLUTION: turning an argv into the chain of commands it names, plus the
// flag-token helpers that reading argv requires.
//
// Distinct from parser.go, and the distinction is the point: resolution is REQUIRED —
// dispatch cannot pick a handler without it — while parser.go's [Parser] is an opt-in
// service that parses and validates a command's declared inputs once dispatch has
// already chosen it.

// ResolvedCommand is one node on the invoked command path (root → leaf): the
// flattened command-tree data the runtime resolved for this invocation. The
// runtime computes the chain in order to dispatch the correct handler, and
// exposes it via [Rtx.Chain] so opt-in tooling (the [Parser]) binds inputs
// against the exact command whose handler ran — including for a statically
// composed child, whose chain is relative to its own root.
type ResolvedCommand struct {
	Name                  string
	Handler               string
	Matched               string   // the argv token that resolved this command (name or an alias); "" for the root
	DeprecatedIdentifiers []string // aliases of this command that are deprecated
	Flags                 []FlagDef
	Arguments             []ArgDef
	FlagGroups            []FlagGroup
	FlagDependencies      []FlagDependency
	Commands              []CommandDef
	Remotes               []RemoteDef
	Discovery             *RemoteDiscoveryDef
	Passthrough           bool
}

func rootFrame(def Definition) ResolvedCommand {
	return ResolvedCommand{
		Name: def.Name, Handler: def.Handler,
		Flags: def.Flags, Arguments: def.Arguments,
		FlagGroups: def.FlagGroups, FlagDependencies: def.FlagDependencies,
		Commands: def.Commands, Remotes: def.RemoteCommands, Discovery: def.Discovery,
		Passthrough: def.Passthrough,
	}
}

func cmdFrame(c CommandDef) ResolvedCommand {
	return ResolvedCommand{
		Name: c.Name, Handler: c.Handler, DeprecatedIdentifiers: c.DeprecatedIdentifiers,
		Flags: c.Flags, Arguments: c.Arguments,
		FlagGroups: c.FlagGroups, FlagDependencies: c.FlagDependencies,
		Commands: c.Commands, Remotes: c.Remotes, Discovery: c.Discovery,
		Passthrough: c.Passthrough,
	}
}

// resolveChain walks argv against def to find the invoked command path without
// validating inputs. It descends sub-commands by name/alias, skips flags (and a
// flag's separate value, so it is never mistaken for a command), and stops at the
// first positional argument. If a token names a remote/co-located command it
// returns the chain so far plus a non-nil [RemoteDispatch] the runtime should
// exec instead of dispatching the chain.
//
// Resolution is intentionally lenient — unknown flags, missing values, and bad
// input are not errors here. Parsing and validation are opt-in, performed by the
// handler via [Parser.Parse].
func resolveChain(def Definition, argv []string) ([]ResolvedCommand, *RemoteDispatch) {
	chain := []ResolvedCommand{rootFrame(def)}
	if def.Passthrough {
		return chain, nil // a passthrough root: every token is a positional
	}
	for i := 0; i < len(argv); i++ {
		tok := argv[i]
		if tok == "--" {
			break // the rest are positional; no further command descent
		}
		if isFlag(tok) {
			name, _, hasInline := splitFlag(tok)
			// Skip a separate value token so it is not mistaken for a command.
			if fd, _, ok := findFlag(chain, name); ok && takesValue(fd) && !hasInline {
				i++
			}
			continue
		}
		// A non-flag token that still begins with "-" (a negative-number argument like
		// "-5", or bare "-") is a positional, never a command/remote/plugin name — those
		// begin with a letter. Stop descending so it is not mis-dispatched.
		if strings.HasPrefix(tok, "-") {
			break
		}
		cur := chain[len(chain)-1]
		if child, ok := findChild(cur, tok); ok {
			frame := cmdFrame(child)
			frame.Matched = tok // record the token used (name or alias) for deprecation detection
			chain = append(chain, frame)
			if frame.Passthrough {
				break // raw tokens from here on — no further descent, no flag skipping
			}
			continue
		}
		if rd, ok := findRemote(cur, tok); ok {
			return chain, &RemoteDispatch{Def: rd, Args: append([]string{}, argv[i+1:]...)}
		}
		// Plugin discovery: at a discovery-enabled command, an unmatched token is
		// dispatched to the sibling executable <prefix><token> (kubectl-plugin style).
		// The binary is resolved (and any error reported) at exec time.
		if d := cur.Discovery; d != nil {
			rd := RemoteDef{Name: tok, Binary: d.Prefix + tok}
			return chain, &RemoteDispatch{Def: rd, Args: append([]string{}, argv[i+1:]...), Dir: d.Path, Discovered: true}
		}
		break // first positional argument; stop descending
	}
	return chain, nil
}

// findRemote returns the remote sub-command of f matching tok by name or alias.
func findRemote(f ResolvedCommand, tok string) (RemoteDef, bool) {
	for _, r := range f.Remotes {
		if r.Name == tok {
			return r, true
		}
		if slices.Contains(r.Aliases, tok) {
			return r, true
		}
	}
	return RemoteDef{}, false
}

// isFlag reports whether tok is a flag token (e.g. "-h", "--watch", "--config=x").
// Bare "-" and "--" are not flags. A token that parses as a number (e.g. "-5",
// "-0.5", "-1e3") is a negative-number argument, not a flag — flag identifiers always
// have a letter after the dash(es) — so it is excluded here and handled as a positional.
func isFlag(tok string) bool {
	if len(tok) <= 1 || tok[0] != '-' || tok == "--" {
		return false
	}
	if _, err := strconv.ParseFloat(tok, 64); err == nil {
		return false
	}
	return true
}

// splitFlag splits a flag token into its identifier and an inline "=value".
func splitFlag(tok string) (name, value string, hasValue bool) {
	if before, after, ok := strings.Cut(tok, "="); ok {
		return before, after, true
	}
	return tok, "", false
}

// findFlag searches the resolved chain leaf→root for a flag whose identifiers
// include name, returning its definition and the owning command-name scope.
func findFlag(chain []ResolvedCommand, name string) (FlagDef, string, bool) {
	for _, v := range slices.Backward(chain) {
		for _, f := range v.Flags {
			if slices.Contains(f.Identifiers, name) {
				return f, v.Name, true
			}
		}
	}
	return FlagDef{}, "", false
}

// findChild returns the sub-command of f matching tok by name or alias.
func findChild(f ResolvedCommand, tok string) (CommandDef, bool) {
	for _, c := range f.Commands {
		if c.Name == tok {
			return c, true
		}
		if slices.Contains(c.Aliases, tok) {
			return c, true
		}
	}
	return CommandDef{}, false
}
