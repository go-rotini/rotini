package codegen

// Package-wide spec tree-traversal and input/schema enumeration helpers, shared by the
// lint rules, the generator, and validation. Kept in one place so a rule, the composer,
// or a derivation never re-implements the same recursive walk.

// walkCommands visits every command in the spec depth-first (pre-order), passing a
// display path — "root/child/grandchild", using a child's $ref segment when it has
// no name (and "(root)" for an unnamed root). The rules call this instead of each
// re-defining the same recursive walk.
func walkCommands(spec *Spec, visit func(c *Command, path string)) {
	var walk func(c *Command, path string)
	walk = func(c *Command, path string) {
		visit(c, path)
		for i := range c.Commands {
			child := &c.Commands[i]
			seg := child.Name
			if seg == "" {
				seg = child.Ref
			}
			walk(child, path+"/"+seg)
		}
	}
	name := spec.Command.Name
	if name == "" {
		name = "(root)"
	}
	walk(&spec.Command, name)
}

// allConfigFiles gathers every command's config_files sources across the tree (each
// command's inputs carry their own, cascading per D-W3.1), flattened for the global
// BindMeta and the declared-name lints.
func allConfigFiles(spec *Spec) []ConfigurationFile {
	var out []ConfigurationFile
	walkCommands(spec, func(c *Command, _ string) {
		if c.inputs() != nil {
			out = append(out, c.inputs().ConfigFiles...)
		}
	})
	return out
}

// walkChains visits every command paired with its ancestor chain (root → command,
// the command last). It is the chain-aware counterpart of walkCommands, for rules
// that must reason about what's in scope via the cascade (config_files, file: pins).
func walkChains(spec *Spec, visit func(chain []*Command, path string)) {
	var walk func(c *Command, ancestors []*Command, path string)
	walk = func(c *Command, ancestors []*Command, path string) {
		chain := make([]*Command, len(ancestors)+1) // fresh slice → no sibling clobber across recursion
		copy(chain, ancestors)
		chain[len(ancestors)] = c
		visit(chain, path)
		for i := range c.Commands {
			child := &c.Commands[i]
			seg := child.Name
			if seg == "" {
				seg = child.Ref
			}
			walk(child, chain, path+"/"+seg)
		}
	}
	name := spec.Command.Name
	if name == "" {
		name = "(root)"
	}
	walk(&spec.Command, nil, name)
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

// walkSchemaRefs invokes visit for a schema and recurses into its object properties and
// array items, so a "$ref" at any nesting depth is seen.
func walkSchemaRefs(b BaseSchema, visit func(BaseSchema)) {
	visit(b)
	for _, p := range b.Properties {
		walkSchemaRefs(p.BaseSchema, visit)
	}
	if b.Items != nil {
		walkSchemaRefs(b.Items.BaseSchema, visit)
	}
}

// walkSchemaImports records (type, import) for a schema and recurses into its object
// properties and array items.
func walkSchemaImports(b BaseSchema, record func(typ, imp string)) {
	record(b.Type, b.Import)
	for _, p := range b.Properties {
		walkSchemaImports(p.BaseSchema, record)
	}
	if b.Items != nil {
		walkSchemaImports(b.Items.BaseSchema, record)
	}
}
