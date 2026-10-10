package codegen

import (
	"cmp"
	"encoding/json"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"text/template"
)

// The whole-program feature kind. The help, man and markdown features render one page per
// command and completion one script per shell, all into the cmd package. A program feature
// renders files about the whole program into the repository, at its conf `file` (resolved
// under the module root, like generate.contract.file):
//
//   - text pages render from a Go template over templateProgramData, seeded into
//     template_dir with `template: true` exactly as help's is (skill, llms);
//   - structured exports are built in Go from the contract document, never from a template
//     (tools, permissions);
//   - the user-facing files are built in Go from the command tree (carapace, config_example,
//     env_example).
//
// Program features add no Go code to the cmd package (tools' `go: true`, and config_example
// and env_example's ConfigExample and EnvExample, are the exceptions) and are never pruned:
// turning one off leaves its files, as with the contract file.

// programFeature describes one whole-program feature.
type programFeature struct {
	name     string // the conf type, e.g. "skill"
	file     string // the default `file`: a directory when it ends in "/"
	tmplFile string // editable template file name; "" for a structured export
	embedded string // the built-in template text
	accessor bool   // it also puts its text in the cmd package, which embed and embed_dir store
	// render returns the feature's files, keyed by path relative to its resolved `file`
	// ("" for the file itself), and any notices.
	render func(p *program, f *Feature, a *agentProgram, tmpl *template.Template) (map[string][]byte, []error, error)
}

// programFeatures are the whole-program features, in generation order.
var programFeatures = []programFeature{
	{name: "tools", file: "tools/", render: renderToolExports},
	{name: "skill", file: "skills/", tmplFile: skillTemplateName, embedded: templateSkill, render: renderSkill},
	{name: "llms", file: "llms.txt", tmplFile: llmsTemplateName, embedded: templateLLMS, render: renderLLMS},
	{name: "permissions", file: "agents/", render: renderPermissions},
	{name: "carapace", file: "completions/carapace/", render: renderCarapace},
	{name: "config_example", file: "examples/config/", render: renderConfigExample, accessor: true},
	{name: "env_example", file: ".env.example", render: renderEnvExample, accessor: true},
}

// programFeatureOf returns the whole-program feature with the given conf type.
func programFeatureOf(name string) (programFeature, bool) {
	i := slices.IndexFunc(programFeatures, func(f programFeature) bool { return f.name == name })
	if i < 0 {
		return programFeature{}, false
	}
	return programFeatures[i], true
}

// emitProgramFeatures writes every enabled whole-program feature's files. It runs after the
// contract and before the cmd file, which carries tools' ToolsMCP variable.
func (p *program) emitProgramFeatures() error {
	g := p.conf.Generate
	if g == nil {
		return nil
	}
	var a *agentProgram
	for _, pf := range programFeatures {
		f := g.featureOf(pf.name)
		if f == nil || !f.Enabled {
			continue
		}
		if a == nil {
			var err error
			if a, err = p.agentProgram(); err != nil {
				return err
			}
		}
		var tmpl *template.Template
		if pf.tmplFile != "" {
			var err error
			if f.Template {
				dir := filepath.Join(p.module.root, filepath.FromSlash(f.TemplateDir))
				tmpl, err = loadFeatureTemplate(p.plan, dir, docFeature{name: pf.name, tmplFile: pf.tmplFile, embedded: pf.embedded})
			} else {
				tmpl, err = parseDocTemplate(pf.tmplFile, pf.embedded)
			}
			if err != nil {
				return err
			}
		}
		files, notices, err := pf.render(p, f, a, tmpl)
		if err != nil {
			return fmt.Errorf("generate.features.%s: %w", pf.name, err)
		}
		p.agentNotices = append(p.agentNotices, notices...)
		base, err := underModule(p.module.root, "generate.features."+pf.name+".file", cmp.Or(f.File, pf.file))
		if err != nil {
			return err
		}
		for _, rel := range slices.Sorted(maps.Keys(files)) {
			target := base
			if rel != "" {
				target = filepath.Join(base, filepath.FromSlash(rel))
			}
			if err := p.plan.write(target, files[rel]); err != nil {
				return err
			}
		}
	}
	return nil
}

// ─── the program as agents see it ───────────────────────────────────────────────.

