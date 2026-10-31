package codegen

import (
	"slices"
	"strings"
)

// exitStatusRefPrefix is the pointer form of a reference to a named exit status.
const exitStatusRefPrefix = "#/exit_statuses/"

// resolveExitStatusRefs replaces each `{$ref: <name>}` entry of an exit_status list with the
// named exit status the root command's exit_statuses declares, named by its key. An entry
// whose name isn't declared, or that sets another key beside its $ref, keeps its $ref for
// lintExitStatus to report; a resolved entry's $ref is cleared.
func resolveExitStatusRefs(s *Spec) {
	named := s.Command.ExitStatuses
	walkCommands(s, func(c *Command, _ string) {
		for i, e := range c.ExitStatus {
			if e.Ref == "" {
				continue
			}
			name := strings.TrimPrefix(e.Ref, exitStatusRefPrefix)
			n, ok := named[name]
			if !ok || e != (ExitStatusEntry{Ref: e.Ref}) {
				continue
			}
			entry := ExitStatusEntry{Code: n.Code, Name: name, Summary: n.Summary, Retryable: n.Retryable, DocsUrl: n.DocsUrl}
			if n.Output != nil {
				out := deepCopy(*n.Output)
				entry.Output = &out
			}
			c.ExitStatus[i] = entry
		}
	})
}

// documentedExit is one exit code a command documents, and whether it comes from the root's
// list.
type documentedExit struct {
	ExitStatusEntry

	inherited bool
}

// documentedExits is the exit_status a command documents, given its own list as written and
// the root command's: its own list when it declares one, else the root's, which is
// program-wide. An `inherit: true` item in its own list stands for the root's entries, less
// any code the list gives itself. Every consumer of a command's documented exit codes reads
// them through here, so the pages, the contract, the Definition and the handler audit agree.
func documentedExits(own, root []ExitStatusEntry) []documentedExit {
	var out []documentedExit
	if len(own) == 0 {
		for _, e := range root {
			if !e.Inherit {
				out = append(out, documentedExit{e, true})
			}
		}
		return out
	}
	listed := func(code int) bool {
		return slices.ContainsFunc(own, func(e ExitStatusEntry) bool { return !e.Inherit && e.Code == code })
	}
	inherited := false
	for _, e := range own {
		if !e.Inherit {
			out = append(out, documentedExit{e, false})
			continue
		}
		if inherited {
			continue
		}
		inherited = true
		for _, r := range root {
			if !r.Inherit && !listed(r.Code) {
				out = append(out, documentedExit{r, true})
			}
		}
	}
	return out
}

// effectiveExitStatus is [documentedExits] as plain entries.
func effectiveExitStatus(own, root []ExitStatusEntry) []ExitStatusEntry {
	exits := documentedExits(own, root)
	if exits == nil {
		return nil
	}
	out := make([]ExitStatusEntry, len(exits))
	for i, e := range exits {
		out[i] = e.ExitStatusEntry
	}
	return out
}
