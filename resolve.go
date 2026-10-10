package rotini

import (
	"fmt"
	"slices"
	"strings"
)

// Command resolution turns argv into the chain of commands it names. [DefaultResolver] is the
// default, which a program may wrap or replace with [Program.WithResolver]. It always runs before
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
	DeprecatedSince       string   // the release that deprecated it; see [CommandDef.DeprecatedSince]
	RemovedIn             string   // the release that will remove it
	ReplacedBy            string   // the command to use instead; see [CommandDef.ReplacedBy]
	Flags                 []FlagDef
	Arguments             []ArgDef
	FlagGroups            []FlagGroup
	FlagDependencies      []FlagDependency
	Commands              []CommandDef        // sub-commands; empty for a leaf
	Plugins               []PluginDef         // co-located plugin binaries dispatched as sub-commands
	PluginDiscovery       *PluginDiscoveryDef // plugin auto-discovery (nil = off)
	// PluginPath is an extra directory searched for this command's declared and discovered
	// plugin binaries, with a leading ~ and $VAR references already expanded from the run's
	// environment ([Program.WithEnviron]). Empty means only the host binary's directory and PATH
	// are searched.
	PluginPath  string
	Passthrough bool       // every token after this command is a raw positional (no flag parsing)
	Output      *OutputDef // what the command writes to stdout (nil = not declared); see [Context.WriteOutput]

	// OptionsFirst stops flags at this command's first argument when it is the invoked one; see
	// [CommandDef.OptionsFirst].
	OptionsFirst bool

	// DeprecatedIdentifiersRemovedIn is the release removing each deprecated alias; see
	// [CommandDef.DeprecatedIdentifiersRemovedIn].
	DeprecatedIdentifiersRemovedIn map[string]string

	// Invoked reports whether this is the command the user invoked: the last command in the
	// chain. Exactly one entry of [Context.CommandChain] has it set; in a cascading hook,
	// rtx.Command().Invoked distinguishes the invoked command from its ancestors.
	Invoked bool

	// Usage is the command's usage line; see [CommandDef.Usage] and [Context.Usage].
	Usage string

	// pluginPathRaw is PluginPath as declared, for re-expanding it against a run's injected
	// environment; view is that run's environment, used by [Command.PluginBinary] and
	// [Command.DiscoveredPlugins]. Both are set by the runtime; nil reads the process.
	pluginPathRaw string
	view          *osView
}

// expandPluginPath expands a leading ~ to the user's home directory and $VAR / ${VAR}
// references from the environment, as a shell would. When the home directory cannot be found
// the ~ is left in place.
func expandPluginPath(dir string) string {
	var process *osView
	return process.expandPluginPath(dir)
}

// rootFrame is the chain frame for the program's root command.
func rootFrame(def Definition) Command {
	return Command{
		Name: def.Name, Handler: def.Handler,
		Flags: def.Flags, Arguments: def.Arguments,
		FlagGroups: def.FlagGroups, FlagDependencies: def.FlagDependencies,
		Commands: def.Commands, Plugins: def.Plugins, PluginDiscovery: def.PluginDiscovery,
		PluginPath: expandPluginPath(def.PluginPath), pluginPathRaw: def.PluginPath, Passthrough: def.Passthrough, OptionsFirst: def.OptionsFirst,
		Output: def.Output, Usage: def.Usage,
	}
}

