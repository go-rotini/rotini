package codegen

// Each flag set a command uses becomes one generated struct, <Root><Set>FlagSet, embedded in
// the command's <Prefix>Flags after its own fields. Promotion keeps the inputs paths flat
// (in.TaskrList.Flags.Format), so moving a flag into a set changes no handler code, and a
// handler can pass the set's struct to a helper shared by every command that uses it.

// flagSetBlock is one generated flag set struct.
type flagSetBlock struct {
	typ    string
	fields []fieldDef
}

// templateFlagSet is a flag set struct as the cmd and models templates render it.
type templateFlagSet struct {
	Type   string
	Fields []templateInputField
}

// flagSetType names the struct generated for the set called name.
func (gp *program) flagSetType(name string) string { return gp.rootPascal + name + "FlagSet" }

// embedFlagSets moves the fields of gc's flags that come from flag sets out of its own
// fields, into one embedded struct per set, recording each set's struct the first time a
// command uses it. c is the command's (expanded) spec.
func (gp *program) embedFlagSets(gc *genCommand, c *Command) {
	if len(c.Use) == 0 || len(gp.flagSets) == 0 {
		return
	}
	origins := flagOrigins(c, gp.flagSets)
	var own []fieldDef
	members := map[string][]fieldDef{}
	for i, f := range gc.flags {
		if i >= len(origins) || origins[i].set == "" {
			own = append(own, f)
			continue
		}
		members[origins[i].set] = append(members[origins[i].set], f)
	}
	for _, name := range c.Use {
		fields, ok := members[name]
		if !ok {
			continue
		}
		typ := gp.flagSetType(name)
		gc.embeds = append(gc.embeds, typ)
		if gp.flagSetEmitted == nil {
			gp.flagSetEmitted = map[string]bool{}
		}
		if !gp.flagSetEmitted[typ] {
			gp.flagSetEmitted[typ] = true
			gp.flagSetBlocks = append(gp.flagSetBlocks, flagSetBlock{typ: typ, fields: fields})
		}
	}
	gc.flags = own
}

// flagSetTemplates is the program's flag set structs for the templates.
func flagSetTemplates(gp *program) []templateFlagSet {
	out := make([]templateFlagSet, 0, len(gp.flagSetBlocks))
	for _, s := range gp.flagSetBlocks {
		out = append(out, templateFlagSet{Type: s.typ, Fields: toTemplateFields(s.fields)})
	}
	return out
}

// addFlagSetImports records the imports the flag set structs' field types need.
func addFlagSetImports(gp *program, set map[string]bool) {
	for _, s := range gp.flagSetBlocks {
		inputFields{flags: s.fields}.addImports(set)
	}
}

// flagSetTypeNames lists the flag set structs, for the models package's aliases.
func flagSetTypeNames(gp *program) []string {
	out := make([]string, 0, len(gp.flagSetBlocks))
	for _, s := range gp.flagSetBlocks {
		out = append(out, s.typ)
	}
	return out
}

// setsOr is the flag sets of the spec declaring ctx's subtree, else own, the program's.
func (ctx composeCtx) setsOr(own map[string]FlagSet) map[string]FlagSet {
	if ctx.flagSets != nil {
		return ctx.flagSets
	}
	return own
}

// flagSetNames is the set each of c's flags came from, "" for its own, or nil when c uses no
// set.
func flagSetNames(c *Command, sets map[string]FlagSet) []string {
	if len(c.Use) == 0 || len(sets) == 0 {
		return nil
	}
	origins := flagOrigins(c, sets)
	out := make([]string, len(origins))
	for i, o := range origins {
		out[i] = o.set
	}
	return out
}
