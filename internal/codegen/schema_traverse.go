package codegen

import "fmt"

// Package-wide spec tree-traversal and input/schema enumeration helpers, shared by the
// lint rules, the generator, and validation. Kept in one place so a rule, the composer,
// or a derivation never re-implements the same recursive walk.

// walkCommands visits every command in the spec depth-first (pre-order), passing a
// display path — "root/child/grandchild", using a child's $ref segment when it has
// no name (and "(root)" for an unnamed root). The rules call this instead of each
// re-defining the same recursive walk.
//
// Use [walkCommandsAt] when the rule reports a problem: it supplies the JSON pointer that
// gives the message a file:line:col.
func walkCommands(spec *Spec, visit func(c *Command, path string)) {
	walkCommandsAt(spec, func(c *Command, path, _ string) { visit(c, path) })
}

// rootPointer is the JSON pointer of the document's root command.
const rootPointer = "/command"

// walkCommandsAt is [walkCommands] plus each command's JSON pointer ("/command",
// "/command/commands/0/commands/2"), which [locateProblems] resolves to a line and column in
// the file the author actually wrote.
//
// Every rule that reports a problem uses this: a message naming "command demo/build" makes a
// reader search for it, and the same message at .rotini.spec.yaml:41:7 does not. The pointer
// addresses the COMMAND, not the offending key inside it, which is one hop coarser than a
// schema violation's pointer and costs each rule nothing to carry.
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

// walkChainsAt visits every command paired with its ancestor chain (root → command, the
// command last) and its JSON pointer. It is the chain-aware counterpart of [walkCommandsAt],
// for rules that must reason about what is in scope via the cascade (config_files, file:
// pins).
func walkChainsAt(spec *Spec, visit func(chain []*Command, path, ptr string)) {
	var walk func(c *Command, ancestors []*Command, path, ptr string)
	walk = func(c *Command, ancestors []*Command, path, ptr string) {
		chain := make([]*Command, len(ancestors)+1) // fresh slice → no sibling clobber across recursion
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

// chainConfigNames is the set of config_files logical names in scope for a chain —
// every name declared on the command or any ancestor (the cascade's name space).
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

// flagNames returns the set of a command's declared flag names plus an ordered slice
// of them (for closestName "did you mean?" suggestions). Shared by the flag_groups
// and flag_dependencies rules.
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

// eachInputSchema visits each declared input schema of a command across all
// channels (flag, argument, env, config, stdin), passing the channel label and the
// input's logical name ("" for stdin). It is the single place the schema-walking
// rules enumerate a command's input channels.
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

// walkSchemaTree invokes visit for a schema and every schema nested in it — its object
// properties and array items, at any depth — so a check (a "$ref", a pattern_message) sees them
// all.
func walkSchemaTree(b BaseSchema, visit func(BaseSchema)) {
	visit(b)
	for _, p := range b.Properties {
		walkSchemaTree(p.BaseSchema, visit)
	}
	if b.Items != nil {
		walkSchemaTree(b.Items.BaseSchema, visit)
	}
}
