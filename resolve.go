package rotini

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// Command resolution turns argv into the chain of commands it names. It always runs before
// dispatch; parsing a command's declared inputs ([Parser]) is a separate, later step.

// Command is one node on the invoked command path, root → leaf, as resolved for this
// invocation and exposed via [Context.CommandChain]. For a statically composed child, the chain
// is its full path under the parent.
//
// Fields copy the matching [Definition] (root) or [CommandDef] fields; an empty slice means the
// command declares none of that kind.
type Command struct {
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
	Plugins               []PluginDef         // co-located plugin binaries dispatched as sub-commands
	PluginDiscovery       *PluginDiscoveryDef // plugin auto-discovery (nil = off)
	// PluginPath is an extra directory searched for this command's declared and discovered
	// plugin binaries, with a leading ~ and $VAR references already expanded. Empty means only
	// the host binary's directory and PATH are searched.
	PluginPath  string
	Passthrough bool       // every token after this command is a raw positional (no flag parsing)
	Output      *OutputDef // what the command writes to stdout (nil = not declared); see [Context.WriteOutput]

	// Invoked reports whether this is the command the user invoked: the last command in the
	// chain. Exactly one entry of [Context.CommandChain] has it set; in a cascading hook,
	// rtx.Command().Invoked distinguishes the invoked command from its ancestors.
	Invoked bool
}

// expandPluginPath expands a leading ~ to the user's home directory and $VAR / ${VAR}
// references from the environment, as a shell would. When the home directory cannot be found
// the ~ is left in place.
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
func rootFrame(def Definition) Command {
	return Command{
		Name: def.Name, Handler: def.Handler,
		Flags: def.Flags, Arguments: def.Arguments,
		FlagGroups: def.FlagGroups, FlagDependencies: def.FlagDependencies,
		Commands: def.Commands, Plugins: def.Plugins, PluginDiscovery: def.PluginDiscovery,
		PluginPath: expandPluginPath(def.PluginPath), Passthrough: def.Passthrough,
		Output: def.Output,
	}
}

// cmdFrame is the chain frame for sub-command c; the caller sets Matched.
func cmdFrame(c CommandDef) Command {
	return Command{
		Name: c.Name, Handler: c.Handler, DeprecatedIdentifiers: c.DeprecatedIdentifiers, Deprecated: c.Deprecated,
		Flags: c.Flags, Arguments: c.Arguments,
		FlagGroups: c.FlagGroups, FlagDependencies: c.FlagDependencies,
		Commands: c.Commands, Plugins: c.Plugins, PluginDiscovery: c.PluginDiscovery,
		PluginPath: expandPluginPath(c.PluginPath), Passthrough: c.Passthrough,
		Output: c.Output,
	}
}

// resolveChain walks argv against def to find the invoked command path without validating
// inputs: descend sub-commands by name or alias, skip flags and their separate values, and
// stop at the first positional. A token naming a declared plugin returns the chain so far plus
// a non-nil [PluginDispatch] to exec instead.
//
// It is lenient by design: unknown flags, missing values and bad input are left to the parser.
func resolveChain(def Definition, argv []string) ([]Command, *PluginDispatch) {
	chain := []Command{rootFrame(def)}
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
		// A non-flag token beginning with "-" (a negative number, or bare "-") is a
		// positional, never a command or plugin name.
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
		if rd, ok := findPlugin(cur, tok); ok {
			return chain, &PluginDispatch{Def: rd, Args: append([]string{}, argv[i+1:]...), Dir: cur.PluginPath}
		}
		// At a discovery-enabled command, an unmatched token dispatches to the executable
		// <prefix><token>; the binary is located (and any error reported) at exec time.
		if d := cur.PluginDiscovery; d != nil {
			rd := PluginDef{Name: tok, Binary: d.Prefix + tok}
			return chain, &PluginDispatch{Def: rd, Args: append([]string{}, argv[i+1:]...), Dir: cur.PluginPath, Discovered: true}
		}
		break // first positional argument; stop descending
	}
	return chain, nil
}

// findPlugin returns the declared plugin of f matching tok by name or alias.
func findPlugin(f Command, tok string) (PluginDef, bool) {
	for _, r := range f.Plugins {
		if r.Name == tok {
			return r, true
		}
		if slices.Contains(r.Aliases, tok) {
			return r, true
		}
	}
	return PluginDef{}, false
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
func findFlag(chain []Command, name string) (FlagDef, string, bool) {
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
func findChild(f Command, tok string) (CommandDef, bool) {
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
