package rotini

import "strings"

// ResolvedCommand is one node on the invoked command path (root → leaf): the
// flattened command-tree data the runtime resolved for this invocation. The
// runtime computes the chain in order to dispatch the correct handler, and
// exposes it via [Rtx.Chain] so opt-in tooling (the rtk parser) binds inputs
// against the exact command whose handler ran — including for a statically
// composed child, whose chain is relative to its own root.
type ResolvedCommand struct {
	Name      string
	Handler   string
	Flags     []FlagDef
	Arguments []ArgDef
	Commands  []CommandDef
	Remotes   []RemoteDef
	Discovery *RemoteDiscoveryDef
}

func rootFrame(def Definition) ResolvedCommand {
	return ResolvedCommand{
		Name: def.Name, Handler: def.Handler,
		Flags: def.Flags, Arguments: def.Arguments, Commands: def.Commands,
		Remotes: def.RemoteCommands, Discovery: def.Discovery,
	}
}

func cmdFrame(c CommandDef) ResolvedCommand {
	return ResolvedCommand{
		Name: c.Name, Handler: c.Handler,
		Flags: c.Flags, Arguments: c.Arguments, Commands: c.Commands, Discovery: c.Discovery,
	}
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
// handler via the rtk package's Parse.
func resolveChain(def Definition, argv []string) ([]ResolvedCommand, *remoteDispatch) {
	chain := []ResolvedCommand{rootFrame(def)}
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
		// Plugin discovery: at a discovery-enabled command, an unmatched token is
		// dispatched to the sibling executable <prefix><token> (kubectl-plugin style).
		// The binary is resolved (and any error reported) at exec time.
		if d := cur.Discovery; d != nil {
			rd := RemoteDef{Name: tok, Binary: d.Prefix + tok}
			return chain, &remoteDispatch{def: rd, args: append([]string{}, argv[i+1:]...), dir: d.Path}
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
		for _, a := range r.Aliases {
			if a == tok {
				return r, true
			}
		}
	}
	return RemoteDef{}, false
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
func findFlag(chain []ResolvedCommand, name string) (FlagDef, string, bool) {
	for i := len(chain) - 1; i >= 0; i-- {
		for _, f := range chain[i].Flags {
			for _, id := range f.Identifiers {
				if id == name {
					return f, chain[i].Name, true
				}
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
		for _, a := range c.Aliases {
			if a == tok {
				return c, true
			}
		}
	}
	return CommandDef{}, false
}
