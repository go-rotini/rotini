package rotini

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// Command resolution: turning an argv into the chain of commands it names, plus the flag-token
// helpers reading argv requires. Resolution is required — dispatch cannot pick a handler
// without it — where parser.go's [Parser] is the opt-in service that parses and validates a
// command's declared inputs once dispatch has chosen it.

// ResolvedCommand is one node on the invoked command path, root → leaf: the flattened
// command-tree data the runtime resolved for this invocation. It is exposed via
// [Context.Chain] so opt-in tooling binds inputs against the exact command whose handler ran —
// including a statically composed child, whose chain is relative to its own root.
//
// Fields copy the matching [Definition] (root) or [CommandDef] fields; an empty slice means the
// command declares none of that kind.
type ResolvedCommand struct {
	Name                  string
	Handler               string   // ProgramHandlers method for this command; see [CommandDef.Handler]
	Matched               string   // the argv token that resolved this command (name or an alias); "" for the root
	DeprecatedIdentifiers []string // aliases of this command that are deprecated
	Deprecated            string   // the command's deprecation message, when it is deprecated as a whole
	Flags                 []FlagDef
	Arguments             []ArgDef
	FlagGroups            []FlagGroup
	FlagDependencies      []FlagDependency
	Commands              []CommandDef        // sub-commands; empty for a leaf
	Remotes               []RemoteDef         // co-located plugin binaries dispatched as sub-commands
	Discovery             *RemoteDiscoveryDef // plugin auto-discovery (nil = off)
	// PluginPath is the extra directory this command's plugin binaries may live in, searched
	// for BOTH declared remotes and discovered plugins — they are the same binaries in the
	// same place. Empty means only the host binary's directory and PATH are searched. It is the
	// directory as searched: a leading ~ and $VAR references in the declared path are already
	// expanded.
	PluginPath  string
	Passthrough bool // every token after this command is a raw positional (no flag parsing)
}

// expandPluginPath expands a leading ~ to the user's home directory and $VAR / ${VAR}
// references from the environment, the way a shell would — so `plugin_path: ~/.app/plugins`
// means what it says, as a configuration file's path does. When the home directory cannot be
// found the ~ is left in place, and the search simply finds nothing there.
func expandPluginPath(dir string) string {
	if dir == "" {
		return ""
	}
	dir = os.ExpandEnv(dir)
	if dir == "~" || strings.HasPrefix(dir, "~/") || strings.HasPrefix(dir, "~"+string(filepath.Separator)) {
		if home, err := os.UserHomeDir(); err == nil {
			dir = filepath.Join(home, dir[1:])
		}
	}
	return dir
}

// rootFrame is the chain frame for the program's root command.
func rootFrame(def Definition) ResolvedCommand {
	return ResolvedCommand{
		Name: def.Name, Handler: def.Handler,
		Flags: def.Flags, Arguments: def.Arguments,
		FlagGroups: def.FlagGroups, FlagDependencies: def.FlagDependencies,
		Commands: def.Commands, Remotes: def.RemoteCommands, Discovery: def.Discovery,
		PluginPath: expandPluginPath(def.PluginPath), Passthrough: def.Passthrough,
	}
}

// cmdFrame is the chain frame for sub-command c; the caller sets Matched.
func cmdFrame(c CommandDef) ResolvedCommand {
	return ResolvedCommand{
		Name: c.Name, Handler: c.Handler, DeprecatedIdentifiers: c.DeprecatedIdentifiers, Deprecated: c.Deprecated,
		Flags: c.Flags, Arguments: c.Arguments,
		FlagGroups: c.FlagGroups, FlagDependencies: c.FlagDependencies,
		Commands: c.Commands, Remotes: c.Remotes, Discovery: c.Discovery,
		PluginPath: expandPluginPath(c.PluginPath), Passthrough: c.Passthrough,
	}
}

// resolveChain walks argv against def to find the invoked command path without validating
// inputs: descend sub-commands by name or alias, skip flags and their separate values, and
// stop at the first positional. A token naming a remote command returns the chain so far plus
// a non-nil [RemoteDispatch] to exec instead.
//
// It is deliberately lenient: unknown flags, missing values and bad input are not errors here.
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
			// Skip a separate value word so it is not mistaken for a command.
			i += flagTokenWidth(chain, argv, i)
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
			// The plugin path applies to a DECLARED remote too: an author who says where
			// this command's plugins live means it for all of them.
			return chain, &RemoteDispatch{Def: rd, Args: append([]string{}, argv[i+1:]...), Dir: cur.PluginPath}
		}
		// Plugin discovery: at a discovery-enabled command, an unmatched token is
		// dispatched to the sibling executable <prefix><token> (kubectl-plugin style).
		// The binary is resolved (and any error reported) at exec time.
		if d := cur.Discovery; d != nil {
			rd := RemoteDef{Name: tok, Binary: d.Prefix + tok}
			return chain, &RemoteDispatch{Def: rd, Args: append([]string{}, argv[i+1:]...), Dir: cur.PluginPath, Discovered: true}
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

// isFlag reports whether tok is a flag token. Bare "-" and "--" are not, and neither is a
// token that parses as a number ("-5", "-1e3"): flag identifiers always have a letter after
// the dashes, so a negative number is handled as a positional.
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
