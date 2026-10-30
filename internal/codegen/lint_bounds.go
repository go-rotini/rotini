package codegen

import (
	"fmt"
	"maps"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/go-rotini/rotini"
)

// Value rules that catch an input no value can ever satisfy: contradictory bounds, enum
// members that are not the declared type, and a requirement a default always meets.

// walkSchemaTreeAt is [walkSchemaTree] with each schema's JSON pointer and its property path
// from b ("" for b itself, "limits.max", "tags[]"). Properties are visited in sorted order so
// reports are deterministic.
func walkSchemaTreeAt(b BaseSchema, ptr, path string, visit func(b BaseSchema, ptr, path string)) {
	visit(b, ptr, path)
	for _, k := range slices.Sorted(maps.Keys(b.Properties)) {
		sub := k
		if path != "" {
			sub = path + "." + k
		}
		walkSchemaTreeAt(b.Properties[k].BaseSchema, ptr+"/properties/"+k, sub, visit)
	}
	if b.Items != nil {
		walkSchemaTreeAt(b.Items.BaseSchema, ptr+"/items", path+"[]", visit)
	}
}

// schemaNodeType resolves a document schema node (a named schema, an output or stdin
// property) to its Go type, a named scalar to the type it declares. It returns "" for a node
// with no type or one naming an object schema.
func schemaNodeType(b BaseSchema, schemas map[string]Schema) string {
	if name := refTypeName(b.Ref); name != "" {
		return namedScalarType(name, schemas)
	}
	if b.Type == "" {
		return ""
	}
	return jsonSchemaTypeToGo(b.Type)
}

// boundSide is one side of a numeric range: the bound's key, its value in the type's unit,
// and whether it is exclusive.
type boundSide struct {
	key  string
	v    float64
	excl bool
}

// integerValued reports whether a numeric type holds whole numbers only, so a range must
// contain an integer to be satisfiable. Durations and sizes count in nanoseconds and bytes.
func integerValued(elem string) bool {
	return strings.HasPrefix(elem, "int") || strings.HasPrefix(elem, "uint") || measuredTypes[elem]
}

// rangeNoun names what a range of elem's values holds, for a message.
func rangeNoun(elem string) string {
	switch {
	case elem == "time.Duration":
		return "duration"
	case elem == rotiniPkgName+".ByteSize":
		return "size"
	case integerValued(elem):
		return "integer"
	}
	return "number"
}

// formatBoundIn renders a bound in the type's own spelling: a duration as 1m0s, a size as
// 512Mi, a number with no trailing zeros.
func formatBoundIn(elem string, v float64) string {
	switch elem {
	case "time.Duration":
		return time.Duration(int64(v)).String()
	case rotiniPkgName + ".ByteSize":
		return rotini.ByteSize(int64(v)).String()
	}
	return formatBound(v)
}

// numericBound reads one bound in elem's unit. A number is used as is; text is converted for
// a duration or size (named, output and stdin schemas keep their bounds as text). ok is false
// when the bound is text that does not convert, so the range can't be judged.
func numericBound(v any, elem string) (n *float64, ok bool) {
	if v == nil {
		return nil, true
	}
	if f := bound(v); f != nil {
		return f, true
	}
	if text, isText := v.(string); isText {
		if f, converted := measuredValue(elem, text); converted {
			return &f, true
		}
	}
	return nil, false
}

// boundsProblems reports each pair of bounds on b that leaves no value for elem (the type a
// value is checked as): a numeric range with nothing in it, minLength above maxLength, and
// minItems above maxItems. lengths and items say whether those bounds apply to the type. Each
// problem is keyed by the lower bound it names first.
func boundsProblems(b *BaseSchema, elem string, lengths, items bool) []keyedMsg {
	var out []keyedMsg
	if constraintNumericFamily[elem] || measuredTypes[elem] {
		if km := rangeProblem(b, elem); km.msg != "" {
			out = append(out, km)
		}
	}
	if lengths && b.MaxLength != nil && b.MinLength > *b.MaxLength {
		out = append(out, keyedMsg{"minLength", fmt.Sprintf("`minLength` %d is above `maxLength` %d, so no value can ever be accepted; lower `minLength` or raise `maxLength`", b.MinLength, *b.MaxLength)})
	}
	if items && b.MaxItems != nil && b.MinItems > *b.MaxItems {
		out = append(out, keyedMsg{"minItems", fmt.Sprintf("`minItems` %d is above `maxItems` %d, so no value can ever be accepted; lower `minItems` or raise `maxItems`", b.MinItems, *b.MaxItems)})
	}
	return out
}

