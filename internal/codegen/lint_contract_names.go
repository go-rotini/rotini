package codegen

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Lint rules for facts a program reads from the spec as data: what a flag's role promises
// against what its command does, and an exit code's name across the program.

// lintReadOnlyRoles warns about a flag with role dry-run, confirm or force on a command whose
// effects say it only reads, when none of its flags raises that: there is nothing to preview,
// confirm or overwrite, so a program driving the CLI is told something the spec contradicts.
func lintReadOnlyRoles(spec *Spec) []error {
	var problems []error
	walkCommandsAt(spec, func(c *Command, cmdPath, ptr string) {
		if c.Effects == nil || c.Effects.Kind != "read" {
			return
		}
		if slices.ContainsFunc(c.Flags, func(f FlagInput) bool { return f.Effects != nil && effectRank()[f.Effects.Kind] > 0 }) {
			return
		}
		for i, f := range c.Flags {
			what := map[string]string{"dry-run": "preview", "confirm": "confirm", "force": "overwrite"}[f.Role]
			if what == "" {
				continue
			}
			p := inputProblem(fmt.Sprintf("%s/flags/%d/role", ptr, i), cmdPath, "flag", f.Name, fmt.Sprintf(
				"has role %s, but its command's effects are read, so there is nothing to %s; remove the role, or declare the command's (or the flag's) effects as write or destructive",
				f.Role, what))
			p.sev = severityWarning
			problems = append(problems, p)
		}
	})
	return problems
}

// lintExitNames warns when one exit code has different names on different commands
// (`not_found: 3` on one, `missing: 3` on another): a script reading the contract or the
// generated constants sees two meanings for one number. Composed specs name their own codes.
func lintExitNames(spec *Spec) []error {
	type naming struct{ name, path string }
	first := map[int]naming{}
	var problems []error
	walkCommandsAt(spec, func(c *Command, cmdPath, ptr string) {
		for i, e := range c.ExitStatus {
			if e.Name == "" {
				continue
			}
			prev, ok := first[e.Code]
			if !ok {
				first[e.Code] = naming{e.Name, cmdPath}
				continue
			}
			if prev.name == e.Name {
				continue
			}
			problems = append(problems, &problem{kind: "spec", ptr: fmt.Sprintf("%s/exit_status/%d/name", ptr, i), loc: "command " + cmdPath, sev: severityWarning,
				msg: fmt.Sprintf("exit_status names code %d %q, but command %s names it %q; give a code one name across the program, so a script reading it sees one meaning",
					e.Code, e.Name, prev.path, prev.name)})
		}
	})
	return problems
}

// seeAlsoPage matches a man page cross-reference: a page name and its section, `taskr-add(1)`.
func seeAlsoPage() *regexp.Regexp {
	return seeAlsoPageOnce.value(func() *regexp.Regexp {
		return regexp.MustCompile(`^([A-Za-z0-9][A-Za-z0-9._-]*)\((\d[A-Za-z0-9]*)\)$`)
	})
}

var seeAlsoPageOnce lazy[*regexp.Regexp]

// lintSeeAlso warns about a `see_also` entry that names a command or help topic the program
// doesn't have: a `{command: …}` or `{topic: …}` reference, or a text entry naming one of the
// program's own man pages (its name is the root's, or starts with the root's and "-") that
// matches no command or help topic page, or names it in another section than the man
// feature's. Commands composed by `$ref` are declared in their own spec, so any command or
// page below one is accepted.
func lintSeeAlso(spec *Spec, conf *Conf) []error {
	if spec == nil || spec.Command.Name == "" {
		return nil
	}
	root := strings.ToLower(spec.Command.Name)
	pages := map[string]bool{}
	commands := map[string]bool{} // command paths below the root, space separated
	var mounted []string          // the page names of $ref commands, whose pages below them the spec can't list
	var mountedPaths []string     // the same commands' paths
	walkChainsAt(spec, func(chain []*Command, _, _ string) {
		names := make([]string, 0, len(chain))
		for _, c := range chain {
			if c.Name == "" {
				return
			}
			names = append(names, c.Name)
		}
		page := manPageName(names[0], names[1:])
		pages[page] = true
		path := strings.Join(names[1:], " ")
		commands[path] = true
		if chain[len(chain)-1].Ref != "" {
			mounted = append(mounted, page)
			mountedPaths = append(mountedPaths, path)
		}
	})
	topics := map[string]bool{}
	for _, t := range spec.Command.Topics {
		pages[manPageName(spec.Command.Name, []string{t.Name})] = true
		topics[t.Name] = true
	}
	section := manSection(conf)
	var problems []error
	walkCommandsAt(spec, func(c *Command, cmdPath, ptr string) {
		for i, entry := range seeAlsoOf(c.SeeAlso) {
			at := &problem{kind: "spec", ptr: ptr + "/see_also/" + strconv.Itoa(i), loc: "command " + cmdPath, sev: severityWarning}
			switch {
			case entry.command != nil:
				path := strings.Join(entry.command, " ")
				underMount := slices.ContainsFunc(mountedPaths, func(m string) bool { return strings.HasPrefix(path, m+" ") })
				if commands[path] || underMount {
					continue
				}
				at.msg = didYouMean(fmt.Sprintf("`see_also` names the command %q, which %s doesn't have", path, spec.Command.Name), path, slices.Sorted(maps.Keys(commands)))
			case entry.topic != "":
				if topics[entry.topic] {
					continue
				}
				at.msg = didYouMean(fmt.Sprintf("`see_also` names the help topic %q, which %s doesn't declare in `topics:`", entry.topic, spec.Command.Name), entry.topic, slices.Sorted(maps.Keys(topics)))
			case entry.url != "":
				continue
			default:
				m := seeAlsoPage().FindStringSubmatch(strings.TrimSpace(entry.text))
				if len(m) < 3 || (m[1] != root && !strings.HasPrefix(m[1], root+"-")) {
					continue
				}
				underMount := slices.ContainsFunc(mounted, func(page string) bool { return strings.HasPrefix(m[1], page+"-") })
				switch {
				case !pages[m[1]] && !underMount:
					at.msg = didYouMean(fmt.Sprintf("`see_also` entry %q names the page %s, which no command or help topic of %s has", entry.text, m[1], spec.Command.Name), m[1], slices.Sorted(maps.Keys(pages)))
				case m[2] != section:
					at.msg = fmt.Sprintf("`see_also` entry %q names section %s, but the man pages are written in section %s; write %s(%s)", entry.text, m[2], section, m[1], section)
				default:
					continue
				}
			}
			problems = append(problems, at)
		}
	})
	return problems
}
