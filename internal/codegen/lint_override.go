package codegen

import (
	"cmp"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"
)

// The checks on what a spec overrides of the specs it mounts. They read each spec mounted by a
// local `$ref` (a mod:// spec is checked in its own module) and report in the file that writes
// the override.

// expandedSpec returns spec with every local `$ref` replaced by the command it mounts, as the
// composer builds it: the mounted root with the keys written beside the `$ref`, its commands
// with the overrides written there, and the commands added there. The named schemas of the
// mounted specs are added to the root's where their names are free. A spec mounting nothing
// is returned as it is.
func expandedSpec(spec *Spec, specPath string) *Spec {
	if !specHasRefs(spec) {
		return spec
	}
	abs, err := filepath.Abs(specPath)
	if err != nil {
		return spec
	}
	cp := *spec
	x := expander{seen: map[string]bool{filepath.Clean(abs): true}, schemas: maps.Clone(spec.Command.Schemas)}
	cp.Command.Commands = x.commands(spec.Command.Commands, filepath.Dir(abs))
	cp.Command.Schemas = x.schemas
	return &cp
}

type expander struct {
	seen    map[string]bool // the specs being expanded, to stop at a cycle
	schemas map[string]Schema
}

func (x *expander) commands(cmds []Command, base string) []Command {
	out := make([]Command, len(cmds))
	for i := range cmds {
		out[i] = x.command(cmds[i], base)
	}
	return out
}

func (x *expander) command(c Command, base string) Command {
	if c.Ref == "" {
		c.Commands = x.commands(c.Commands, base)
		return c
	}
	child, dir, locator, ok := readLocalRef(c.Ref, base, x.seen)
	if !ok {
		return c
	}
	x.seen[locator] = true
	defer delete(x.seen, locator)
	for name, s := range child.Command.Schemas {
		if _, taken := x.schemas[name]; !taken {
			if x.schemas == nil {
				x.schemas = map[string]Schema{}
			}
			x.schemas[name] = s
		}
	}
	m := overlayCommand(child.Command, c)
	m.Ref, m.Handler = "", nil
	own := x.commands(child.Command.Commands, dir)
	overrides, additions := splitOverrides(c.Commands, own)
	m.Commands = append(applyOverrideEntries(own, overrides), x.commands(additions, base)...)
	return m
}

// readLocalRef reads the spec a local `$ref` names, relative to base, unless it is on the chain
// being expanded (seen). ok is false for a mod:// or remote ref, and a spec that doesn't read.
func readLocalRef(ref, base string, seen map[string]bool) (spec *Spec, dir, locator string, ok bool) {
	if strings.HasPrefix(ref, modScheme) || isExternalLocator(ref) {
		return nil, "", "", false
	}
	locator, err := locateRef(base, ref)
	if err != nil || seen[locator] {
		return nil, "", "", false
	}
	spec, err = readSpec(locator)
	if err != nil {
		return nil, "", "", false
	}
	return spec, filepath.Dir(locator), locator, true
}

// splitOverrides sorts the entries written beside a `$ref` into overrides of the mounted spec's
// commands (own, already expanded) and additions, as the composer does.
func splitOverrides(beside, own []Command) (overrides, additions []Command) {
	for _, e := range beside {
		if e.Ref == "" && slices.ContainsFunc(own, func(c Command) bool { return c.Name == e.Name }) {
			overrides = append(overrides, e)
		} else {
			additions = append(additions, e)
		}
	}
	return overrides, additions
}

// applyOverrideEntries returns cmds with each override entry applied to the command it names,
// and its own entries to that command's commands. An entry naming none is left to lintOverrides.
func applyOverrideEntries(cmds, overrides []Command) []Command {
	if len(overrides) == 0 {
		return cmds
	}
	out := slices.Clone(cmds)
	for _, o := range overrides {
		i := slices.IndexFunc(out, func(c Command) bool { return c.Name == o.Name })
		if i < 0 || o.Ref != "" {
			continue
		}
		m := overlayCommand(out[i], o)
		m.Commands = applyOverrideEntries(out[i].Commands, o.Commands)
		out[i] = m
	}
	return out
}

