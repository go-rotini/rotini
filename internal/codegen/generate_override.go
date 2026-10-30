package codegen

import (
	"fmt"
	"maps"
	"slices"
	"strings"
)

// A spec mounting another with `$ref` can override, for its own tree, any command-level key of
// the mounted command (written beside the `$ref`) and of the commands below it (written as an
// entry of the `commands:` beside the `$ref`, named like the command it overrides). With
// several layers of composition, the spec nearest the root wins.

// override is one override entry, and the underscore path of the root of the spec writing it
// ("" for the program's own), which its `replaced_by` is relative to.
type override struct {
	cmd      Command
	specRoot string
}

// overridesByName indexes override entries, written in the spec rooted at specRoot, by the name
// of the command each overrides.
func overridesByName(entries []Command, specRoot string) map[string][]override {
	if len(entries) == 0 {
		return nil
	}
	out := map[string][]override{}
	for _, e := range entries {
		out[e.Name] = append(out[e.Name], override{cmd: e, specRoot: specRoot})
	}
	return out
}

// applyOverrides applies the overrides ctx holds for c, in order, and returns c as overridden,
// the overrides for the commands below it, and its resolved replacement.
func (gp *program) applyOverrides(c Command, ctx composeCtx) (Command, map[string][]override, replacement) {
	replacedBy := gp.commandReplacement(c.ReplacedBy, ctx.specRoot)
	var below map[string][]override
	for _, o := range ctx.overrides[c.Name] {
		c = overlayCommand(c, o.cmd)
		if o.cmd.ReplacedBy != "" {
			replacedBy = gp.commandReplacement(o.cmd.ReplacedBy, o.specRoot)
		}
		for _, e := range o.cmd.Commands {
			if below == nil {
				below = map[string][]override{}
			}
			below[e.Name] = append(below[e.Name], override{cmd: e, specRoot: o.specRoot})
		}
	}
	return c, below, replacedBy
}

// checkOverrides reports an override entry below the command at path that names none of its
// commands, mounts a spec, or sets a key it can't.
func checkOverrides(overrides map[string][]override, children []rnode, path []string) error {
	where := strings.Join(path, " ")
	for _, name := range slices.Sorted(maps.Keys(overrides)) {
		for _, o := range overrides[name] {
			if o.cmd.Ref != "" {
				return &refEntryError{fmt.Errorf("an override of a command under %q mounts %q; a spec can be mounted only beside a `$ref` or in the spec that declares %q", where, o.cmd.Ref, where)}
			}
			if keys := overrideOnlyKeys(&o.cmd, false); len(keys) > 0 {
				return &refEntryError{fmt.Errorf("the override of the mounted command %q sets %#q, which the mounted command's handler depends on; declare it in the spec that declares %q",
					where+" "+name, keys[0], name)}
			}
		}
		if !slices.ContainsFunc(children, func(n rnode) bool { return n.name == name }) {
			return &refEntryError{fmt.Errorf("an override names the command %q under %q, which has no such command; a command can be added only beside a `$ref`",
				name, where)}
		}
	}
	return nil
}

// splitBeside sorts the entries written beside a `$ref` into overrides, those without a `$ref`
// named like one of the mounted spec's own commands (own, whose relative refs resolve against
// base), and additions. An override that sets a key it can't is an error.
func (gp *program) splitBeside(beside, own []Command, base, moduleName string) (overrides, additions []Command, err error) {
	if len(beside) == 0 {
		return nil, nil, nil
	}
	names := map[string]bool{}
	for i := range own {
		names[writtenName(&own[i], base, moduleName)] = true
	}
	for _, e := range beside {
		if e.Ref != "" || !names[e.Name] {
			additions = append(additions, e)
			continue
		}
		if keys := overrideOnlyKeys(&e, false); len(keys) > 0 {
			return nil, nil, &refEntryError{fmt.Errorf("the override of the mounted command %q sets %#q, which the mounted command's handler depends on; declare it in the spec that declares %q",
				e.Name, keys[0], e.Name)}
		}
		overrides = append(overrides, e)
	}
	return overrides, additions, nil
}

// writtenName is the name a command entry is invoked by: its own, or for an unnamed `$ref`, the
// name of the spec it mounts ("" when that spec doesn't read).
func writtenName(c *Command, base, moduleName string) string {
	if c.Name != "" || c.Ref == "" {
		return c.Name
	}
	locator, err := locateRef(base, c.Ref)
	if err != nil {
		return ""
	}
	rr, err := loadRef(locator, moduleName)
	if err != nil {
		return ""
	}
	return rr.spec.Command.Name
}

// overrideOnlyKeys lists the keys c sets that a mounting spec can't override, because the
// mounted command's handler, built in the composed spec, depends on them. atRef is true for a
// `$ref` node itself, which may also set `handler:`; an override of a command below it can't,
// nor nest a `$ref`. Nested override entries are checked where they apply.
func overrideOnlyKeys(c *Command, atRef bool) []string {
	var keys []string
	add := func(set bool, key string) {
		if set {
			keys = append(keys, key)
		}
	}
	add(len(c.Flags) > 0, "flags")
	add(len(c.Arguments) > 0, "arguments")
	add(len(c.Env) > 0, "env")
	add(len(c.Config) > 0, "config")
	add(len(c.ConfigFiles) > 0, "config_files")
	add(c.Stdin != nil, "stdin")
	add(len(c.FlagGroups) > 0, "flag_groups")
	add(len(c.FlagDependencies) > 0, "flag_dependencies")
	add(c.Output != nil, "output")
	add(c.OutputStream, "output_stream")
	add(len(c.Plugins) > 0, "plugins")
	add(c.PluginDiscovery != nil, "plugin_discovery")
	add(c.Passthrough, "passthrough")
	add(c.OptionsFirst, "options_first")
	add(c.Filename != "", "filename")
	if !atRef {
		add(len(c.Use) > 0, "use")
		add(c.Handler != nil, "handler")
	}
	return keys
}