// rangeProblem folds the inclusive and exclusive bounds into one lower and one upper bound and
// reports, keyed by the lower bound, when no value of elem lies between them. For whole-number
// types the bounds round inward first, so exclusiveMinimum 1 with exclusiveMaximum 2 holds no
// integer.
func rangeProblem(b *BaseSchema, elem string) keyedMsg {
	var sides [4]*float64
	for i, v := range []any{b.Minimum, b.ExclusiveMinimum, b.Maximum, b.ExclusiveMaximum} {
		n, ok := numericBound(v, elem)
		if !ok {
			return keyedMsg{} // text that isn't a value of the type: lintConstraintApplicability
		}
		sides[i] = n
	}
	whole := integerValued(elem)
	lo := tighterBound(side(sides[0], "minimum", false), side(sides[1], "exclusiveMinimum", true), whole, true)
	hi := tighterBound(side(sides[2], "maximum", false), side(sides[3], "exclusiveMaximum", true), whole, false)
	if lo == nil || hi == nil {
		return keyedMsg{}
	}
	empty := effectiveBound(*lo, whole, true) > effectiveBound(*hi, whole, false)
	if !whole {
		empty = lo.v > hi.v || (lo.v == hi.v && (lo.excl || hi.excl))
	}
	if !empty {
		return keyedMsg{}
	}
	if lo.v > hi.v {
		return keyedMsg{lo.key, fmt.Sprintf("%#q %s is above %#q %s, so no value can ever be accepted; lower %#q or raise %#q",
			lo.key, formatBoundIn(elem, lo.v), hi.key, formatBoundIn(elem, hi.v), lo.key, hi.key)}
	}
	fix := "widen the range"
	if lo.excl || hi.excl {
		fix += " or use `minimum`/`maximum`"
	}
	return keyedMsg{lo.key, fmt.Sprintf("%#q %s and %#q %s leave no %s between them; %s",
		lo.key, formatBoundIn(elem, lo.v), hi.key, formatBoundIn(elem, hi.v), rangeNoun(elem), fix)}
}

// effectiveBound is a lower or upper bound as the nearest value a whole-number type can take;
// any other type takes the bound as is.
func effectiveBound(s boundSide, whole, lower bool) float64 {
	switch {
	case !whole:
		return s.v
	case lower && s.excl:
		return math.Floor(s.v) + 1
	case lower:
		return math.Ceil(s.v)
	case s.excl:
		return math.Ceil(s.v) - 1
	default:
		return math.Floor(s.v)
	}
}

// tighterBound returns whichever of an inclusive and an exclusive bound on one side admits
// fewer values, the exclusive one on a tie; nil when neither is set.
func tighterBound(incl, excl *boundSide, whole, lower bool) *boundSide {
	if incl == nil || excl == nil {
		if incl == nil {
			return excl
		}
		return incl
	}
	i, x := effectiveBound(*incl, whole, lower), effectiveBound(*excl, whole, lower)
	if i == x || (lower && x > i) || (!lower && x < i) {
		return excl
	}
	return incl
}

// side builds a boundSide, or nil when the bound is absent.
func side(v *float64, key string, excl bool) *boundSide {
	if v == nil {
		return nil
	}
	return &boundSide{key: key, v: *v, excl: excl}
}

// inputValueType resolves an input to the type its values are checked as: a named scalar to
// the type it declares, and a list to its element. typ is the whole (list or map) type.
func inputValueType(schema *InputSchema, schemas map[string]Schema) (typ, elem string) {
	typ = getSchemaType(schema)
	if t := namedScalarType(typ, schemas); t != "" {
		typ = t
	}
	return typ, strings.TrimPrefix(typ, "[]")
}

// inheritedScalarRef names the named scalar schema an input (or a list input's items) refers
// to, or "" when it refers to none.
func inheritedScalarRef(schema *InputSchema, schemas map[string]Schema) string {
	ref := schema.Ref
	if ref == "" && schema.Items != nil {
		ref = schema.Items.Ref
	}
	name := refTypeName(ref)
	if name == "" || namedScalarType(name, schemas) == "" {
		return ""
	}
	return name
}

