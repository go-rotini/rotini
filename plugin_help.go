package rotini

import (
	"fmt"
	"slices"
)

// PluginHelp answers a help command for a plugin: when path names one, it runs the plugin with
// the words after the plugin's name and --help, and reports true. path is read as
// [Context.HelpFor] reads it, from the command the help command sits under, so in `acme help
// sync` it is ["sync"]. The plugin runs as `acme sync --help` would run it: the same search,
// timeout, environment and signal handling, and the program refuses to run itself as one. Its
// output passes through, and the run halts with its exit code, or with a [*PluginError] when the
// plugin can't be run. A discovered plugin counts only when its binary is found, so a mistyped
// word still gets the help command's own answer. It reports false, doing nothing, when path
// names no plugin.
//
//	if rtx.PluginHelp(topic...) {
//		return
//	}
//	page, err := rtx.HelpFor(topic...)
func (rtx *Context) PluginHelp(path ...string) bool {
	rtx.mu.RLock()
	run := rtx.pluginHelp
	frame := rtx.frameIndexLocked()
	rtx.mu.RUnlock()
	if run == nil || len(path) == 0 {
		return false
	}
	return run(rtx, append(helpPath(rtx.chainRef(), 1, frame), path...))
}

// pluginHelp is [Context.PluginHelp] for words, a command path below the root.
func (p *Program) pluginHelp(rtx *Context, words []string) bool {
	chain, pd := resolveChain(p.def, words)
	if pd == nil {
		return false
	}
	fs := p.features()
	if fs.plugins == nil {
		path := pathOf(chain)
		rtx.recordFault(asFault(missingFeatureIn(path, "plugins", "Plugins()",
			fmt.Sprintf("command %q dispatches plugin %q", path, pd.Def.Name))))
		rtx.Halt()
		return true
	}
	if fs.plugins.help == nil {
		return false
	}
	return fs.plugins.help(p, rtx, chain, pluginDispatchFor(chain, pd, rtx.view))
}

// runPluginHelp runs d with --help for [Context.PluginHelp].
func (p *Program) runPluginHelp(rtx *Context, chain []Command, d *PluginDispatch) bool {
	if d.Discovered {
		if len(chain) > 0 && foldsToDeclared(&chain[len(chain)-1], d.Def.Name) {
			return false
		}
		if _, err := resolvePluginBinary(d.Def.Binary, d.Dir, rtx.osView()); err != nil {
			return false
		}
	}
	h := *d
	h.Args = append(slices.Clone(d.Args), "--help")
	code, perr, err := p.runPlugin(rtx.Context(), rtx, chain, &h)
	if perr != nil {
		rtx.HaltWith(perr)
		return true
	}
	// err is the plugin's own failure, whose exit code passes through.
	if err != nil && code == 0 {
		code = 1
	}
	rtx.HaltWithCode(code)
	return true
}