// mountedOverride is an override entry in the spec being linted, the command it overrides as
// the mounted spec declares it, and where the entry is written.
type mountedOverride struct {
	entry, target *Command
	ptr, path     string
}

// eachOverride visits every override entry spec writes for the specs it mounts by a local
// `$ref`, nested ones included, and each entry under an override that can't apply (it names
// no command of the command overridden, or mounts a spec), with the reason.
func eachOverride(spec *Spec, specPath string, visit func(o mountedOverride), invalid func(ptr, path, msg string)) {
	abs, err := filepath.Abs(specPath)
	if err != nil {
		return
	}
	base := filepath.Dir(abs)
	var within func(entry, target *Command, ptr, path string)
	within = func(entry, target *Command, ptr, path string) {
		visit(mountedOverride{entry: entry, target: target, ptr: ptr, path: path})
		for j := range entry.Commands {
			sub := &entry.Commands[j]
			sptr := fmt.Sprintf("%s/commands/%d", ptr, j)
			spath := path + "/" + cmp.Or(sub.Name, sub.Ref)
			i := slices.IndexFunc(target.Commands, func(c Command) bool { return c.Name == sub.Name })
			switch {
			case sub.Ref != "":
				invalid(sptr+"/$ref", path, fmt.Sprintf("mounts %q inside an override of a mounted command; mount a spec beside a `$ref`, or in the spec that declares %s", sub.Ref, quotedPath(path)))
			case i < 0:
				invalid(sptr, path, fmt.Sprintf("overrides %s, which declares no command %q; an entry under an override names a command to override, and a command can be added only beside a `$ref`", quotedPath(path), sub.Name))
			default:
				within(sub, &target.Commands[i], sptr, spath)
			}
		}
	}
	walkCommandsAt(spec, func(c *Command, path, ptr string) {
		if c.Ref == "" || ptr == rootPointer {
			return
		}
		// The ref's directory: entries written beside a $ref nested under another one still
		// resolve against this spec's directory.
		child, dir, locator, ok := readLocalRef(c.Ref, base, map[string]bool{})
		if !ok {
			return
		}
		x := expander{seen: map[string]bool{filepath.Clean(abs): true, locator: true}}
		own := x.commands(child.Command.Commands, dir)
		if c.Name == "" {
			path = strings.TrimSuffix(path, "/"+c.Ref) + "/" + child.Command.Name
		}
		for i := range c.Commands {
			e := &c.Commands[i]
			j := slices.IndexFunc(own, func(o Command) bool { return o.Name == e.Name })
			if e.Ref != "" || j < 0 {
				continue
			}
			within(e, &own[j], fmt.Sprintf("%s/commands/%d", ptr, i), path+"/"+e.Name)
		}
	})
}

// quotedPath writes a slash-joined command path as the user types it, in backticks.
func quotedPath(path string) string {
	return "`" + strings.ReplaceAll(path, "/", " ") + "`"
}

// lintOverrides checks what spec overrides of the specs it mounts: an override sets only keys a
// mounting spec may change, and names a command of the command it overrides. Each problem is
// placed at the entry in spec's own file.
func lintOverrides(spec *Spec, specPath string) []error {
	var problems []error
	eachOverride(spec, specPath, func(o mountedOverride) {
		for _, key := range overrideOnlyKeys(o.entry, false) {
			problems = append(problems, &problem{kind: "spec", ptr: o.ptr + "/" + key, loc: "command " + o.path,
				msg: fmt.Sprintf("overrides the mounted command %s, so it can't set %#q; the command runs the handler of the spec that declares it, built against that spec's own inputs and output, so declare it there",
					quotedPath(o.path), key)})
		}
	}, func(ptr, path, msg string) {
		problems = append(problems, &problem{kind: "spec", ptr: ptr, loc: "command " + path, msg: msg})
	})
	return append(problems, lintMountedExamples(spec, specPath)...)
}