// lintBoundsSatisfiable rejects bounds that leave no value: a numeric range with nothing in
// it (minimum above maximum, or exclusive bounds with no value of the type between them),
// minLength above maxLength, and minItems above maxItems. It checks inputs on every channel,
// named schemas, output, and stdin payloads at any depth. A named scalar schema's problem is
// reported once, on the schema, not again on each input that refers to it.
func lintBoundsSatisfiable(spec *Spec) []error {
	schemas := spec.Command.Schemas
	var problems []error
	nodeProblems := func(b BaseSchema, typ string) []keyedMsg {
		return boundsProblems(&b, typ, stringValued(typ), strings.HasPrefix(typ, "[]") || strings.HasPrefix(typ, "map["))
	}
	tree := func(loc, subject, ptr string, root BaseSchema) {
		walkSchemaTreeAt(root, ptr, "", func(b BaseSchema, ptr, path string) {
			typ := schemaNodeType(b, schemas)
			if typ == "" || (refTypeName(b.Ref) != "" && path != "") {
				return // a $ref'd scalar is checked once, on its named schema
			}
			who := subject
			if path != "" {
				who = fmt.Sprintf("%s property %q", subject, path)
			}
			for _, km := range nodeProblems(b, typ) {
				problems = append(problems, &problem{kind: "spec", ptr: ptr + "/" + km.key, loc: loc, msg: who + ": " + km.msg})
			}
		})
	}
	named := map[string][]keyedMsg{} // named scalar schema -> its problems, for the input dedupe
	for _, name := range slices.Sorted(maps.Keys(schemas)) {
		s := schemas[name]
		if t := namedScalarType(name, schemas); t != "" {
			named[name] = nodeProblems(s.BaseSchema, t)
		}
		tree(rootLabel(spec), fmt.Sprintf("schema %q", name), rootPointer+"/schemas/"+name, s.BaseSchema)
	}
	walkCommandsAt(spec, func(c *Command, path, ptr string) {
		eachInputAt(c, ptr, func(channel, name, ptr string, schema *InputSchema) {
			if schema == nil || objectRef(schema, schemas) != "" {
				return
			}
			if channel == "stdin" {
				tree("command "+path, "stdin", ptr+"/schema", schema.BaseSchema)
				return
			}
			typ, elem := inputValueType(schema, schemas)
			items := strings.HasPrefix(typ, "[]") || strings.HasPrefix(typ, "map[")
			inherited := named[inheritedScalarRef(schema, schemas)]
			for _, km := range boundsProblems(&schema.BaseSchema, elem, stringValued(elem), items) {
				if slices.Contains(inherited, km) {
					continue
				}
				problems = append(problems, schemaKeyProblem(ptr, km.key, path, channel, name, km.msg))
			}
		})
		for _, s := range outputShapesAt(c.Output, ptr+"/output") {
			tree("command "+path, "output", s.ptr, s.schema.BaseSchema)
		}
	})
	return problems
}

// enumConstraintProblem reports the first enum member of a non-map input that is a value of
// its type but breaks the input's own bounds, lengths or pattern, so help and completion offer
// it yet every run refuses it, keyed by the member; or nothing.
func enumConstraintProblem(schema *InputSchema, typ string) keyedMsg {
	if strings.HasPrefix(typ, "map[") {
		return keyedMsg{}
	}
	constrained := *schema
	constrained.Ref, constrained.Items, constrained.Type, constrained.Enum = "", nil, typ, nil
	for i, m := range enumStrings(schema.Enum) {
		msg := defaultViolation(&constrained, "`enum` value", m)
		if msg == "" && measuredTypes[strings.TrimPrefix(typ, "[]")] {
			if complaint := runtimeRejects(&constrained, []string{m}); complaint != "" {
				msg = fmt.Sprintf("`enum` value %q breaks the declared bounds (%s)", m, complaint)
			}
		}
		if msg != "" {
			return keyedMsg{fmt.Sprintf("enum/%d", i), msg + ", so it is offered but always refused; drop the value or change the constraint"}
		}
	}
	return keyedMsg{}
}

