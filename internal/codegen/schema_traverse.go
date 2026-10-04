package codegen

import "fmt"

// Spec tree-traversal and input enumeration helpers shared by lint, validation and the
// generator.

// walkCommands visits every command depth-first (pre-order) with a display path such as
// "root/child/grandchild". An unnamed child contributes its $ref; an unnamed root is
// "(root)". Rules that report problems use [walkCommandsAt].
func walkCommands(spec *Spec, visit func(c *Command, path string)) {
	walkCommandsAt(spec, func(c *Command, path, _ string) { visit(c, path) })
}

// rootPointer is the JSON pointer of the document's root command.
const rootPointer = "/command"

// walkCommandsAt is [walkCommands] plus each command's JSON pointer ("/command",
// "/command/commands/0/commands/2"), which [locateProblems] resolves to a source line and
// column.
func walkCommandsAt(spec *Spec, visit func(c *Command, path, ptr string)) {
	var walk func(c *Command, path, ptr string)
	walk = func(c *Command, path, ptr string) {
		visit(c, path, ptr)
		for i := range c.Commands {
			child := &c.Commands[i]
			seg := child.Name
			if seg == "" {
				seg = child.Ref
			}
			walk(child, path+"/"+seg, fmt.Sprintf("%s/commands/%d", ptr, i))
		}
	}
	name := spec.Command.Name
	if name == "" {
		name = "(root)"
	}
	walk(&spec.Command, name, rootPointer)
}

// walkChainsAt is [walkCommandsAt] with each command's ancestor chain (root first, the
// command last), for rules that reason about cascaded scope such as config_files.
func walkChainsAt(spec *Spec, visit func(chain []*Command, path, ptr string)) {
	var walk func(c *Command, ancestors []*Command, path, ptr string)
	walk = func(c *Command, ancestors []*Command, path, ptr string) {
		chain := make([]*Command, len(ancestors)+1) // fresh slice so siblings do not share a backing array
		copy(chain, ancestors)
		chain[len(ancestors)] = c
		visit(chain, path, ptr)
		for i := range c.Commands {
			child := &c.Commands[i]
			seg := child.Name
			if seg == "" {
				seg = child.Ref
			}
			walk(child, chain, path+"/"+seg, fmt.Sprintf("%s/commands/%d", ptr, i))
		}
	}
	name := spec.Command.Name
	if name == "" {
		name = "(root)"
	}
	walk(&spec.Command, nil, name, rootPointer)
}

// chainConfigNames returns the config_files names declared anywhere along a chain.
func chainConfigNames(chain []*Command) map[string]bool {
	names := map[string]bool{}
	for _, c := range chain {
		if c.inputs() == nil {
			continue
		}
		for _, cf := range c.inputs().ConfigFiles {
			names[cf.Name] = true
		}
	}
	return names
}

// flagNames returns a command's declared flag names as a set and in declaration order (for
// suggestions).
func flagNames(c *Command) (known map[string]bool, ordered []string) {
	known = map[string]bool{}
	if c.inputs() == nil {
		return known, nil
	}
	ordered = make([]string, 0, len(c.inputs().Flags))
	for _, f := range c.inputs().Flags {
		known[f.Name] = true
		ordered = append(ordered, f.Name)
	}
	return known, ordered
}

// eachInputSchema visits each input schema across all channels (flag, argument, env,
// config, stdin) with its channel label and name ("" for stdin).
func eachInputSchema(in *Inputs, visit func(channel, name string, schema *InputSchema)) {
	if in == nil {
		return
	}
	for i := range in.Flags {
		visit("flag", in.Flags[i].Name, in.Flags[i].Schema)
	}
	for i := range in.Arguments {
		visit("argument", in.Arguments[i].Name, in.Arguments[i].Schema)
	}
	for i := range in.Env {
		visit("env", in.Env[i].Name, in.Env[i].Schema)
	}
	for i := range in.Config {
		visit("config", in.Config[i].Name, in.Config[i].Schema)
	}
	if in.Stdin != nil {
		visit("stdin", "", in.Stdin.Schema)
	}
}

// walkSchemaTree visits a schema and every schema nested in its properties and items, at any
// depth.
func walkSchemaTree(b BaseSchema, visit func(BaseSchema)) {
	visit(b)
	for _, p := range b.Properties {
		walkSchemaTree(p.BaseSchema, visit)
	}
	if b.Items != nil {
		walkSchemaTree(b.Items.BaseSchema, visit)
	}
}
