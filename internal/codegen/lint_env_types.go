package codegen

import (
	"fmt"
	"strings"
)

// envSite is one input that reads an environment variable: an env input, or a flag's or
// argument's environment fallback. typ is how the input reads the variable's text: its
// declared type and the options that change parsing; see envReading.
type envSite struct {
	variable, typ      string
	ptr, path, channel string
	name               string
	own                bool   // declared by the chain's last command, where it is reported
	key                string // the key the variable is derived from, or the input's name
	explicit           bool   // named by `variable:` rather than derived from key
	at                 string // the pointer of what names the variable: `variable`, `key` or the name
}

// lintEnvVariableTypes warns when one environment variable feeds inputs that read it
// differently in one command chain (a command and its ancestors): a value right for one input
// can fail the other on every run that sets it.
func lintEnvVariableTypes(spec *Spec) []error {
	var problems []error
	said := map[string]bool{}
	walkChainsAt(spec, func(chain []*Command, path, ptr string) {
		sites := make([]envSite, 0, len(chain))
		for depth, c := range chain {
			sites = append(sites, envSitesOf(spec, c, depth == len(chain)-1, path, ptr)...)
		}
		first := map[string]envSite{}
		for _, s := range sites {
			prev, ok := first[s.variable]
			if !ok {
				first[s.variable] = s
				continue
			}
			if prev.typ == s.typ || !s.own || said[s.ptr+"\x00"+s.variable] {
				continue
			}
			said[s.ptr+"\x00"+s.variable] = true
			consequence := "a value right for one fails the other on every run that sets it"
			if s.typ == "string" || prev.typ == "string" {
				consequence = "any value suits the string, but one the other rejects fails every run that sets it"
			}
			p := inputProblem(s.at, s.path, s.channel, s.name, fmt.Sprintf(
				"reads environment variable %s as %s, but %s %q reads it as %s; %s, so give one its own variable",
				s.variable, s.typ, prev.channel, prev.name, prev.typ, consequence))
			p.sev = severityWarning
			problems = append(problems, p)
		}
	})
	return problems
}

// lintEnvVariableKeys warns when two inputs in one command chain read one environment variable
// for different keys: `a.b_c` and `a_b.c` both derive A_B_C, and a `variable:` can name what
// another input derives. Setting the variable then sets both. Inputs sharing a key mean one
// setting, and two that both name the variable with `variable:` share it on purpose; a variable
// read as two types is lintEnvVariableTypes's.
func lintEnvVariableKeys(spec *Spec) []error {
	var problems []error
	said := map[string]bool{}
	walkChainsAt(spec, func(chain []*Command, path, ptr string) {
		sites := make([]envSite, 0, len(chain))
		for depth, c := range chain {
			sites = append(sites, envSitesOf(spec, c, depth == len(chain)-1, path, ptr)...)
		}
		first := map[string]envSite{}
		for _, s := range sites {
			prev, ok := first[s.variable]
			if !ok {
				first[s.variable] = s
				continue
			}
			if prev.key == s.key || prev.typ != s.typ || (prev.explicit && s.explicit) || !s.own || said[s.ptr+"\x00"+s.variable] {
				continue
			}
			said[s.ptr+"\x00"+s.variable] = true
			p := inputProblem(s.at, s.path, s.channel, s.name, fmt.Sprintf(
				"reads environment variable %s, %s, and %s %q reads it too, %s; setting it sets both, so rename a key or give one its own `variable:`",
				s.variable, envSource(s), prev.channel, prev.name, envSource(prev)))
			p.sev = severityWarning
			problems = append(problems, p)
		}
	})
	return problems
}

// envSource says how a site comes to read its variable.
func envSource(s envSite) string {
	if s.explicit {
		return "named by `variable:`"
	}
	return fmt.Sprintf("derived from key %q", s.key)
}

