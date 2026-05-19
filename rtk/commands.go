package rtk

import "slices"

// matchesCommand reports whether token matches the command's name or any of
// its aliases.
func matchesCommand(cmd *CommandSpec, token string) bool {
	if cmd.Name == token {
		return true
	}
	return slices.Contains(cmd.Aliases, token)
}

// findMatchedCommand scans flagArgs from startIdx looking for the first
// unconsumed positional token that matches a command name or alias in
// commands. It skips flag tokens, "-" (stdin marker), and indices already
// marked as consumed.
//
// Returns the matched command and its index in flagArgs, or (nil, -1) if
// no match is found.
func findMatchedCommand(commands []CommandSpec, flagArgs []string, startIdx int, consumed map[int]bool) (*CommandSpec, int) {
	for i := startIdx; i < len(flagArgs); i++ {
		if consumed[i] || isFlag(flagArgs[i]) || flagArgs[i] == "-" {
			continue
		}
		for ci := range commands {
			c := &commands[ci]
			if matchesCommand(c, flagArgs[i]) {
				return c, i
			}
		}
	}
	return nil, -1
}

// suggestFlags returns up to one suggestion (in a single-element slice) for
// an unknown flag identifier among the given flag set, or nil if no
// candidate is within edit-distance 2.
func suggestFlags(name string, flags []FlagSpec) []string {
	var candidates []string
	for _, f := range flags {
		candidates = append(candidates, f.Identifiers...)
	}
	if s := suggestNearest(name, candidates); s != "" {
		return []string{s}
	}
	return nil
}