// cmdFrame is the chain frame for sub-command c; the caller sets Matched.
func cmdFrame(c CommandDef) Command {
	return Command{
		Name: c.Name, Handler: c.Handler, DeprecatedIdentifiers: c.DeprecatedIdentifiers, Deprecated: c.Deprecated,
		DeprecatedSince: c.DeprecatedSince, RemovedIn: c.RemovedIn, DeprecatedIdentifiersRemovedIn: c.DeprecatedIdentifiersRemovedIn, ReplacedBy: c.ReplacedBy,
		Flags: c.Flags, Arguments: c.Arguments,
		FlagGroups: c.FlagGroups, FlagDependencies: c.FlagDependencies,
		Commands: c.Commands, Plugins: c.Plugins, PluginDiscovery: c.PluginDiscovery,
		PluginPath: expandPluginPath(c.PluginPath), pluginPathRaw: c.PluginPath, Passthrough: c.Passthrough, OptionsFirst: c.OptionsFirst,
		Output: c.Output, Usage: c.Usage,
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
		if isFlag(chain, tok) {
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

// isFlag reports whether tok is a flag token, given chain, the commands reached so far. Every
// argv walker (the resolver, the parser, completion and the deprecation and plugin-host parses)
// decides with it, so they agree on where flags are. Bare "-" and "--" are not flags, and
// neither is a negative number: "-" followed by a digit ("-5", "-1e3", "-5s"), or by "." and a
// digit ("-.5"). The exception is a declared digit option: "-4…" is a flag when -4 is an
// identifier on chain. Any other word starting with "-" is a flag, so a declared -I wins over
// "-Inf".
func isFlag(chain []Command, tok string) bool {
	if len(tok) <= 1 || tok[0] != '-' || tok == "--" {
		return false
	}
	if isDigit(tok[1]) {
		_, _, declared := findFlagIndex(chain, tok[:2])
		return declared
	}
	return len(tok) < 3 || tok[1] != '.' || !isDigit(tok[2])
}

// flagIn returns isFlag bound to chain, for a walker that tests words one at a time.
func flagIn(chain []Command) func(string) bool {
	return func(tok string) bool { return isFlag(chain, tok) }
}

func isDigit(b byte) bool { return '0' <= b && b <= '9' }

// splitFlag splits a flag token into its identifier and an inline "=value".
func splitFlag(tok string) (name, value string, hasValue bool) {
	if before, after, ok := strings.Cut(tok, "="); ok {
		return before, after, true
	}
	return tok, "", false
}

// findChild returns the sub-command of f matching tok by name, alias or hidden alias.
func findChild(f Command, tok string) (CommandDef, bool) {
	for _, c := range f.Commands {
		if c.matches(tok) {
			return c, true
		}
	}
	return CommandDef{}, false
}

// Resolution is the outcome of the resolve phase: the invoked command path
// (root → leaf), or a plugin dispatch that replaces local execution.
type Resolution struct {
	// Chain is the resolved command path the run phase dispatches (when
	// Plugin is nil). It must be non-empty — the root command is always there.
	Chain []Command
	// Plugin, when non-nil, short-circuits local dispatch: the runtime execs this binary
	// instead, stdio passed through and context honored.
	Plugin *PluginDispatch
	// Argv is the vector the run phase exposes as [Context.Argv]. A resolver that rewrites
	// tokens returns the rewritten vector here so parsing agrees with its routing; nil keeps
	// the original argv.
	Argv []string
}

// Resolver is the resolve phase: it matches argv against the [Definition] to decide what this
// invocation targets. An error is reported as a fault and fails the run. See
// [DefaultResolver].
//
// The Definition is passed by value, but its slices are the program's own and shared by every
// run. A resolver must treat it as read-only; writing through it changes every later run of the
// [Program].
type Resolver func(def Definition, argv []string) (Resolution, error)

// DefaultResolver is rotini's resolve phase, exported for a custom [Resolver] to wrap. It
// descends sub-commands by name or alias, skips flags and their values, stops at the first
// positional, and diverts to a plugin dispatch for declared and discovered plugins. It does
// not validate the invoked command's input.
//
// A plugin receives only the words after its name, so flags typed before the name are checked
// strictly: a short-circuit flag set there (such as --help) cancels the dispatch, and the run
// answers it for the command the words before the plugin name resolve to, with [Resolution.Argv]
// cut to those words. The root's `role: chdir` flag may come there too: the plugin runs in its
// directory. Any other flag there, or one that does not parse, is returned as a usage
// [*ParseError].
func DefaultResolver(def Definition, argv []string) (Resolution, error) {
	chain, plugin := resolveChain(def, argv)
	if plugin != nil {
		before := argv[:len(argv)-len(plugin.Args)-1]
		answer, err := pluginHostFlags(chain, before, plugin.Def.Name)
		if err != nil {
			return Resolution{}, err
		}
		if answer {
			return Resolution{Chain: chain, Argv: before}, nil
		}
	}
	return Resolution{Chain: chain, Plugin: plugin, Argv: argv}, nil
}

// pluginHostFlags checks the words before plugin's name (before) against chain, the commands
// they resolved. It reports whether a short-circuit flag set there replaces the dispatch, and
// fails on any other flag, which the plugin would never receive. The root's `role: chdir` flag
// is allowed there: the plugin runs in its directory.
func pluginHostFlags(chain []Command, before []string, plugin string) (answer bool, err error) {
	before = withoutChdir(chain, before)
	first := slices.IndexFunc(before, flagIn(chain))
	if first < 0 {
		return false, nil
	}
	store, err := quietTokens(chain, before)
	if err != nil {
		return false, err
	}
	if shortCircuited(chain, store) {
		return true, nil
	}
	name, _, _ := splitFlag(before[first])
	return false, &ParseError{
		Kind:    ParseKindMisplacedFlag,
		Msg:     fmt.Sprintf("%s can't come before plugin %q: a plugin receives only the words after its name", name, plugin),
		Command: chain[len(chain)-1].Name,
		Flag:    name,
		Token:   name,
	}
}