// agentProgram is the contract document read back, with what every agent output needs worked
// out once: which commands are offered to agents, their worst-case effects and the flags that
// preview, confirm and select JSON. The contract is the one source of these facts.
type agentProgram struct {
	doc      agentDoc
	commands []agentCommand // every non-plugin command, contract order
	display  string         // the root's display name
	help     map[string]cmdHelp
}

// agentDoc is the subset of the contract document the agent outputs read.
type agentDoc struct {
	Name        string             `json:"name"`
	Commands    []agentContractCmd `json:"commands"`
	Definitions map[string]any     `json:"definitions"`
}

type agentContractCmd struct {
	Name        string         `json:"name"`
	Path        []string       `json:"path"`
	Summary     string         `json:"summary"`
	Description string         `json:"description"`
	Aliases     []string       `json:"aliases"`
	Hidden      bool           `json:"hidden"`
	Deprecated  string         `json:"deprecated"`
	Plugin      bool           `json:"plugin"`
	Passthrough bool           `json:"passthrough"`
	Arguments   []agentInput   `json:"arguments"`
	Flags       []agentInput   `json:"flags"`
	Env         []agentInput   `json:"env"`
	Config      []agentInput   `json:"config"`
	Stdin       *contractStdin `json:"stdin"`
	Output      map[string]any `json:"output"`
	Stream      bool           `json:"stream"`
	ExitStatus  []contractExit `json:"exit_status"`
	Stability   string         `json:"stability"`
	Effects     *Effects       `json:"effects"`
	Agent       *bool          `json:"agent"`
}

// agentInput is an argument, flag, env or config input of the contract.
type agentInput struct {
	Name                  string                       `json:"name"`
	Summary               string                       `json:"summary"`
	Description           string                       `json:"description"`
	Type                  string                       `json:"type"`
	Kind                  string                       `json:"kind"`
	Required              bool                         `json:"required"`
	Variadic              bool                         `json:"variadic"`
	Passthrough           bool                         `json:"passthrough"`
	Identifiers           []string                     `json:"identifiers"`
	DeprecatedIdentifiers []string                     `json:"deprecated_identifiers"`
	Negated               []string                     `json:"negated"`
	ShortCircuit          bool                         `json:"short_circuit"`
	Role                  string                       `json:"role"`
	RoleValue             string                       `json:"role_value"`
	Inherited             bool                         `json:"inherited"`
	Env                   []string                     `json:"env"`
	ConfigKey             string                       `json:"config_key"`
	Variables             []string                     `json:"variables"`
	Key                   string                       `json:"key"`
	From                  []string                     `json:"from"`
	Separator             string                       `json:"separator"`
	Secret                bool                         `json:"secret"`
	Hidden                bool                         `json:"hidden"`
	Deprecated            string                       `json:"deprecated"`
	Stability             string                       `json:"stability"`
	Effects               *Effects                     `json:"effects"`
	Agent                 *bool                        `json:"agent"`
	Schema                map[string]any               `json:"schema"`
	EnumValues            map[string]contractEnumValue `json:"enum_values"`
}

// agentCommand is one command of the program, with its agent-facing facts resolved.
type agentCommand struct {
	c           agentContractCmd
	invocation  string   // "taskr remote add", with the root's display name
	words       []string // the words that run it: the display name's words, then the path
	leaf        bool     // it has no sub-commands
	offered     bool     // in tool exports, the skill page, llms.txt and permission rules
	effects     *Effects // the worst case over the command and every flag it accepts
	toolEffects *Effects // the worst case over the command and the flags a tool can pass
	machine     []string // what to add to the command line for JSON output; nil for none
	dryRun      string   // the identifier of its dry-run flag; "" for none
	confirm     string   // the identifier of its confirm flag; "" for none
	stability   string   // the least stable of it and its ancestors: experimental, beta or ""
}

