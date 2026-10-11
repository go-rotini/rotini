package rotini

// DashIndex reports how many of the invoked command's positional words came before a "--"
// that ended flags, and whether one was typed, so a handler forwarding its arguments can tell
// the words typed before the "--" from those after it:
//
//	app exec -- ls -la      → 0, true
//	app exec a -- b         → 1, true
//	app exec ls -la         → 0, false
//
// It counts words as typed, before a separator splits them. A "--" that is itself an argument
// isn't counted: one after a passthrough argument has started (`app exec ls -- x`), after an
// options_first command's first argument, or anywhere after a passthrough command's name. It
// reads only the command line, never a file or stdin, and reports (0, false) when the command
// line doesn't parse.
func (rtx *Context) DashIndex() (int, bool) {
	if rtx == nil {
		return 0, false
	}
	chain := rtx.CommandChain()
	if len(chain) == 0 {
		return 0, false
	}
	store := quietParse(chain, rtx.Argv)
	if store == nil || !store.dashSeen {
		return 0, false
	}
	return store.dashAt, true
}
