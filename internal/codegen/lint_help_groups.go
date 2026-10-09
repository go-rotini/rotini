package codegen

import "fmt"

// lintGroups checks a command's `groups`: each name is listed once, and each names a group one
// of the command's sub-commands or flags uses (a warning otherwise, as a likely typo). A
// composed sub-command may take its group from its own spec, which isn't read here, so a
// command with one doesn't get the warning.
func lintGroups(spec *Spec) []error {
	var problems []error
	walkCommandsAt(spec, func(c *Command, path, ptr string) {
		if len(c.Groups) == 0 {
			return
		}
		used := map[string]bool{}
		unknowable := false
		for _, sub := range c.Commands {
			used[sub.Group] = true
			if sub.Ref != "" && sub.Group == "" {
				unknowable = true
			}
		}
		for _, f := range c.Flags {
			used[f.Group] = true
		}
		seen := map[string]bool{}
		for i, g := range c.Groups {
			at := fmt.Sprintf("%s/groups/%d", ptr, i)
			switch {
			case seen[g.Name]:
				problems = append(problems, &problem{kind: "spec", ptr: at, loc: "command " + path,
					msg: fmt.Sprintf("lists group %q twice in `groups`; list each group once", g.Name)})
			case !used[g.Name] && !unknowable:
				problems = append(problems, &problem{kind: "spec", ptr: at, loc: "command " + path, sev: severityWarning,
					msg: fmt.Sprintf("lists group %q in `groups`, but no sub-command or flag of this command has `group: %s`; check the spelling, or remove the entry", g.Name, g.Name)})
			}
			seen[g.Name] = true
		}
	})
	return problems
}
