package codegen

import (
	"fmt"
	"strings"
)

// This file holds the lint rules that need BOTH documents: a problem in the spec that is only a
// problem because of how the conf configures generation. They run after each document has
// passed on its own, and report against the spec, which is where the fix goes.

// lintAcross runs the rules that read the spec and the conf together.
func (p *Processor) lintAcross(rs *reconciledSpec, rc *reconciledConf) []error {
	if rs == nil || rc == nil {
		return nil
	}
	problems := lintManPageNames(rs.spec, rc.conf)
	locateProblems(problems, rs.path, rs.locate)
	return problems
}

// lintManPageNames rejects two commands whose man pages would share one name, when the man
// feature is on. A page is named after the command path joined with "-" and lowercased, so
// `notes tag-remove` and `notes tag remove` are both notes-tag-remove, and `Add` and `add` are one
// file on a case-insensitive file system. One page would overwrite the other, and the name is
// also how `man` finds a page and how pages refer to each other.
//
// It checks the commands this spec declares. Commands composed in from another spec exist only
// once the whole tree is assembled, so generate checks those too.
func lintManPageNames(spec *Spec, conf *Conf) []error {
	if spec == nil || conf == nil || conf.Generate == nil {
		return nil
	}
	if f := conf.Generate.featureOf("man"); f == nil || !f.Enabled {
		return nil
	}
	first := map[string]string{}
	var problems []error
	walkChainsAt(spec, func(chain []*Command, _, ptr string) {
		names := make([]string, 0, len(chain))
		for _, c := range chain {
			if c.Name == "" {
				return // a $ref taking its name from the composed spec: generate checks it
			}
			names = append(names, c.Name)
		}
		page := manPageName(names[0], names[1:])
		command := strings.Join(names, " ")
		if prev, ok := first[page]; ok {
			problems = append(problems, &problem{
				kind: "spec", ptr: ptr, loc: "command " + command,
				msg: fmt.Sprintf("has the man page name %q, the same as command %q; both pages would be written to %s.<section>, so rename one of them", page, prev, page),
			})
			return
		}
		first[page] = command
	})
	return problems
}
