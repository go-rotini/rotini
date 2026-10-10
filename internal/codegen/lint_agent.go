package codegen

import (
	"cmp"
	"fmt"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

// Lint rules for the agent-facing keys: effects, the flag roles programs read, and the
// `agent` opt-in and opt-out; plus lintFeatureKeys, which keeps each feature's own keys on
// that feature.

// raisesEffects reports whether a flag's effects add anything to its command's: a higher
// kind, "not idempotent" where the command doesn't say so, or "open world" where it doesn't.
func raisesEffects(cmd, flag *Effects) bool {
	if effectRank[flag.Kind] > effectRank[cmd.Kind] {
		return true
	}
	if flag.Idempotent != nil && !*flag.Idempotent && (cmd.Idempotent == nil || *cmd.Idempotent) {
		return true
	}
	return flag.OpenWorld != nil && *flag.OpenWorld && (cmd.OpenWorld == nil || !*cmd.OpenWorld)
}

// lintEffects checks `effects`: a flag's only raise its command's, which must declare its own,
// and a `$ref` node takes the child's, since the child's handler is what runs.
func lintEffects(spec *Spec) []error {
	var problems []error
	walkCommandsAt(spec, func(c *Command, cmdPath, ptr string) {
		if c.Ref != "" && c.Effects != nil {
			problems = append(problems, &problem{kind: "spec", ptr: ptr + "/effects", loc: "command " + cmdPath,
				msg: "sets `effects` on a `$ref` node; the child's handler is what runs, so declare its effects in the child spec"})
		}
		for i, f := range c.Flags {
			if f.Effects == nil {
				continue
			}
			fptr := fmt.Sprintf("%s/flags/%d/effects", ptr, i)
			switch {
			case c.Effects == nil:
				problems = append(problems, inputProblem(fptr, cmdPath, "flag", f.Name,
					"has `effects` but its command declares none; a flag's effects raise its command's, so declare the command's `effects` too"))
			case !raisesEffects(c.Effects, f.Effects):
				problems = append(problems, inputProblem(fptr, cmdPath, "flag", f.Name, fmt.Sprintf(
					"has effects %q, which add nothing to its command's %q; a flag's effects can only raise its command's; mark a preview with `role: dry-run`",
					effectsText(f.Effects), effectsText(c.Effects))))
			}
		}
	})
	return problems
}

// pageTypes are the types a `role: page` flag may have: a page number or a page token.
var pageTypes = []string{"int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64", "string"}

// lintFlagRoles checks the roles a program driving the CLI reads (dry-run, confirm,
// machine-output, page): their flag types, `role_value`, each at most once per command
// counting the cascading flags it inherits, and a warning for a destructive command that
// offers neither a preview nor a confirmation.
func lintFlagRoles(spec *Spec) []error {
	var problems []error
	walkChainsAt(spec, func(chain []*Command, cmdPath, ptr string) {
		c := chain[len(chain)-1]
		own := map[string]bool{}
		for _, f := range c.Flags {
			own[f.Name] = true
		}
		inherited := map[string]string{} // role → the inherited cascading flag that has it
		for _, a := range chain[:len(chain)-1] {
			for _, f := range a.Flags {
				if f.Cascading && isAgentRole(f.Role) && !own[f.Name] {
					inherited[f.Role] = f.Name
				}
			}
		}
		has := map[string]bool{}
		for r := range inherited {
			has[r] = true
		}
		for i, f := range c.Flags {
			fptr := fmt.Sprintf("%s/flags/%d", ptr, i)
			add := func(at, msg string) {
				problems = append(problems, inputProblem(fptr+at, cmdPath, "flag", f.Name, msg))
			}
			if f.RoleValue != "" && f.Role != "machine-output" {
				add("/role_value", "has `role_value` but not `role: machine-output`; role_value names the enum value that selects JSON output")
			}
			if !isAgentRole(f.Role) {
				continue
			}
			has[f.Role] = true
			if prev, ok := inherited[f.Role]; ok {
				add("/role", fmt.Sprintf("has role %s, which the cascading flag %q this command inherits already has; a command gives each role to one flag", f.Role, prev))
			}
			t := getSchemaType(f.Schema)
			switch f.Role {
			case "dry-run", "confirm":
				if t != "bool" {
					add("/role", fmt.Sprintf("has role %s but is a %s; the %s role marks a bool flag", f.Role, t, f.Role))
				}
			case "machine-output":
				problems = append(problems, machineOutputProblems(c, f, t, cmdPath, fptr)...)
			case "page":
				if !slices.Contains(pageTypes, t) {
					add("/role", fmt.Sprintf("has role page but is a %s; the page role marks an integer page number or a string page token", t))
				}
			}
		}
		if c.Effects != nil && c.Effects.Kind == "destructive" && !has["dry-run"] && !has["confirm"] {
			problems = append(problems, &problem{kind: "spec", ptr: ptr + "/effects", loc: "command " + cmdPath, sev: severityWarning,
				msg: "is destructive but has no flag with `role: dry-run` or `role: confirm`; an agent can neither preview it nor confirm it without a prompt"})
		}
	})
	return problems
}

// machineOutputProblems checks one `role: machine-output` flag: a bool flag, or a string flag
// with an enum and a `role_value` naming the value that selects JSON; a warning when its
// command declares no output to select.
func machineOutputProblems(c *Command, f FlagInput, t, cmdPath, fptr string) []error {
	var problems []error
	add := func(at string, sev severity, msg string) {
		p := inputProblem(fptr+at, cmdPath, "flag", f.Name, msg)
		p.sev = sev
		problems = append(problems, p)
	}
	switch t {
	case "bool":
		if f.RoleValue != "" {
			add("/role_value", severityError, "has `role_value` but is a bool flag; giving the flag selects JSON, so remove role_value")
		}
	case "string":
		var values []string
		if f.Schema != nil {
			for _, v := range enumValues(f.Schema.Enum) {
				values = append(values, v.Value)
			}
		}
		switch {
		case len(values) == 0:
			add("/role", severityError, "has role machine-output but is a string flag without an `enum`; list the output formats and name the JSON one in `role_value`")
		case f.RoleValue == "":
			add("/role", severityError, "has role machine-output but no `role_value`; name the enum value that selects JSON (`role_value: json`)")
		case !slices.Contains(values, f.RoleValue):
			add("/role_value", severityError, fmt.Sprintf("names %q, which isn't one of its enum values (%s)", f.RoleValue, strings.Join(values, ", ")))
		}
	default:
		add("/role", severityError, fmt.Sprintf("has role machine-output but is a %s; the machine-output role marks a bool flag, or a string flag whose `role_value` selects JSON", t))
	}
	if c.Output == nil && !f.Cascading {
		add("/role", severityWarning, "has role machine-output but its command declares no `output`, so there is no JSON to select")
	}
	return problems
}

// lintAgent warns about an `agent` key that changes nothing: `agent: true` on an item already
// offered to agents, on an item under a command with `agent: false`, or on a flag that is never
// a tool parameter; and `agent: true` on an env or config input that isn't hidden.
func lintAgent(spec *Spec) []error {
	var problems []error
	walkChainsAt(spec, func(chain []*Command, cmdPath, ptr string) {
		if p := agentCommandWarning(chain, cmdPath, ptr); p != nil {
			problems = append(problems, p)
		}
		problems = append(problems, agentInputWarnings(chain[len(chain)-1], cmdPath, ptr)...)
	})
	return problems
}

// agentCommandWarning warns about a command's `agent: true` that changes nothing, or nil.
func agentCommandWarning(chain []*Command, cmdPath, ptr string) error {
	c := chain[len(chain)-1]
	if c.Agent == nil || !*c.Agent {
		return nil
	}
	hiddenAbove, offAt := false, ""
	for _, a := range chain[:len(chain)-1] {
		hiddenAbove = hiddenAbove || a.Hidden
		if a.Agent != nil && !*a.Agent && offAt == "" {
			offAt = cmp.Or(a.Name, "(root)")
		}
	}
	var msg string
	switch {
	case offAt != "":
		msg = fmt.Sprintf("has `agent: true`, which has no effect: command %q has `agent: false`, which keeps its whole subtree from agents", offAt)
	case !commandOutByDefault(c, hiddenAbove):
		msg = "has `agent: true`, which has no effect: the command is offered to agents already; `agent: true` brings back a hidden, deprecated, passthrough or group command"
	default:
		return nil
	}
	return &problem{kind: "spec", ptr: ptr + "/agent", loc: "command " + cmdPath, sev: severityWarning, msg: msg}
}

// agentInputWarnings warns about each of a command's inputs whose `agent: true` changes nothing.
func agentInputWarnings(c *Command, cmdPath, ptr string) []error {
	var problems []error
	warn := func(at, channel, name, msg string) {
		p := inputProblem(at+"/agent", cmdPath, channel, name, "has `agent: true`, which has no effect: "+msg)
		p.sev = severityWarning
		problems = append(problems, p)
	}
	on := func(b *bool) bool { return b != nil && *b }
	for i, f := range c.Flags {
		at := fmt.Sprintf("%s/flags/%d", ptr, i)
		switch {
		case !on(f.Agent):
		case f.Role == "chdir":
			warn(at, "flag", f.Name, "a directory flag (role chdir) is never a tool parameter, since a tool runs in the server's directory")
		case f.Role == "machine-output":
			warn(at, "flag", f.Name, "tool exports select JSON with the machine-output flag themselves, so it is never a tool parameter")
		case !flagOutByDefault(f):
			warn(at, "flag", f.Name, "the flag is a tool parameter already; `agent: true` brings back a hidden, deprecated, secret, short-circuit or `from:` flag")
		}
	}
	for i, a := range c.Arguments {
		if on(a.Agent) && !inputOutByDefault(a.Hidden, a.Deprecated, a.Schema) {
			warn(fmt.Sprintf("%s/arguments/%d", ptr, i), "argument", a.Name,
				"the argument is a tool parameter already; `agent: true` brings back a hidden, deprecated, secret or `from:` argument")
		}
	}
	for i, e := range c.Env {
		if on(e.Agent) && !e.Hidden {
			warn(fmt.Sprintf("%s/env/%d", ptr, i), "env", e.Name,
				"environment variables are never tool parameters, and a visible one is listed on the agent skill page already")
		}
	}
	for i, cfg := range c.Config {
		if on(cfg.Agent) && !cfg.Hidden {
			warn(fmt.Sprintf("%s/config/%d", ptr, i), "config", cfg.Name,
				"config values are never tool parameters, and a visible one is listed on the agent skill page already")
		}
	}
	return problems
}

// commandOutByDefault reports whether a command is left out of agent outputs unless it says
// `agent: true`: hidden (itself or an ancestor), deprecated, a group command, or passthrough.
func commandOutByDefault(c *Command, hiddenAbove bool) bool {
	if hiddenAbove || c.Hidden || c.Deprecated != "" || len(c.Commands) > 0 || c.Passthrough {
		return true
	}
	return slices.ContainsFunc(c.Arguments, func(a ArgumentInput) bool { return a.Passthrough })
}

// flagOutByDefault reports whether a flag is left out of tool parameters unless it says
// `agent: true`.
func flagOutByDefault(f FlagInput) bool {
	return f.ShortCircuit || inputOutByDefault(f.Hidden, f.Deprecated, f.Schema)
}

// inputOutByDefault reports whether an input is left out of tool parameters unless it says
// `agent: true`: hidden, deprecated, secret, or read `from:` a file or stdin.
func inputOutByDefault(hidden bool, deprecated string, s *InputSchema) bool {
	return hidden || deprecated != "" || (s != nil && (s.Secret || len(s.From) > 0))
}

// programFeatureKeys maps each key only some features take to the feature types that take it.
var programFeatureKeys = []struct {
	key   string
	types []string
	set   func(Feature) bool
}{
	{"file", []string{"tools", "skill", "llms", "permissions"}, func(f Feature) bool { return f.File != "" }},
	{"targets", []string{"tools"}, func(f Feature) bool { return len(f.Targets) > 0 }},
	{"mcp_revision", []string{"tools"}, func(f Feature) bool { return f.McpRevision != "" }},
	{"go", []string{"tools"}, func(f Feature) bool { return f.Go }},
	{"name", []string{"skill"}, func(f Feature) bool { return f.Name != "" }},
	{"description", []string{"skill"}, func(f Feature) bool { return f.Description != "" }},
	{"base_url", []string{"llms"}, func(f Feature) bool { return f.BaseUrl != "" }},
	{"harnesses", []string{"permissions"}, func(f Feature) bool { return len(f.Harnesses) > 0 }},
}

// lintFeatureKeys keeps each feature-specific key on the features it applies to, and checks the
// whole-program features' own settings: `file` stays inside the module, `go` needs the mcp
// target, the per-command knobs (embed, embed_dir) and, for the features built from the
// contract, the template knobs don't apply, and the skill's references and llms.txt's links
// need the markdown feature.
func lintFeatureKeys(conf *Conf) []error {
	if conf.Generate == nil {
		return nil
	}
	var problems []error
	add := func(i int, f Feature, key string, sev severity, msg string) {
		problems = append(problems, &problem{kind: "conf", ptr: featurePointer(i), loc: "generate.features." + f.Type + "." + key, sev: sev, msg: msg})
	}
	for i, f := range conf.Generate.Features {
		for _, k := range programFeatureKeys {
			if k.set(f) && !slices.Contains(k.types, f.Type) {
				add(i, f, k.key, severityError, fmt.Sprintf("%#q applies only to the %s feature%s; move it there or remove it", k.key, strings.Join(k.types, ", "), map[bool]string{true: "s", false: ""}[len(k.types) > 1]))
			}
		}
		if pf, ok := programFeatureOf(f.Type); ok && f.Enabled {
			problems = append(problems, programFeatureProblems(conf, i, f, pf)...)
		}
	}
	return problems
}

// programFeatureProblems checks one enabled whole-program feature's own settings.
func programFeatureProblems(conf *Conf, i int, f Feature, pf programFeature) []error {
	var problems []error
	add := func(key string, sev severity, msg string) {
		problems = append(problems, &problem{kind: "conf", ptr: featurePointer(i), loc: "generate.features." + f.Type + "." + key, sev: sev, msg: msg})
	}
	if clean := path.Clean(filepath.ToSlash(f.File)); f.File != "" && (clean == ".." || strings.HasPrefix(clean, "../")) {
		add("file", severityError, fmt.Sprintf("%q must resolve under the module root", f.File))
	}
	if f.Embed || f.EmbedDir != "" {
		add("embed", severityError, f.Type+" writes files for agents into the repository, not into the binary; `embed` and `embed_dir` don't apply, so remove them (use `file` to choose where they go)")
	}
	switch {
	case pf.tmplFile == "" && (f.Template || f.TemplateDir != ""):
		add("template", severityWarning, f.Type+" is built from the contract and has no editable template; `template` and `template_dir` have no effect here")
	case f.TemplateDir != "" && !f.Template:
		add("template_dir", severityWarning, "is set but `template` is false; `template_dir` is used only when the editable template is seeded (`template: true`); it is otherwise ignored")
	}
	md := conf.Generate.featureOf("markdown")
	mdOn := md != nil && md.Enabled
	switch {
	case f.Type == "tools" && f.Go && len(f.Targets) > 0 && !slices.Contains(f.Targets, "mcp"):
		add("go", severityError, "puts mcp.json in the generated code, but `targets` doesn't include mcp; add mcp or remove `go`")
	case f.Type == "skill" && !mdOn:
		add("type", severityWarning, "the skill's references/ folder holds the markdown pages, but the markdown feature is off, so the skill has none; turn markdown on (it needn't embed)")
	case f.Type == "llms" && f.BaseUrl == "" && (!mdOn || !md.Embed):
		add("base_url", severityWarning, "llms.txt links each command to its markdown page, but without `base_url` the links are relative and the markdown feature doesn't write its pages (it needs `enabled: true` and `embed: true`); set `base_url` to where the pages are published")
	}
	return problems
}