// envSitesOf lists the environment variables c's inputs read. For an ancestor (own false)
// only its env inputs and cascading flags count: its other flags and arguments aren't read
// below it.
func envSitesOf(spec *Spec, c *Command, own bool, path, ptr string) []envSite {
	var sites []envSite
	prefix := spec.Command.EnvPrefix
	eachInputAt(c, ptr, func(channel, name, at string, schema *InputSchema) {
		if !own && (channel == "argument" || channel == "flag" && !cascadingFlag(c, name)) {
			return
		}
		var vars, key string
		switch channel {
		case "flag", "argument":
			key = flagReconKey(name, schema)
			vars = flagEnvVar(schema, key, prefix)
		case "env":
			if schema != nil && schema.Nesting != "" {
				return // a nested input reads a family of variables, not one
			}
			key = name
			vars = envVarName(EnvInput{Name: name, Schema: schema}, prefix)
		default:
			return
		}
		explicit := len(variables(schema)) > 0
		named := at + "/name"
		switch {
		case explicit:
			named = at + "/schema/variable"
		case schema != nil && schema.Key != "":
			named = at + "/schema/key"
		}
		typ := envReading(schema, spec.Command.Schemas)
		for v := range strings.SplitSeq(vars, ",") {
			if v != "" {
				sites = append(sites, envSite{variable: v, typ: typ, ptr: at, path: path, channel: channel, name: name, own: own,
					key: key, explicit: explicit, at: named})
			}
		}
	})
	return sites
}

// cascadingFlag reports whether c declares a cascading flag called name.
func cascadingFlag(c *Command, name string) bool {
	for _, f := range c.Flags {
		if f.Name == name {
			return f.Cascading
		}
	}
	return false
}

// envReading describes how an input reads a variable's text: its declared type, a named
// scalar schema's own type, with the options that change what parses: layouts, a list's or
// map's separator, an integer base, and relative times. Spellings of one type (int and
// integer) read alike.
func envReading(schema *InputSchema, schemas map[string]Schema) string {
	typ := declaredReadType(schema, schemas)
	var opts []string
	if l := layoutsFor(schema); len(l) > 0 {
		opts = append(opts, fmt.Sprintf("layout %q", strings.Join(l, " or ")))
	}
	if strings.HasPrefix(typ, "[]") || strings.HasPrefix(typ, "map[") {
		sep := ","
		if schema != nil && schema.Separator != "" {
			sep = schema.Separator
		}
		opts = append(opts, fmt.Sprintf("separator %q", sep))
	}
	if b := intBase(schema); b != "" {
		opts = append(opts, "base "+b)
	}
	if r := relative(schema); r != "" {
		opts = append(opts, "relative "+r)
	}
	if len(opts) == 0 {
		return typ
	}
	return typ + " (" + strings.Join(opts, ", ") + ")"
}

// declaredReadType is an input's type as the parser reads it: a named scalar schema's type in
// place of its name, a list's element type, and Go spellings for JSON Schema names, keeping
// value-type names (date, datetime, existingfile) that share a Go type but parse differently.
func declaredReadType(schema *InputSchema, schemas map[string]Schema) string {
	if schema == nil {
		return "string"
	}
	if name := refTypeName(schema.Ref); name != "" {
		return namedReadType(name, schemas)
	}
	if schema.Type == "" {
		return "string"
	}
	if jsonSchemaTypeToGo(schema.Type) == "[]string" && schema.Items != nil {
		if name := refTypeName(schema.Items.Ref); name != "" {
			return "[]" + namedReadType(name, schemas)
		}
		if schema.Items.Type != "" {
			return "[]" + readTypeName(schema.Items.Type)
		}
	}
	return readTypeName(schema.Type)
}

// namedReadType is a named schema's read type: its own type when it is a scalar, else name.
func namedReadType(name string, schemas map[string]Schema) string {
	if src, ok := schemas[name]; ok && src.Type != "" && src.Type != "object" && len(src.Properties) == 0 {
		return readTypeName(src.Type)
	}
	return name
}

// readTypeName spells a declared type name so spellings that parse alike compare equal.
func readTypeName(t string) string {
	if elem, ok := strings.CutPrefix(t, "[]"); ok {
		return "[]" + readTypeName(elem)
	}
	if k, v, ok := splitMapType(t); ok {
		return "map[" + readTypeName(k) + "]" + readTypeName(v)
	}
	if t == "count" {
		return "int" // a count's fallback reads a number
	}
	if _, ok := valueTypeGo[t]; ok || parserSignificantType(t) {
		return t
	}
	return jsonSchemaTypeToGo(t)
}
