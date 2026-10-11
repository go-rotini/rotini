package codegen

import (
	"fmt"
	"net/url"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

// This file holds the rules for what the generated pages show beyond a command's inputs: long
// input descriptions, stability markers, help topics and exit-code links, plus the conf rules
// for install-ready files and the config file schemas.

// pageInput is one input of a command, as the page rules see it.
type pageInput struct {
	ptr, subject         string
	summary, description string
	stability            string
	required             bool
}

// eachPageInput visits every flag, argument, env and config input of a command.
func eachPageInput(c *Command, ptr string, visit func(pageInput)) {
	required := func(s *InputSchema) bool { return s != nil && s.Required }
	for i, f := range c.Flags {
		visit(pageInput{ptr: fmt.Sprintf("%s/flags/%d", ptr, i), subject: fmt.Sprintf("flag %q", flagIdentifiers(f)[0]),
			summary: f.Summary, description: f.Description, stability: f.Stability, required: required(f.Schema)})
	}
	for i, a := range c.Arguments {
		visit(pageInput{ptr: fmt.Sprintf("%s/arguments/%d", ptr, i), subject: fmt.Sprintf("argument %q", a.Name),
			summary: a.Summary, description: a.Description, stability: a.Stability, required: required(a.Schema)})
	}
	for i, e := range c.Env {
		visit(pageInput{ptr: fmt.Sprintf("%s/env/%d", ptr, i), subject: fmt.Sprintf("env %q", e.Name),
			summary: e.Summary, description: e.Description, stability: e.Stability, required: required(e.Schema)})
	}
	for i, cfg := range c.Config {
		visit(pageInput{ptr: fmt.Sprintf("%s/config/%d", ptr, i), subject: fmt.Sprintf("config %q", cfg.Name),
			summary: cfg.Summary, description: cfg.Description, stability: cfg.Stability, required: required(cfg.Schema)})
	}
}

// lintInputDescription warns about an input with a description but no summary: help shows only
// the summary, so its row there would be empty.
func lintInputDescription(spec *Spec) []error {
	var problems []error
	walkCommandsAt(spec, func(c *Command, path, ptr string) {
		eachPageInput(c, ptr, func(in pageInput) {
			if in.description != "" && in.summary == "" {
				problems = append(problems, &problem{kind: "spec", ptr: in.ptr + "/description", loc: "command " + path, sev: severityWarning,
					msg: in.subject + ": sets `description` but no `summary`; help shows only the summary, so its row there is empty; add a one-line summary"})
			}
		})
	})
	return problems
}

// lintStability warns about stability markers that promise more than they can: an item marked
// more stable than the command it belongs to (it is as unstable as that command), and a
// required experimental input on a command that isn't experimental, which makes the command
// depend on something that may go away.
func lintStability(spec *Spec) []error {
	var problems []error
	var walk func(c *Command, path, ptr, inherited, from string)
	walk = func(c *Command, path, ptr, inherited, from string) {
		warn := func(at, msg string) {
			problems = append(problems, &problem{kind: "spec", ptr: at, loc: "command " + path, sev: severityWarning, msg: msg})
		}
		if c.Stability != "" && inherited != "" && stabilityRank[c.Stability] > stabilityRank[inherited] {
			warn(ptr+"/stability", fmt.Sprintf("declares stability %q but inherits %s from %s; a command is as unstable as its ancestors; remove `stability` or make it %q", c.Stability, inherited, from, inherited))
		}
		effective, source := inherited, from
		if c.Stability != "" && stabilityRank[c.Stability] < stabilityRank[inherited] {
			effective, source = c.Stability, "command "+path
		}
		eachPageInput(c, ptr, func(in pageInput) {
			switch {
			case in.stability != "" && effective != "" && stabilityRank[in.stability] > stabilityRank[effective]:
				warn(in.ptr+"/stability", fmt.Sprintf("%s: declares stability %q but inherits %s from %s; an input is as unstable as its command; remove `stability` or make it %q", in.subject, in.stability, effective, source, effective))
			case in.stability == "experimental" && in.required && effective != "experimental":
				warn(in.ptr+"/stability", in.subject+": is required and experimental on a command that isn't experimental, so the command depends on an input that may change or go away; make it optional, or mark the command experimental too")
			}
		})
		for i := range c.Commands {
			child := &c.Commands[i]
			seg := child.Name
			if seg == "" {
				seg = child.Ref
			}
			walk(child, path+"/"+seg, fmt.Sprintf("%s/commands/%d", ptr, i), effective, source)
		}
	}
	name := spec.Command.Name
	if name == "" {
		name = "(root)"
	}
	walk(&spec.Command, name, rootPointer, "", "")
	return problems
}

// lintTopics checks help topics: they are declared on the root, each name once, with exactly
// one of `body` and `generate`, and no name a root command, alias or declared plugin also
// answers to (`help <name>` would be ambiguous).
func lintTopics(spec *Spec) []error {
	var problems []error
	walkCommandsAt(spec, func(c *Command, path, ptr string) {
		if c != &spec.Command && len(c.Topics) > 0 {
			problems = append(problems, &problem{kind: "spec", ptr: ptr + "/topics", loc: "command " + path,
				msg: "sets `topics`, which only the root command declares; move the topics to the root"})
		}
	})
	root := &spec.Command
	taken := map[string]string{}
	for _, c := range root.Commands {
		if c.Name != "" {
			taken[c.Name] = fmt.Sprintf("the command %q", c.Name)
		}
		for _, a := range slices.Concat(c.Aliases, c.HiddenAliases) {
			taken[a] = fmt.Sprintf("an alias of the command %q", c.Name)
		}
	}
	for _, pl := range root.Plugins {
		taken[pl.Name] = fmt.Sprintf("the plugin %q", pl.Name)
		for _, a := range pl.Aliases {
			taken[a] = fmt.Sprintf("an alias of the plugin %q", pl.Name)
		}
	}
	loc := rootLabel(spec)
	seen := map[string]bool{}
	environment := false
	for i, t := range root.Topics {
		at := fmt.Sprintf("%s/topics/%d", rootPointer, i)
		add := func(key, msg string) {
			p := at
			if key != "" {
				p += "/" + key
			}
			problems = append(problems, &problem{kind: "spec", ptr: p, loc: loc, msg: fmt.Sprintf("topic %q: %s", t.Name, msg)})
		}
		if seen[t.Name] {
			add("name", "is declared twice; give each topic its own name")
		}
		seen[t.Name] = true
		if what, ok := taken[t.Name]; ok {
			add("name", fmt.Sprintf("has the name of %s, so `help %s` would be ambiguous; rename the topic", what, t.Name))
		}
		switch {
		case t.Body == "" && t.Generate == "":
			add("", "has neither `body` nor `generate`; write its text in `body`, or set `generate`")
		case t.Body != "" && t.Generate != "":
			add("body", "sets both `body` and `generate`; keep one")
		}
		if t.Generate == topicGenerateEnvironment {
			if environment {
				add("generate", "generates the environment page a second time; keep one")
			}
			environment = true
		}
	}
	return problems
}

// lintDocsURL rejects an exit code's docs_url that is not an absolute http or https link with a
// host; the pages print it as a link.
func lintDocsURL(spec *Spec) []error {
	var problems []error
	walkCommandsAt(spec, func(c *Command, path, ptr string) {
		for i, e := range c.ExitStatus {
			if e.DocsUrl == "" {
				continue
			}
			u, err := url.Parse(e.DocsUrl)
			if err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" {
				continue
			}
			problems = append(problems, &problem{kind: "spec", ptr: fmt.Sprintf("%s/exit_status/%d/docs_url", ptr, i), loc: "command " + path,
				msg: fmt.Sprintf("exit_status code %d: docs_url %q is not an absolute http or https link with a host; write the full address, such as https://example.com/errors/%d", e.Code, e.DocsUrl, e.Code)})
		}
	})
	return problems
}