// lintMountedExamples parses the examples of spec that reach a command mounted by a local `$ref`
// against the mounted command, overrides included; lintExamplesParse accepts whatever follows a
// mounted command.
func lintMountedExamples(spec *Spec, specPath string) []error {
	expanded := expandedSpec(spec, specPath)
	if expanded == spec {
		return nil
	}
	var problems []error
	plain, full := newExampleChecker(spec), newExampleChecker(expanded)
	walkCommandsAt(spec, func(c *Command, cmdPath, ptr string) {
		// An unnamed $ref's path segment is the ref, so name it in the message instead.
		subject := ""
		if c.Ref != "" && c.Name == "" {
			cmdPath, subject = strings.TrimSuffix(cmdPath, "/"+c.Ref), fmt.Sprintf("$ref %q: ", c.Ref)
		}
		for i, line := range c.Examples {
			if msg, unclosed := plain.complaint(line); msg != "" || unclosed {
				continue
			}
			if msg, _ := full.complaint(line); msg != "" {
				problems = append(problems, &problem{kind: "spec", ptr: fmt.Sprintf("%s/examples/%d", ptr, i), loc: "command " + cmdPath,
					msg: fmt.Sprintf("%sexample %q does not parse: %s", subject, line, msg)})
			}
		}
	})
	return problems
}

// overrideLintView returns spec as the single-file rules should see it: each override entry
// carries the aliases and deprecation keys of the command it overrides that it doesn't write
// itself, so a rule relating them sees what the command ends up with. filled holds the pointer
// of each key copied in, whose problems belong to the mounted spec's own file.
func overrideLintView(spec *Spec, specPath string) (view *Spec, filled map[string]bool) {
	if !specHasRefs(spec) {
		return spec, nil
	}
	cp := *spec
	cp.Command.Commands = cloneCommands(spec.Command.Commands)
	filled = map[string]bool{}
	eachOverride(&cp, specPath, func(o mountedOverride) {
		e, t := o.entry, o.target
		fill := func(key string, empty bool, copyIn func()) {
			if empty {
				copyIn()
				filled[o.ptr+"/"+key] = true
			}
		}
		fill("aliases", e.Aliases == nil && t.Aliases != nil, func() { e.Aliases = t.Aliases })
		fill("hidden_aliases", e.HiddenAliases == nil && t.HiddenAliases != nil, func() { e.HiddenAliases = t.HiddenAliases })
		fill("deprecated", e.Deprecated == "" && t.Deprecated != "", func() { e.Deprecated = t.Deprecated })
		fill("deprecated_since", e.DeprecatedSince == "" && t.DeprecatedSince != "", func() { e.DeprecatedSince = t.DeprecatedSince })
		fill("removed_in", e.RemovedIn == "" && t.RemovedIn != "", func() { e.RemovedIn = t.RemovedIn })
		fill("deprecated_identifiers", e.DeprecatedIdentifiers == nil && t.DeprecatedIdentifiers != nil,
			func() { e.DeprecatedIdentifiers = t.DeprecatedIdentifiers })
		fill("deprecated_identifiers_removed_in", e.DeprecatedIdentifiersRemovedIn == nil && t.DeprecatedIdentifiersRemovedIn != nil,
			func() { e.DeprecatedIdentifiersRemovedIn = t.DeprecatedIdentifiersRemovedIn })
	}, func(string, string, string) {})
	return &cp, filled
}

// cloneCommands copies a command tree's commands lists, so a view can change its entries.
func cloneCommands(cmds []Command) []Command {
	out := slices.Clone(cmds)
	for i := range out {
		out[i].Commands = cloneCommands(out[i].Commands)
	}
	return out
}

// dropFilled removes the problems placed at a key overrideLintView copied in.
func dropFilled(problems []error, filled map[string]bool) []error {
	if len(filled) == 0 {
		return problems
	}
	return slices.DeleteFunc(problems, func(err error) bool {
		p := &problem{}
		return errors.As(err, &p) && filled[p.ptr]
	})
}
