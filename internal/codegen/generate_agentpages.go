package codegen

import (
	"bytes"
	"cmp"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"text/template"
)

// The agent pages: an Agent Skills page (skills/<name>/SKILL.md, with the markdown pages under
// references/) and an llms.txt. Both render from editable templates over templateProgramData.

const (
	skillTemplateName = "skill.md.tmpl"
	llmsTemplateName  = "llms.txt.tmpl"
)

var (
	//go:embed templates/skill.md.tmpl
	templateSkill string
	//go:embed templates/llms.txt.tmpl
	templateLLMS string
)

// skillLineLimit is the length the Agent Skills format recommends a SKILL.md stay under.
const skillLineLimit = 500

// skillName is the Agent Skills rule for a skill's name.
var skillName = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// templateProgramData is what the whole-program page templates render.
type templateProgramData struct {
	Name        string // the root command's name: the binary
	Display     string // the name pages show: display_name, else Name
	Summary     string // the root's summary
	Description string // the root's description

	SkillName        string // skill: the skill's name (its directory)
	SkillDescription string // skill: the frontmatter description
	AllowedTools     string // skill: the frontmatter allowed-tools, "" for none
	BaseURL          string // llms: where the markdown pages are published, "" for none

	Commands []templateProgramCommand // the commands offered to agents, root first
	Optional []templateProgramCommand // llms: the other visible commands (groups, deprecated, passthrough)
	Env      []templateProgramInput   // environment variables the offered commands read
	Config   []templateProgramInput   // config values the offered commands read
	JSON     bool                     // some command has a machine-output flag
	Previews bool                     // some command has a dry-run or confirm flag
}

// templateProgramCommand is one command on a whole-program page.
type templateProgramCommand struct {
	Invocation  string   // "taskr remote add"
	Summary     string   //
	Description string   //
	Aliases     []string // its other names
	Effects     string   // the worst case over the command and its flags, as words; "" when undeclared
	Kind        string   // that worst case's kind: read, write, destructive, or ""
	Stability   string   // experimental, beta or "", inherited from its ancestors
	Deprecated  string   // its deprecation message, "" for none
	JSON        string   // what to add to the command line for JSON output, "" for none
	DryRun      string   // its dry-run flag, "" for none
	Confirm     string   // its confirm flag, "" for none
	Examples    []string
	ExitStatus  []templateDocExitRow
	Link        string // skill: its reference page (references/<page>.md); llms: its markdown page; "" for none
}

// templateProgramInput is an environment variable or config value on a whole-program page.
type templateProgramInput struct {
	Name     string // the variable(s), or the config key
	Summary  string
	Required bool
	Secret   bool
	Commands []string // the commands that read it
}

// programData builds the page data both agent pages share. link returns a command's page
// link, or "".
func programData(p *program, a *agentProgram, link func(page string) string) templateProgramData {
	d := templateProgramData{Name: a.doc.Name, Display: a.display, Summary: p.rootHelp.Summary, Description: p.rootHelp.Description}
	envAt := map[string]int{}
	configAt := map[string]int{}
	for _, c := range a.commands {
		cmd := c.c
		h := a.help[strings.Join(cmd.Path, " ")]
		row := templateProgramCommand{
			Invocation: c.invocation, Summary: cmd.Summary, Description: cmd.Description, Aliases: cmd.Aliases,
			Effects: effectsText(c.effects), Stability: c.stability, Deprecated: cmd.Deprecated,
			JSON: strings.Join(c.machine, " "), DryRun: c.dryRun, Confirm: c.confirm, Examples: h.Examples,
			Link: link(manPageName(a.doc.Name, cmd.Path)),
		}
		if c.effects != nil {
			row.Kind = c.effects.Kind
		}
		for _, e := range cmd.ExitStatus {
			row.ExitStatus = append(row.ExitStatus, templateDocExitRow{Code: e.Code, Name: e.Name, Summary: e.Summary, Retryable: e.Retryable, DocsURL: e.DocsURL})
		}
		if !c.offered {
			if !cmd.Hidden && !agentOff(a, cmd.Path) {
				d.Optional = append(d.Optional, row)
			}
			continue
		}
		d.Commands = append(d.Commands, row)
		d.JSON = d.JSON || row.JSON != ""
		d.Previews = d.Previews || row.DryRun != "" || row.Confirm != ""
		for _, e := range cmd.Env {
			if !listedForAgents(e) {
				continue
			}
			name := strings.Join(e.Variables, ", ")
			if i, ok := envAt[name]; ok {
				d.Env[i].Commands = append(d.Env[i].Commands, c.invocation)
				continue
			}
			envAt[name] = len(d.Env)
			d.Env = append(d.Env, templateProgramInput{Name: name, Summary: e.Summary, Required: e.Required, Secret: e.Secret, Commands: []string{c.invocation}})
		}
		for _, cfg := range cmd.Config {
			if !listedForAgents(cfg) {
				continue
			}
			name := cfg.Key
			if i, ok := configAt[name]; ok {
				d.Config[i].Commands = append(d.Config[i].Commands, c.invocation)
				continue
			}
			configAt[name] = len(d.Config)
			d.Config = append(d.Config, templateProgramInput{Name: name, Summary: cfg.Summary, Required: cfg.Required, Secret: cfg.Secret, Commands: []string{c.invocation}})
		}
	}
	return d
}

