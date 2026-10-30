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

// lintEffects checks `effects`: a flag's only raise its command's, which must declare its own.
func lintEffects(spec *Spec) []error {
	var problems []error
	walkCommandsAt(spec, func(c *Command, cmdPath, ptr string) {
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
// offers agents neither a preview nor a confirmation, unless `agent: false` keeps it from them.
func lintFlagRoles(spec *Spec) []error {
	var problems []error
	walkChainsAt(spec, func(chain []*Command, cmdPath, ptr string) {
		c := chain[len(chain)-1]
		own := map[string]bool{}
		for _, f := range c.Flags {
			own[f.Name] = true
		}
		inherited := map[string]string{} // role → the inherited cascading flag that has it
		var roleFlags []FlagInput        // the command's flags with an agent role, inherited ones too
		for _, a := range chain[:len(chain)-1] {
			for _, f := range a.Flags {
				if f.Cascading && isAgentRole(f.Role) && !own[f.Name] {
					inherited[f.Role] = f.Name
					roleFlags = append(roleFlags, f)
				}
			}
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
			roleFlags = append(roleFlags, f)
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
		agentOff := slices.ContainsFunc(chain, func(a *Command) bool { return a.Agent != nil && !*a.Agent })
		if p := unsafeDestructive(c, roleFlags, cmdPath, ptr); p != nil && !agentOff {
			problems = append(problems, p)
		}
	})
	return problems
}

// unsafeDestructive warns about a destructive command whose role flags offer agents neither a
// preview nor a confirmation, or returns nil.
func unsafeDestructive(c *Command, roleFlags []FlagInput, cmdPath, ptr string) error {
	if c.Effects == nil || c.Effects.Kind != "destructive" || slices.ContainsFunc(roleFlags, previewOffered) {
		return nil
	}
	msg := "is destructive but has no flag with `role: dry-run` or `role: confirm`"
	if slices.ContainsFunc(roleFlags, func(f FlagInput) bool { return f.Role == "dry-run" || f.Role == "confirm" }) {
		msg = "is destructive but agents are offered none of its `role: dry-run` or `role: confirm` flags (each is hidden, deprecated or `agent: false`)"
	}
	return &problem{kind: "spec", ptr: ptr + "/effects", loc: "command " + cmdPath, sev: severityWarning,
		msg: msg + "; an agent can neither preview it nor confirm it without a prompt"}
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
// a tool parameter; and `agent: true` on an env or config input that isn't hidden. It also warns
// about `agent: true` on a secret flag or argument, which a tool can't pass on the command line.
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
		msg = "has `agent: true`, which has no effect: the command is offered to agents already; `agent: true` brings back a hidden, deprecated or passthrough command, or one with sub-commands and no arguments"
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
	secret := func(at, channel, name string, s *InputSchema) {
		p := inputProblem(at+"/agent", cmdPath, channel, name, secretAgentMessage(s))
		p.sev = severityWarning
		problems = append(problems, p)
	}
	on := func(b *bool) bool { return b != nil && *b }
	for i, f := range c.Flags {
		at := fmt.Sprintf("%s/flags/%d", ptr, i)
		switch {
		case !on(f.Agent):
		case f.Role == "machine-output":
			warn(at, "flag", f.Name, "the machine-output flag is never a tool parameter; tool exports add it to every call themselves unless it has `agent: false`")
		case f.Schema != nil && f.Schema.Secret:
			secret(at, "flag", f.Name, f.Schema)
		case !flagOutByDefault(f):
			warn(at, "flag", f.Name, "the flag is a tool parameter already; `agent: true` brings back a hidden, deprecated, secret, short-circuit, directory (role chdir) or `from:` flag")
		}
	}
	for i, a := range c.Arguments {
		at := fmt.Sprintf("%s/arguments/%d", ptr, i)
		switch {
		case !on(a.Agent):
		case a.Schema != nil && a.Schema.Secret:
			secret(at, "argument", a.Name, a.Schema)
		case !inputOutByDefault(a.Hidden, a.Deprecated, a.Schema):
			warn(at, "argument", a.Name,
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
// `agent: true`: hidden (itself or an ancestor), deprecated, passthrough, or with sub-commands and
// no arguments (it prints help when run bare).
func commandOutByDefault(c *Command, hiddenAbove bool) bool {
	if hiddenAbove || c.Hidden || c.Deprecated != "" || (len(c.Commands) > 0 && len(c.Arguments) == 0) || c.Passthrough {
		return true
	}
	return slices.ContainsFunc(c.Arguments, func(a ArgumentInput) bool { return a.Passthrough })
}

// flagOutByDefault reports whether a flag is left out of tool parameters unless it says
// `agent: true`.
func flagOutByDefault(f FlagInput) bool {
	return f.ShortCircuit || f.Role == "chdir" || inputOutByDefault(f.Hidden, f.Deprecated, f.Schema)
}

// previewOffered reports whether a flag is a dry-run or confirm flag agents are offered, as
// the agent exports decide it.
func previewOffered(f FlagInput) bool {
	return (f.Role == "dry-run" || f.Role == "confirm") && flagParam(f)
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
	{"file", []string{"tools", "skill", "llms", "permissions", "carapace", "config_example", "env_example"}, func(f Feature) bool { return f.File != "" }},
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
		problems = append(problems, &problem{kind: "conf", ptr: featurePointer(i) + "/" + key, loc: "generate.features." + f.Type + "." + key, sev: sev, msg: msg})
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
		problems = append(problems, &problem{kind: "conf", ptr: featurePointer(i) + "/" + key, loc: "generate.features." + f.Type + "." + key, sev: sev, msg: msg})
	}
	if clean := path.Clean(filepath.ToSlash(f.File)); f.File != "" && (clean == ".." || strings.HasPrefix(clean, "../")) {
		add("file", severityError, fmt.Sprintf("%s must resolve under the module root", quotePath(f.File)))
	}
	problems = append(problems, embedKnobProblems(i, f, pf)...)
	switch {
	case pf.tmplFile == "" && (f.Template || f.TemplateDir != ""):
		add("template", severityWarning, f.Type+" is built in Go and has no editable template; `template` and `template_dir` have no effect here")
	case f.TemplateDir != "" && !f.Template:
		add("template_dir", severityWarning, "is set but `template` is false; `template_dir` is used only when the editable template is seeded (`template: true`); it is otherwise ignored")
	}
	if f.Type == "tools" && f.McpRevision != "" && len(f.Targets) > 0 && !slices.Contains(f.Targets, "mcp") {
		add("mcp_revision", severityError, "sets the revision mcp.json is written for, but `targets` doesn't include mcp, so it would be silently ignored; add mcp or remove `mcp_revision`")
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

// lintLlmsBaseURL warns about an llms feature `base_url` that doesn't end in "/": llms.txt links
// each page as `base_url` followed by its file name, so https://x/app would link
// https://x/appapp-list.md.
func lintLlmsBaseURL(conf *Conf) []error {
	if conf.Generate == nil {
		return nil
	}
	var problems []error
	for i, f := range conf.Generate.Features {
		if f.Type != "llms" || f.BaseUrl == "" || strings.HasSuffix(f.BaseUrl, "/") {
			continue
		}
		problems = append(problems, &problem{kind: "conf", ptr: featurePointer(i) + "/base_url", loc: "generate.features.llms.base_url", sev: severityWarning,
			msg: fmt.Sprintf("%q doesn't end in \"/\"; each link is `base_url` followed by the page's file name, so links read %q; end it with \"/\"", f.BaseUrl, f.BaseUrl+"<page>.md")})
	}
	return problems
}

// lintSkillDescription checks the enabled skill feature's SKILL.md `description`, the root's
// summary followed by the feature's `description`: it must be set, and at most 1024
// characters. The problem is positioned at the feature's `description` when it has one, else
// at the root's summary.
func lintSkillDescription(spec *Spec, conf *Conf) []error {
	if spec == nil || conf == nil || conf.Generate == nil {
		return nil
	}
	i := slices.IndexFunc(conf.Generate.Features, func(f Feature) bool { return f.Type == "skill" && f.Enabled })
	if i < 0 {
		return nil
	}
	f := conf.Generate.Features[i]
	const loc = "generate.features.skill.description"
	n := len([]rune(skillDescription(spec.Command.Summary, f.Description)))
	switch {
	case n == 0:
		return []error{&problem{kind: "conf", ptr: featurePointer(i), loc: loc,
			msg: "the skill needs a description, and the root command has no summary to start it; give the root a summary, or set `description`"}}
	case n <= skillDescriptionLimit:
		return nil
	}
	msg := fmt.Sprintf("the skill's description, the root's summary followed by this `description`, is %d characters; the Agent Skills format allows %d, so shorten it", n, skillDescriptionLimit)
	if f.Description == "" {
		return []error{&problem{kind: "spec", ptr: "/command/summary", loc: "command " + spec.Command.Name + " summary",
			msg: fmt.Sprintf("is the skill's description, and is %d characters; the Agent Skills format allows %d, so shorten it", n, skillDescriptionLimit)}}
	}
	return []error{&problem{kind: "conf", ptr: featurePointer(i) + "/description", loc: loc, msg: msg}}
}

// embedKnobProblems checks `embed` and `embed_dir` on a whole-program feature: only
// config_example and env_example put text in the binary, so only they take them.
func embedKnobProblems(i int, f Feature, pf programFeature) []error {
	at := func(key string, sev severity, msg string) error {
		return &problem{kind: "conf", ptr: featurePointer(i) + "/" + key, loc: "generate.features." + f.Type + "." + key, sev: sev, msg: msg}
	}
	switch {
	case pf.accessor && f.EmbedDir != "" && !f.Embed:
		return []error{at("embed_dir", severityWarning, "is set but `embed` is false; `embed_dir` is used only in embed mode (//go:embed); inline content writes no file, so it is ignored")}
	case !pf.accessor && (f.Embed || f.EmbedDir != ""):
		return []error{at("embed", severityError, f.Type+" writes its files into the repository, not into the binary; `embed` and `embed_dir` don't apply, so remove them (use `file` to choose where they go)")}
	}
	return nil
}

// secretAgentMessage is the warning for `agent: true` on a secret flag or argument: tool servers
// never put a secret on the command line, where other local users can read it.
func secretAgentMessage(s *InputSchema) string {
	const msg = "has `agent: true` on a secret, which a tool can't use: tool servers never pass a secret on the command line, where other local users can see it; "
	if s.Variable != nil {
		return msg + "supply it through its environment variable instead"
	}
	return msg + "declare an environment variable with `variable:` and supply it through that instead"
}
