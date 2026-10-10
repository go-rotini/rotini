package codegen

import "slices"

// completionStub reports whether c's new stub prints a shell's completion script: c is a
// completion command and the completion feature is on.
func completionStub(gp *program, c genCommand) bool {
	return c.completionShell && featureEnabled(gp.conf, "completion")
}

// isCompletionCommand reports whether c is a completion command: named completion, with one
// required string argument whose enum lists only shells rotini writes scripts for. Its new
// stub prints the script for the shell given.
func isCompletionCommand(c *Command) bool {
	in := c.inputs()
	if c.Name != "completion" || in == nil || len(in.Arguments) != 1 {
		return false
	}
	s := in.Arguments[0].Schema
	if s == nil || !s.Required || (s.Type != "" && s.Type != "string") || len(s.Enum) == 0 {
		return false
	}
	for _, v := range s.Enum {
		if !slices.Contains(completionShells, readEnumMember(v).Value) {
			return false
		}
	}
	return true
}
