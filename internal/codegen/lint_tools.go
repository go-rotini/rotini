package codegen

import (
	"fmt"
	"slices"
	"strings"
)

// stdinClashMessage says why a command that reads stdin can't also have a tool parameter named
// "stdin".
const stdinClashMessage = `reads stdin and also has an input named "stdin", the tool parameter that holds what it reads; rename the input`

// lintToolStdinClash rejects, when the tools feature is enabled, a command offered to agents
// that reads stdin and has a tool parameter named "stdin": tool exports put what the command
// reads in a parameter of that name.
func lintToolStdinClash(spec *Spec, conf *Conf) []error {
	if spec == nil || conf == nil || conf.Generate == nil {
		return nil
	}
	if f := conf.Generate.featureOf("tools"); f == nil || !f.Enabled {
		return nil
	}
	var problems []error
	walkChainsAt(spec, func(chain []*Command, cmdPath, ptr string) {
		c := chain[len(chain)-1]
		if c.Stdin == nil || !toolCommandOffered(chain) {
			return
		}
		if at, ok := stdinParamAt(chain, ptr); ok {
			problems = append(problems, &problem{kind: "spec", ptr: at, loc: "command " + cmdPath, msg: stdinClashMessage})
		}
	})
	return problems
}

// toolCommandOffered reports whether the last command of chain is offered to agents, as
// agentProgram decides it from the contract.
func toolCommandOffered(chain []*Command) bool {
	hiddenAbove := false
	for _, a := range chain {
		if a.Agent != nil && !*a.Agent {
			return false
		}
	}
	for _, a := range chain[:len(chain)-1] {
		hiddenAbove = hiddenAbove || a.Hidden
	}
	c := chain[len(chain)-1]
	return (c.Agent != nil && *c.Agent) || !commandOutByDefault(c, hiddenAbove)
}

// stdinParamAt finds the input named "stdin" that a command's tool would take as a parameter,
// with its JSON pointer. The nearest declaration wins, as on the command line: the command's
// arguments, its flags, then each ancestor's cascading flags, nearest first.
func stdinParamAt(chain []*Command, ptr string) (string, bool) {
	c := chain[len(chain)-1]
	for i, a := range c.Arguments {
		if a.Name == "stdin" {
			return fmt.Sprintf("%s/arguments/%d", ptr, i), argumentParam(a)
		}
	}
	at := ptr
	for k, cmd := range slices.Backward(chain) {
		for i, f := range cmd.Flags {
			if f.Name == "stdin" && (k == len(chain)-1 || f.Cascading) {
				return fmt.Sprintf("%s/flags/%d", at, i), flagParam(f)
			}
		}
		if k > 0 {
			at = at[:strings.LastIndex(at, "/")] // drop the index
			at = at[:strings.LastIndex(at, "/")] // drop "commands"
		}
	}
	return "", false
}

// flagParam reports whether a flag is a tool parameter, as toolFlagOffered decides it from the
// contract.
func flagParam(f FlagInput) bool {
	if f.Role == "machine-output" {
		return false
	}
	if f.Agent != nil {
		return *f.Agent
	}
	return !flagOutByDefault(f)
}

// argumentParam reports whether an argument is a tool parameter.
func argumentParam(a ArgumentInput) bool {
	if a.Agent != nil {
		return *a.Agent
	}
	return !inputOutByDefault(a.Hidden, a.Deprecated, a.Schema)
}