// installFeatures are the features install_dir applies to.
var installFeatures = map[string]bool{"completion": true, "man": true}

// lintInstallDir rejects install_dir on a feature other than completion and man, where nothing
// would be written, and an install_dir that escapes the module.
func lintInstallDir(conf *Conf) []error {
	if conf.Generate == nil {
		return nil
	}
	var problems []error
	for i, f := range conf.Generate.Features {
		if f.InstallDir == "" {
			continue
		}
		loc := "generate.features." + f.Type + ".install_dir"
		if !installFeatures[f.Type] {
			problems = append(problems, &problem{kind: "conf", ptr: featurePointer(i) + "/install_dir", loc: loc,
				msg: "`install_dir` writes installable completion scripts and man pages, so it applies only to the completion and man features; move it there or remove it"})
			continue
		}
		if why := escapesModule(f.InstallDir); why != "" {
			problems = append(problems, &problem{kind: "conf", ptr: featurePointer(i) + "/install_dir", loc: loc,
				msg: fmt.Sprintf("%q %s", f.InstallDir, why)})
		}
	}
	return problems
}

// lintSchemaDirs rejects a generate.schemas output or config directory that is absolute or
// escapes the module root.
func lintSchemaDirs(conf *Conf) []error {
	if conf.Generate == nil || conf.Generate.Schemas == nil {
		return nil
	}
	var problems []error
	check := func(label, dir string) {
		if dir == "" {
			return
		}
		if why := escapesModule(dir); why != "" {
			problems = append(problems, &problem{kind: "conf", ptr: "/generate/schemas/" + label + "/dir",
				loc: "generate.schemas." + label + ".dir", msg: fmt.Sprintf("%q %s", dir, why)})
		}
	}
	if s := conf.Generate.Schemas.Output; s != nil {
		check("output", s.Dir)
	}
	if s := conf.Generate.Schemas.Config; s != nil {
		check("config", s.Dir)
	}
	return problems
}

// escapesModule says why a conf path can't be written relative to the module root, or "".
func escapesModule(p string) string {
	switch clean := path.Clean(filepath.ToSlash(p)); {
	case path.IsAbs(clean) || filepath.IsAbs(p):
		return "must be module-root-relative, not absolute"
	case clean == ".." || strings.HasPrefix(clean, "../"):
		return "must resolve under the module root"
	default:
		return ""
	}
}
