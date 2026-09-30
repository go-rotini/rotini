package codegen

import (
	"errors"
	"fmt"
	"net"
	"net/mail"
	"net/netip"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/go-rotini/recon"
	"github.com/go-rotini/rotini"
)

// lintValuesParse holds every value a spec writes FOR an input — a flag's or argument's
// default, and a flag's implicit_value — to the input's own type, by running it through the
// runtime's parser.
//
// Without it `default: abc` on an int flag, or `default: 5` on a duration (Go durations need a
// unit), validated clean and generated fine, and then every run that left the flag unset failed
// with a usage error about a value the user never typed. The runtime is the judge rather than a
// re-implementation here, so the two cannot disagree about what parses: a new type, a new
// spelling (yes/no for a bool, 7d for a duration) is accepted here the moment the runtime
// accepts it.
//
// Only the argv channels are checked: env and config defaults are decoded by recon. Types whose
// Go shape codegen cannot know — an imported type — are left to the first run, as are path
// types, whose existence is a fact about the machine the program runs on, not this one.
func lintValuesParse(spec *Spec) []error {
	var problems []error
	walkCommandsAt(spec, func(c *Command, path, ptr string) {
		eachInputSchema(c.inputs(), func(channel, name string, schema *InputSchema) {
			if schema == nil || (channel != "flag" && channel != "argument") || schema.Ref != "" {
				return
			}
			check := func(what, consequence string, values []string) {
				if msg := runtimeRejects(schema, values); msg != "" {
					problems = append(problems, &problem{kind: "spec", ptr: ptr, loc: "command " + path,
						msg: fmt.Sprintf("%s %q: %s %s — %s", channel, name, what, msg, consequence)})
				}
			}
			if schema.Default != nil {
				values := defaultList(schema.Default)
				if values == nil {
					values = []string{defaultString(schema.Default)}
				}
				check("default", "every run that leaves it unset would fail", values)
			}
			if schema.ImplicitValue != nil && channel == "flag" {
				check("implicit_value", "the flag given bare would always fail", []string{defaultString(schema.ImplicitValue)})
			}
		})
	})
	return problems
}

// runtimeRejects parses each value into the input's Go type exactly as a run would, returning
// the runtime's own complaint about the first that fails, or "" when all parse or the type is
// one this cannot build.
func runtimeRejects(schema *InputSchema, values []string) string {
	defType := definitionType(schema)
	if defType == "count" || strings.Contains(defType, "existingfile") || strings.Contains(defType, "existingdir") {
		return ""
	}
	rt, ok := reflectTypeOf(getSchemaType(schema))
	if !ok {
		return ""
	}
	flags := reflect.StructOf([]reflect.StructField{{Name: "V", Type: rt, Tag: `rotini:"v"`}})
	cmd := reflect.StructOf([]reflect.StructField{
		{Name: "Flags", Type: flags},
		{Name: "Arguments", Type: reflect.TypeFor[struct{}]()},
	})
	inputs := reflect.StructOf([]reflect.StructField{{Name: "App", Type: cmd}})
	def := rotini.Definition{Name: "app", Handler: "App", Flags: []rotini.FlagDef{{Name: "v", Identifiers: []string{"--v"}, Type: defType}}}
	for _, v := range values {
		err := rotini.NewParser().Parse(rotini.NewContextFor(def, []string{"--v=" + v}), reflect.New(inputs).Interface())
		if err != nil {
			return strings.TrimPrefix(err.Error(), "--v: ")
		}
	}
	return ""
}

// valueTypes are the Go types a spec's type names resolve to whose reflect.Type codegen knows —
// the builtins and rotini's own vocabulary. time.Time is left out until `date` parses the
// dates it is documented to accept; until then this would reject every date default.
var valueTypes = map[string]reflect.Type{
	"string": reflect.TypeFor[string](), "bool": reflect.TypeFor[bool](),
	"int": reflect.TypeFor[int](), "int8": reflect.TypeFor[int8](), "int16": reflect.TypeFor[int16](),
	"int32": reflect.TypeFor[int32](), "int64": reflect.TypeFor[int64](),
	"uint": reflect.TypeFor[uint](), "uint8": reflect.TypeFor[uint8](), "uint16": reflect.TypeFor[uint16](),
	"uint32": reflect.TypeFor[uint32](), "uint64": reflect.TypeFor[uint64](),
	"float32": reflect.TypeFor[float32](), "float64": reflect.TypeFor[float64](),
	"byte": reflect.TypeFor[byte](), "rune": reflect.TypeFor[rune](),
	"time.Duration":                reflect.TypeFor[time.Duration](),
	"*time.Location":               reflect.TypeFor[*time.Location](),
	"*url.URL":                     reflect.TypeFor[*url.URL](),
	"mail.Address":                 reflect.TypeFor[mail.Address](),
	"net.HardwareAddr":             reflect.TypeFor[net.HardwareAddr](),
	"netip.Addr":                   reflect.TypeFor[netip.Addr](),
	"netip.Prefix":                 reflect.TypeFor[netip.Prefix](),
	"netip.AddrPort":               reflect.TypeFor[netip.AddrPort](),
	rotiniPkgName + ".ByteSize":    reflect.TypeFor[rotini.ByteSize](),
	rotiniPkgName + ".HexBytes":    reflect.TypeFor[rotini.HexBytes](),
	rotiniPkgName + ".Base64Bytes": reflect.TypeFor[rotini.Base64Bytes](),
}