// listedForAgents reports whether an env or config input is listed on the agent pages: a
// visible one unless `agent: false`, a hidden one with `agent: true`.
func listedForAgents(in agentInput) bool {
	if in.Agent != nil {
		return *in.Agent
	}
	return !in.Hidden
}

// agentOff reports whether a command or an ancestor says `agent: false`.
func agentOff(a *agentProgram, path []string) bool {
	for _, c := range a.doc.Commands {
		if c.Agent != nil && !*c.Agent && len(c.Path) <= len(path) && strings.Join(c.Path, " ") == strings.Join(path[:len(c.Path)], " ") {
			return true
		}
	}
	return false
}

// renderProgramPage executes a whole-program page template and tidies the result, ending it
// with one newline.
func renderProgramPage(tmpl *template.Template, d templateProgramData) ([]byte, error) {
	var b bytes.Buffer
	if err := tmpl.Execute(&b, d); err != nil {
		return nil, errors.New(templateFailure(tmpl.Name(), err))
	}
	return []byte(tidy(b.String()) + "\n"), nil
}

// renderSkill writes skills/<name>/SKILL.md, and the offered commands' markdown pages under
// skills/<name>/references/ when the markdown feature is on.
func renderSkill(p *program, f *Feature, a *agentProgram, tmpl *template.Template) (map[string][]byte, []error, error) {
	name := f.Name
	if name == "" {
		name = strings.Trim(regexp.MustCompile(`-+`).ReplaceAllString(strings.ToLower(strings.ReplaceAll(a.doc.Name, "_", "-")), "-"), "-")
	}
	if len(name) > 64 || !skillName.MatchString(name) {
		return nil, nil, fmt.Errorf("the skill name %q isn't valid: use 1 to 64 lowercase letters, digits and single hyphens; set `name`", name)
	}
	refs := map[string]string{}
	for _, o := range p.featureOutputs {
		if o.desc.name != "markdown" {
			continue
		}
		for i, n := range o.nodes {
			if !n.topic {
				refs[n.data.PageName] = o.contents[i]
			}
		}
	}
	d := programData(p, a, func(page string) string {
		if _, ok := refs[page]; ok {
			return "references/" + page + ".md"
		}
		return ""
	})
	d.SkillName = name
	d.SkillDescription = sentence(p.rootHelp.Summary)
	if f.Description != "" {
		d.SkillDescription = strings.TrimSpace(d.SkillDescription + " " + f.Description)
	}
	switch n := len([]rune(d.SkillDescription)); {
	case n == 0:
		return nil, nil, errors.New("the skill needs a description: give the root command a summary, or set the feature's `description`")
	case n > 1024:
		return nil, nil, fmt.Errorf("the skill's description (the root's summary and the feature's `description`) is %d characters; the Agent Skills format allows 1024", n)
	}
	d.AllowedTools = allowedTools(a)
	page, err := renderProgramPage(tmpl, d)
	if err != nil {
		return nil, nil, err
	}
	var notices []error
	if lines := bytes.Count(page, []byte("\n")); lines > skillLineLimit {
		notices = append(notices, fmt.Errorf("skill: SKILL.md is %d lines; the Agent Skills format recommends under %d, so keep the details in the reference pages", lines, skillLineLimit))
	}
	files := map[string][]byte{name + "/SKILL.md": page}
	for _, c := range d.Commands {
		if c.Link != "" {
			page := strings.TrimSuffix(strings.TrimPrefix(c.Link, "references/"), ".md")
			files[name+"/"+c.Link] = []byte(refs[page] + "\n")
		}
	}
	return files, notices, nil
}

