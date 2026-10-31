package codegen

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
)

// lintExitStatusRefs checks the exit_status items that aren't codes written out: a `$ref` must
// name one of the root's exit_statuses and stand alone, and `inherit: true` must stand alone,
// at most once, on a command other than the root.
func lintExitStatusRefs(spec *Spec) []error {
	names := slices.Sorted(maps.Keys(spec.Command.ExitStatuses)) // sorted for a deterministic suggestion
	var problems []error
	walkCommandsAt(spec, func(c *Command, path, ptr string) {
		inherits := 0
		for i, e := range c.ExitStatus {
			at := fmt.Sprintf("%s/exit_status/%d", ptr, i)
			add := func(key, msg string) {
				problems = append(problems, &problem{kind: "spec", ptr: at + key, loc: "command " + path, msg: msg})
			}
			switch {
			case e.Inherit && e.Ref != "":
				add("", "exit_status item sets both `inherit` and `$ref`; give each its own item")
			case e.Inherit && c == &spec.Command:
				add("/inherit", "`inherit: true` adds the root's exit_status to a command's own, so the root has nothing to inherit; remove it")
			case e.Inherit && e != (ExitStatusEntry{Inherit: true}):
				add("", "`inherit: true` stands alone in its item; give the command's own codes items of their own")
			case e.Inherit:
				inherits++
				if inherits > 1 {
					add("/inherit", "exit_status lists `inherit: true` twice; the root's codes are added once")
				}
			case e.Ref != "":
				name := strings.TrimPrefix(e.Ref, exitStatusRefPrefix)
				if _, ok := spec.Command.ExitStatuses[name]; !ok {
					add("/$ref", didYouMean(fmt.Sprintf("`$ref` %q names no exit status under the root command's `exit_statuses`", e.Ref), name, names))
					continue
				}
				add("", fmt.Sprintf("`$ref` %q stands alone in its item: the entry is the named one; to document the code differently here, write the entry out", e.Ref))
			}
		}
	})
	return problems
}

// lintExitStatusOne, turned on by the conf's `validate.exit_status_one`, warns about each
// command whose own exit_status leaves out 1, the code a recorded error or a recovered panic
// exits with when no handler sets one. A list that adds the root's with `inherit: true` is
// checked with the root's codes in it.
func lintExitStatusOne(spec *Spec, conf *Conf) []error {
	if spec == nil || conf == nil || conf.Validate == nil || !conf.Validate.ExitStatusOne {
		return nil
	}
	var problems []error
	walkCommandsAt(spec, func(c *Command, path, ptr string) {
		if len(c.ExitStatus) == 0 {
			return
		}
		root := spec.Command.ExitStatus
		if c == &spec.Command {
			root = nil
		}
		if slices.ContainsFunc(effectiveExitStatus(c.ExitStatus, root), func(e ExitStatusEntry) bool { return e.Code == 1 }) {
			return
		}
		problems = append(problems, &problem{
			kind: "spec", ptr: ptr + "/exit_status", loc: "command " + path, sev: severityWarning,
			msg: "exit_status leaves out 1, the code every recorded error exits with unless a handler sets another; list it (validate.exit_status_one)",
		})
	})
	return problems
}

// lintExitStatusDropsRoot, turned on by the conf's `validate.exit_status_drops_root`, warns
// about each command whose own exit_status, without `inherit: true`, leaves out a code the
// root's lists. Such a list replaces the root's, so the command's pages and contract stop
// documenting the codes it doesn't repeat.
func lintExitStatusDropsRoot(spec *Spec, conf *Conf) []error {
	if spec == nil || conf == nil || conf.Validate == nil || !conf.Validate.ExitStatusDropsRoot {
		return nil
	}
	root := spec.Command.ExitStatus
	var problems []error
	walkCommandsAt(spec, func(c *Command, path, ptr string) {
		if c == &spec.Command || len(c.ExitStatus) == 0 || slices.ContainsFunc(c.ExitStatus, func(e ExitStatusEntry) bool { return e.Inherit }) {
			return
		}
		var dropped []string
		for _, r := range root {
			if r.Inherit || slices.ContainsFunc(c.ExitStatus, func(e ExitStatusEntry) bool { return e.Code == r.Code }) {
				continue
			}
			if code := strconv.Itoa(r.Code); !slices.Contains(dropped, code) {
				dropped = append(dropped, code)
			}
		}
		if len(dropped) == 0 {
			return
		}
		codes, them := "code "+dropped[0], "it"
		if len(dropped) > 1 {
			codes, them = "codes "+strings.Join(dropped[:len(dropped)-1], ", ")+" and "+dropped[len(dropped)-1], "them"
		}
		problems = append(problems, &problem{
			kind: "spec", ptr: ptr + "/exit_status", loc: "command " + path, sev: severityWarning,
			msg: fmt.Sprintf("exit_status replaces the root's and leaves out %s, which the root lists; list %s too, or add the root's with `inherit: true` (validate.exit_status_drops_root)", codes, them),
		})
	})
	return problems
}
