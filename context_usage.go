package rotini

// Usage returns the usage line of [Context.Command] ("taskr add <title> [flags]"): the
// spec's `usage` when set, else the line rotini derives from the command's shape, which is the
// line help prints under its Usage heading. A cascading hook gets its own command's line. It
// returns "" for a Context with no command, or a hand-built [Definition] that sets none.
//
// Nothing prints it by default. A reporter can print it after a usage error:
//
//	if len(out.Errors) > 0 && rotini.CategoryOf(out.Errors[0]) == rotini.CategoryUsage {
//		if u := rtx.Usage(); u != "" {
//			fmt.Fprintf(rtx.Stderr, "Usage: %s\n", u)
//		}
//	}
//
// Handlers use it rather than the generated Usage function, so a composed command reports the
// line of the program it runs in.
func (rtx *Context) Usage() string {
	if rtx == nil {
		return ""
	}
	rtx.mu.RLock()
	defer rtx.mu.RUnlock()
	if len(rtx.chain) == 0 {
		return ""
	}
	return rtx.chain[rtx.frameIndexLocked()].Usage
}