// lintEnumValues rejects enum members that can never be a value of the input's type: a
// member that doesn't parse as that type (checked with the runtime's own parser), and any
// enum on a bool, which already takes only true or false. On a map input the enum is checked
// against each whole key=value pair; on any other, a member must also meet the input's own
// bounds, lengths and pattern. It also warns about a member listed twice. A named scalar
// schema is checked once, not again on each input that inherits its members.
func lintEnumValues(spec *Spec) []error {
	schemas := spec.Command.Schemas
	var problems []error
	check := func(schema *InputSchema, typ string) (errs []keyedMsg, dup keyedMsg) {
		seen := map[string]bool{}
		for i, m := range enumStrings(schema.Enum) {
			if seen[m] && dup.msg == "" {
				dup = keyedMsg{fmt.Sprintf("enum/%d", i), fmt.Sprintf("lists `enum` value %q twice; list each value once", m)}
			}
			seen[m] = true
		}
		elem := strings.TrimPrefix(typ, "[]")
		if elem == "bool" {
			return []keyedMsg{{"enum", "sets `enum` on a bool, which already takes only true or false; remove the enum, or make the type string"}}, dup
		}
		probe := *schema
		probe.Ref, probe.Items, probe.Type = "", nil, typ
		probe.Minimum, probe.Maximum, probe.ExclusiveMinimum, probe.ExclusiveMaximum, probe.MultipleOf = nil, nil, nil, nil, nil
		for i, m := range enumStrings(schema.Enum) {
			complaint := runtimeRejects(&probe, []string{m})
			if complaint == "" {
				continue
			}
			at := fmt.Sprintf("enum/%d", i)
			if strings.HasPrefix(typ, "map[") && !strings.Contains(m, "=") {
				return []keyedMsg{{at, fmt.Sprintf("`enum` value %q is not a key=value pair, which every value of a map input is; write members as key=value, or remove the enum", m)}}, dup
			}
			if strings.HasPrefix(complaint, strconv.Quote(m)+" ") {
				return []keyedMsg{{at, fmt.Sprintf("`enum` value %s, so no value can match both; fix the value or the type", complaint)}}, dup
			}
			return []keyedMsg{{at, fmt.Sprintf("`enum` value %q is not a valid %s (%s), so no value can match both; fix the value or the type", m, typeSpelling(elem), complaint)}}, dup
		}
		if km := enumConstraintProblem(schema, typ); km.msg != "" {
			return []keyedMsg{km}, dup
		}
		return nil, dup
	}
	report := func(errs []keyedMsg, dup keyedMsg, add func(km keyedMsg, sev severity)) {
		for _, km := range errs {
			add(km, severityError)
		}
		if dup.msg != "" {
			add(dup, severityWarning)
		}
	}
	for _, name := range slices.Sorted(maps.Keys(schemas)) {
		s := schemas[name]
		t := namedScalarType(name, schemas)
		if t == "" || len(s.Enum) == 0 {
			continue
		}
		errs, dup := check(&InputSchema{BaseSchema: s.BaseSchema}, t)
		report(errs, dup, func(km keyedMsg, sev severity) {
			problems = append(problems, &problem{kind: "spec", ptr: rootPointer + "/schemas/" + name + "/" + km.key, loc: rootLabel(spec), msg: fmt.Sprintf("schema %q: %s", name, km.msg), sev: sev})
		})
	}
	walkCommandsAt(spec, func(c *Command, path, ptr string) {
		eachInputAt(c, ptr, func(channel, name, ptr string, schema *InputSchema) {
			if channel == "stdin" || schema == nil || len(schema.Enum) == 0 || schema.Type == "count" || objectRef(schema, schemas) != "" {
				return // count: lintCountFlags; an object input: lintObjectFlags
			}
			if ref := inheritedScalarRef(schema, schemas); ref != "" && slices.Equal(enumStrings(schema.Enum), enumStrings(schemas[ref].Enum)) {
				return // reported on the named schema
			}
			typ, _ := inputValueType(schema, schemas)
			errs, dup := check(schema, typ)
			report(errs, dup, func(km keyedMsg, sev severity) {
				p := schemaKeyProblem(ptr, km.key, path, channel, name, km.msg)
				p.sev = sev
				problems = append(problems, p)
			})
		})
	})
	return problems
}