// agentProgram reads the program's contract document back and resolves the agent facts.
func (p *program) agentProgram() (*agentProgram, error) {
	raw, err := p.contract(p.contractNodes())
	if err != nil {
		return nil, err
	}
	a := &agentProgram{display: p.rootDisplay, help: p.commandHelps()}
	if err := json.Unmarshal(raw, &a.doc); err != nil {
		return nil, fmt.Errorf("read the contract back: %w", err)
	}
	groups := map[string]bool{}
	off := map[string]bool{}        // command paths with `agent: false`
	declared := map[string]string{} // command path → its declared stability
	for _, c := range a.doc.Commands {
		if len(c.Path) > 0 {
			groups[strings.Join(c.Path[:len(c.Path)-1], " ")] = true
		}
		if c.Agent != nil && !*c.Agent {
			off[strings.Join(c.Path, " ")] = true
		}
		declared[strings.Join(c.Path, " ")] = c.Stability
	}
	displayWords := strings.Fields(p.rootDisplay)
	for _, c := range a.doc.Commands {
		if c.Plugin {
			continue
		}
		key := strings.Join(c.Path, " ")
		ac := agentCommand{
			c: c, leaf: !groups[key],
			invocation: strings.Join(append(slices.Clone(displayWords), c.Path...), " "),
			words:      append(slices.Clone(displayWords), c.Path...),
		}
		underOff := false
		for i := 0; i <= len(c.Path); i++ {
			if off[strings.Join(c.Path[:i], " ")] {
				underOff = true
			}
			ac.stability = leastStable(ac.stability, declared[strings.Join(c.Path[:i], " ")])
		}
		passthrough := c.Passthrough || slices.ContainsFunc(c.Arguments, func(in agentInput) bool { return in.Passthrough })
		optIn := c.Agent != nil && *c.Agent
		ac.offered = !underOff && (optIn || (!c.Hidden && c.Deprecated == "" && ac.leaf && !passthrough))
		var flagEffects, toolFlagEffects []*Effects
		for _, f := range c.Flags {
			if f.Effects != nil {
				flagEffects = append(flagEffects, f.Effects)
				if toolFlagOffered(f) {
					toolFlagEffects = append(toolFlagEffects, f.Effects)
				}
			}
			switch f.Role {
			case "machine-output":
				if c.Output != nil {
					ac.machine = machineOutputArgv(f)
				}
			case "dry-run":
				ac.dryRun = longIdentifier(f)
			case "confirm":
				ac.confirm = longIdentifier(f)
			}
		}
		ac.effects = worstEffects(c.Effects, flagEffects)
		ac.toolEffects = worstEffects(c.Effects, toolFlagEffects)
		a.commands = append(a.commands, ac)
	}
	return a, nil
}

// offered returns the commands offered to agents, in contract order.
func (a *agentProgram) offered() []agentCommand {
	var out []agentCommand
	for _, c := range a.commands {
		if c.offered {
			out = append(out, c)
		}
	}
	return out
}

// commandHelps maps each command path (space-joined; "" for the root) to its doc fields, for
// what the contract doesn't carry, such as examples.
func (p *program) commandHelps() map[string]cmdHelp {
	out := map[string]cmdHelp{"": p.rootHelp}
	var walk func(nodes []rnode, path []string)
	walk = func(nodes []rnode, path []string) {
		for _, n := range nodes {
			names := append(slices.Clone(path), n.name)
			out[strings.Join(names, " ")] = n.help
			walk(n.children, names)
		}
	}
	walk(p.tree, nil)
	return out
}

// toolFlagOffered reports whether a flag is a tool parameter: not hidden, deprecated, secret,
// short-circuit, the directory flag or read `from:` a file or stdin unless it opts in with
// `agent: true`, never with `agent: false`, and never the machine-output flag.
func toolFlagOffered(f agentInput) bool {
	if f.Role == "machine-output" {
		return false
	}
	return inputOffered(f, f.ShortCircuit || f.Role == "chdir")
}

// inputOffered applies the `agent` default and opt-in to one input.
func inputOffered(in agentInput, outByDefault bool) bool {
	if in.Agent != nil {
		return *in.Agent
	}
	return !outByDefault && !in.Hidden && in.Deprecated == "" && !in.Secret && len(in.From) == 0
}

// machineOutputArgv is what a caller adds to the command line for JSON output: the bool flag
// itself, or the string flag with its role_value attached.
func machineOutputArgv(f agentInput) []string {
	id := longIdentifier(f)
	if f.RoleValue != "" {
		return []string{id + "=" + f.RoleValue}
	}
	return []string{id}
}

// longIdentifier returns the identifier a caller writes for a flag, as rotini.ArgvOf picks it:
// its first long identifier that isn't deprecated, else its first short one that isn't, else
// its first.
func longIdentifier(f agentInput) string {
	for _, long := range []bool{true, false} {
		for _, id := range f.Identifiers {
			if strings.HasPrefix(id, "--") == long && !slices.Contains(f.DeprecatedIdentifiers, id) {
				return id
			}
		}
	}
	if len(f.Identifiers) > 0 {
		return f.Identifiers[0]
	}
	return ""
}