// allowedTools is the skill's allowed-tools: a Claude Code Bash rule for every name of every
// leaf command offered to agents whose worst case is read. A rule for a group or the root would
// also match every sub-command, so they get none.
func allowedTools(a *agentProgram) string {
	var rules []string
	for _, c := range a.offered() {
		if !c.leaf || c.effects == nil || c.effects.Kind != "read" {
			continue
		}
		for _, words := range commandSpellings(a, c) {
			rules = append(rules, "Bash("+words+" *)")
		}
	}
	return strings.Join(rules, " ")
}

// commandSpellings lists every way to type a command: each name and alias of each command on
// its path, after the display name's words.
func commandSpellings(a *agentProgram, c agentCommand) []string {
	out := []string{strings.Join(c.words[:len(c.words)-len(c.c.Path)], " ")}
	for i := range c.c.Path {
		names := []string{c.c.Path[i]}
		for _, other := range a.doc.Commands {
			if !other.Plugin && strings.Join(other.Path, " ") == strings.Join(c.c.Path[:i+1], " ") {
				names = append(names, other.Aliases...)
			}
		}
		var next []string
		for _, prefix := range out {
			for _, n := range names {
				next = append(next, strings.TrimSpace(prefix+" "+n))
			}
		}
		out = next
	}
	return out
}

// renderLLMS writes llms.txt, linking each command to its markdown page: under base_url when
// set, else relative to llms.txt, at the markdown feature's files.
func renderLLMS(p *program, f *Feature, a *agentProgram, tmpl *template.Template) (map[string][]byte, []error, error) {
	link := func(page string) string { return "" }
	if f.BaseUrl != "" {
		link = func(page string) string { return f.BaseUrl + page + ".md" }
	} else if md := p.conf.Generate.featureOf("markdown"); md != nil && md.Enabled && md.Embed {
		files := map[string]string{}
		for _, o := range p.featureOutputs {
			if o.desc.name == "markdown" {
				for _, n := range o.nodes {
					files[n.data.PageName] = filepath.Join(o.absEmbedDir, n.file)
				}
			}
		}
		at, err := underModule(p.module.root, "generate.features.llms.file", cmp.Or(f.File, "llms.txt"))
		if err != nil {
			return nil, nil, err
		}
		link = func(page string) string {
			if files[page] == "" {
				return ""
			}
			rel, err := filepath.Rel(filepath.Dir(at), files[page])
			if err != nil {
				return ""
			}
			return filepath.ToSlash(rel)
		}
	}
	d := programData(p, a, link)
	d.BaseURL = f.BaseUrl
	page, err := renderProgramPage(tmpl, d)
	if err != nil {
		return nil, nil, err
	}
	return map[string][]byte{"": page}, nil, nil
}

// sentence ends s with a full stop unless it ends a sentence already; "" stays "".
func sentence(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || strings.ContainsAny(s[len(s)-1:], ".!?") {
		return s
	}
	return s + "."
}

// quoteString writes s as a JSON string, which is also a YAML double-quoted string, without
// escaping <, > and &.
func quoteString(s string) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(s); err != nil {
		return strconv.Quote(s) // unreachable: a string always encodes
	}
	return strings.TrimSuffix(b.String(), "\n")
}