// lintRequiredDefault rejects an input that is both required and has a default: defaults are
// applied before requirements are checked, so the requirement can never fire. A default_text
// with no default is only display text and is fine.
func lintRequiredDefault(spec *Spec) []error {
	var problems []error
	walkCommandsAt(spec, func(c *Command, path, ptr string) {
		eachInputAt(c, ptr, func(channel, name, ptr string, schema *InputSchema) {
			if channel == "stdin" || schema == nil || schema.Type == "count" || !schema.Required || schema.Default == nil {
				return // count: lintCountFlags rejects both keys
			}
			problems = append(problems, schemaKeyProblem(ptr, "default", path, channel, name,
				"sets `required` and a `default`; the default always supplies a value, so the requirement can never fire. Drop `default` to make it required, or drop `required`"))
		})
	})
	return problems
}

// lintRequiredVisibility warns about a required flag or argument that is also hidden, which
// every run must pass though help doesn't show it, or deprecated, which every run must pass
// though passing it reports it as deprecated. A flag deprecated only in some spellings
// (`deprecated_identifiers`) is not: its other spellings are the ones to use.
func lintRequiredVisibility(spec *Spec) []error {
	var problems []error
	warn := func(at, path, channel, name, msg string) {
		p := inputProblem(at, path, channel, name, msg)
		p.sev = severityWarning
		problems = append(problems, p)
	}
	check := func(at, path, channel, name string, hidden bool, deprecated string, schema *InputSchema) {
		if schema == nil || !schema.Required {
			return
		}
		if hidden {
			warn(at+"/hidden", path, channel, name, "is `required` and `hidden`: every run must pass it, but help doesn't show it, so a user can't learn it exists; drop `hidden`, or drop `required`")
		}
		if deprecated != "" {
			warn(at+"/deprecated", path, channel, name, "is `required` and `deprecated`: every run must pass it, but passing it reports it as deprecated, so a user can't stop using it; drop `required` while it is deprecated, or drop `deprecated`")
		}
	}
	walkCommandsAt(spec, func(c *Command, path, ptr string) {
		for i, f := range c.Flags {
			deprecated := f.Deprecated
			if len(f.DeprecatedIdentifiers) > 0 {
				deprecated = ""
			}
			check(fmt.Sprintf("%s/flags/%d", ptr, i), path, "flag", f.Name, f.Hidden, deprecated, f.Schema)
		}
		for i, a := range c.Arguments {
			check(fmt.Sprintf("%s/arguments/%d", ptr, i), path, "argument", a.Name, a.Hidden, a.Deprecated, a.Schema)
		}
	})
	return problems
}

// lintSecretDefault warns about a secret input with a literal default: the value is compiled
// into the binary, where anyone with the binary can read it.
func lintSecretDefault(spec *Spec) []error {
	var problems []error
	walkCommandsAt(spec, func(c *Command, path, ptr string) {
		eachInputAt(c, ptr, func(channel, name, ptr string, schema *InputSchema) {
			if channel == "stdin" || schema == nil || schema.Type == "count" || !schema.Secret || schema.Default == nil {
				return // count: lintCountFlags rejects both keys
			}
			p := schemaKeyProblem(ptr, "default", path, channel, name,
				"is secret but has a literal `default`, which is compiled into the binary; supply it at run time (an environment variable, a config file, or `from: [file, stdin]` on a flag) and drop the default")
			p.sev = severityWarning
			problems = append(problems, p)
		})
	})
	return problems
}

// specSpellings maps the Go types rotini's own type names resolve to back to those names.
var specSpellings = map[string]string{
	"time.Duration": "duration", "time.Time": "time", "*url.URL": "url", "mail.Address": "email",
	"*time.Location": "timezone", "net.HardwareAddr": "mac", "netip.Addr": "ip",
	"netip.Prefix": "cidr", "netip.AddrPort": "hostport", rotiniPkgName + ".ByteSize": "bytesize",
	rotiniPkgName + ".HexBytes": "hexbytes", rotiniPkgName + ".Base64Bytes": "base64bytes",
	"*regexp.Regexp": "regexp", rotiniPkgName + ".Glob": "glob",
}

// typeSpelling renders a resolved Go type as the spec spells it, for a message: duration, not
// time.Duration.
func typeSpelling(goType string) string {
	if s, ok := specSpellings[goType]; ok {
		return s
	}
	return goType
}
