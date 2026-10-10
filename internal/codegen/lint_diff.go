package codegen

import (
	"fmt"

	"github.com/go-rotini/rotini/internal/contractdiff"
)

// lintDiffAccept checks diff.accept: each entry names a rule `rotini diff` reports, and no two
// entries acknowledge the same finding.
func lintDiffAccept(conf *Conf) []error {
	if conf.Diff == nil {
		return nil
	}
	var problems []error
	seen := map[[2]string]bool{}
	for i, a := range conf.Diff.Accept {
		ptr := fmt.Sprintf("/diff/accept/%d", i)
		if !contractdiff.KnownRule(a.Rule) {
			problems = append(problems, &problem{
				kind: "conf", ptr: ptr, loc: "diff.accept.rule",
				msg: didYouMean(fmt.Sprintf("%q is not a rule `rotini diff` reports", a.Rule), a.Rule, contractdiff.Rules()),
			})
		}
		key := [2]string{a.Rule, a.Where}
		if seen[key] {
			problems = append(problems, &problem{
				kind: "conf", ptr: ptr, loc: "diff.accept",
				msg: fmt.Sprintf("acknowledges %s at %q again; each finding takes one entry", a.Rule, a.Where),
			})
		}
		seen[key] = true
	}
	return problems
}