// reflectTypeOf builds the reflect.Type for a resolved Go type spelling out of valueTypes,
// through list, string-keyed map and pointer spellings.
func reflectTypeOf(t string) (reflect.Type, bool) {
	if rt, ok := valueTypes[t]; ok {
		return rt, true
	}
	if elem, ok := strings.CutPrefix(t, "[]"); ok {
		et, ok := reflectTypeOf(elem)
		if !ok {
			return nil, false
		}
		return reflect.SliceOf(et), true
	}
	if key, val, ok := splitMapType(t); ok && key == "string" {
		vt, ok := reflectTypeOf(val)
		if !ok {
			return nil, false
		}
		return reflect.MapOf(reflect.TypeFor[string](), vt), true
	}
	if elem, ok := strings.CutPrefix(t, "*"); ok {
		et, ok := reflectTypeOf(elem)
		if !ok {
			return nil, false
		}
		return reflect.PointerTo(et), true
	}
	return nil, false
}

// lintObjectFlags enforces the contract of an object-valued input — one whose schema is a named
// object (`$ref: '#/schemas/DB'`) or a list of them.
//
// Flags only: a flag's value has a spelling (JSON, key=value, a file, --db.host=…), where a
// positional has one word and no name to hang fields on. The keys that shape a single scalar
// value — enum, separator, ignore_case, implicit_value, negatable, dotted_keys and the scalar
// constraints — have nothing to act on; an object's rules live in its named schema, which is
// what the value is validated against. And a default is checked against that schema here, so
// a default that could never validate fails now, not on every run that leaves the flag unset.
func lintObjectFlags(spec *Spec) []error {
	var problems []error
	schemas := spec.Command.Schemas
	walkCommandsAt(spec, func(c *Command, path, ptr string) {
		eachInputSchema(c.inputs(), func(channel, name string, schema *InputSchema) {
			ref := objectRef(schema, schemas)
			if ref == "" {
				return
			}
			add := func(msg string) {
				problems = append(problems, &problem{kind: "spec", ptr: ptr, loc: "command " + path,
					msg: fmt.Sprintf("%s %q %s", channel, name, msg)})
			}
			if channel == "argument" {
				add(fmt.Sprintf("refers to the object schema %q — an object value is taken by a flag, whose name gives its fields somewhere to go (--%s host=…, --%s.host=…); declare it as a flag", refTypeName(ref), name, name))
				return
			}
			if channel != "flag" {
				return // env and config inputs decode objects through recon
			}
			if bad := scalarOnlyKeys(schema); len(bad) > 0 {
				add(fmt.Sprintf("is an object flag, so %s cannot apply — an object's rules belong in its schema %q", strings.Join(bad, ", "), refTypeName(ref)))
			}
			if schema.Default != nil {
				if msg := objectDefaultProblem(schema, schemas); msg != "" {
					add("has a default that " + msg + " — every run that leaves it unset would fail")
				}
			}
		})
	})
	return problems
}

// scalarOnlyKeys lists the keys set on an object flag that act on a scalar value.
func scalarOnlyKeys(s *InputSchema) []string {
	var bad []string
	for key, set := range map[string]bool{
		"enum":           len(s.Enum) > 0,
		"separator":      s.Separator != "",
		"ignore_case":    s.IgnoreCase,
		"implicit_value": s.ImplicitValue != nil,
		"negatable":      s.Negatable,
		"dotted_keys":    s.DottedKeys,
		"minimum/maximum/exclusiveMinimum/exclusiveMaximum/multipleOf": s.Minimum != nil || s.Maximum != nil ||
			s.ExclusiveMinimum != nil || s.ExclusiveMaximum != nil || s.MultipleOf != nil,
		"minLength/maxLength/pattern": s.MinLength != 0 || s.MaxLength != 0 || s.Pattern != "",
	} {
		if set {
			bad = append(bad, key)
		}
	}
	slices.Sort(bad)
	return bad
}

// objectDefaultProblem validates an object flag's default against its named schema with the
// validator the runtime uses, returning what is wrong or "".
func objectDefaultProblem(schema *InputSchema, schemas map[string]Schema) string {
	v, err := recon.NewJSONSchemaValidator([]byte(objectSchemaFor(schema, schemas)))
	if err != nil {
		return ""
	}
	docs := []any{schema.Default}
	if list, ok := schema.Default.([]any); ok && strings.HasPrefix(getSchemaType(schema), "[]") {
		docs = list
	}
	for _, d := range docs {
		m, ok := d.(map[string]any)
		if !ok {
			return fmt.Sprintf("is not an object (write it as a mapping of %s's keys)", refTypeName(objectRef(schema, schemas)))
		}
		if err := v.Validate(m); err != nil {
			return "does not match its schema: " + validationSummary(err)
		}
	}
	return ""
}

// validationSummary renders recon's (possibly joined) validation errors as "key: problem; …".
func validationSummary(err error) string {
	var msgs []string
	var walk func(error)
	walk = func(e error) {
		if joined, ok := e.(interface{ Unwrap() []error }); ok {
			for _, inner := range joined.Unwrap() {
				walk(inner)
			}
			return
		}
		if ve, ok := errors.AsType[*recon.ValidationError](e); ok {
			if p := ve.Path.String(); p != "" {
				msgs = append(msgs, p+": "+ve.Msg)
				return
			}
			msgs = append(msgs, ve.Msg)
		}
	}
	walk(err)
	if len(msgs) == 0 {
		return err.Error()
	}
	return strings.Join(msgs, "; ")
}
