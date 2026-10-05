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
	"strconv"
	"strings"
	"time"

	"github.com/go-rotini/recon"
	"github.com/go-rotini/rotini"
)

// lintValuesParse rejects a flag or argument default, or a flag's implicit_value, that does
// not parse as the input's type. It runs each value through the runtime's own parser, so the
// lint and the runtime cannot disagree about what parses.
//
// Only argv channels are checked; env and config defaults are decoded by recon. Imported types
// and path types (whose existence depends on the run-time machine) are skipped.
func lintValuesParse(spec *Spec) []error {
	var problems []error
	walkCommandsAt(spec, func(c *Command, path, ptr string) {
		eachInputAt(c, ptr, func(channel, name, ptr string, schema *InputSchema) {
			if schema == nil || (channel != "flag" && channel != "argument") || schema.Ref != "" {
				return
			}
			check := func(what, consequence string, values []string) {
				if msg := runtimeRejects(schema, values); msg != "" {
					problems = append(problems, inputProblem(ptr, path, channel, name,
						fmt.Sprintf("%#q %s; %s", what, msg, consequence)))
				}
			}
			if schema.Default != nil {
				values := defaultList(schema.Default)
				if values == nil {
					values = []string{defaultString(schema.Default)}
				}
				check("default", defaultFails, values)
			}
			if schema.ImplicitValue != nil && channel == "flag" {
				check("implicit_value", "the flag given bare would always fail", []string{defaultString(schema.ImplicitValue)})
			}
		})
	})
	return problems
}

// runtimeRejects parses each value into the input's Go type as a run would and returns the
// runtime's complaint about the first failure, or "" when all parse or the type cannot be
// built here.
func runtimeRejects(schema *InputSchema, values []string) string {
	defType := definitionType(schema, nil)
	if defType == "count" || strings.Contains(defType, "existingfile") || strings.Contains(defType, "existingdir") {
		return ""
	}
	rt, ok := reflectTypeOf(getSchemaType(schema))
	if !ok {
		return ""
	}
	fd := rotini.FlagDef{Name: "v", Identifiers: []string{"--v"}, Type: defType, Layout: layoutFor(schema)}
	// Measured-type bounds are checked here because lintDefaultConstraints cannot read "3s".
	if measuredTypes[strings.TrimPrefix(getSchemaType(schema), "[]")] {
		fd.Constraints = rotini.Constraints{
			Minimum: bound(schema.Minimum), Maximum: bound(schema.Maximum),
			ExclusiveMinimum: bound(schema.ExclusiveMinimum), ExclusiveMaximum: bound(schema.ExclusiveMaximum),
			MultipleOf: bound(schema.MultipleOf),
		}
	}
	for _, v := range values {
		if _, complaint := parseAsRuntime(rt, fd, v); complaint != "" {
			return runtimeComplaint(complaint, v)
		}
	}
	return ""
}

// parseAsRuntime parses text as the value of flag fd into a field of type rt, using the
// runtime's parser on a synthetic one-flag program (--v). It returns the field, or the
// runtime's complaint ("" when it parsed).
func parseAsRuntime(rt reflect.Type, fd rotini.FlagDef, text string) (field reflect.Value, complaint string) {
	flags := reflect.StructOf([]reflect.StructField{{Name: "V", Type: rt, Tag: `rotini:"v"`}})
	cmd := reflect.StructOf([]reflect.StructField{
		{Name: "Flags", Type: flags},
		{Name: "Arguments", Type: reflect.TypeFor[struct{}]()},
	})
	out := reflect.New(reflect.StructOf([]reflect.StructField{{Name: "App", Type: cmd}}))
	def := rotini.Definition{Name: "app", Handler: "App", Flags: []rotini.FlagDef{fd}}
	if err := rotini.NewParser().Parse(rotini.NewContextFor(def, []string{"--v=" + text}), out.Interface()); err != nil {
		return reflect.Value{}, err.Error()
	}
	return out.Elem().Field(0).Field(0).Field(0), ""
}

// runtimeComplaint rephrases the runtime's error about the placeholder flag --v as one about
// the value: `--v: "abc" is not a valid integer` drops its label, and `--v must be >= 1m
// (got 30s)` becomes `"30s" must be >= 1m`.
func runtimeComplaint(msg, value string) string {
	if rest, ok := strings.CutPrefix(msg, "--v: "); ok {
		return rest
	}
	if rest, ok := strings.CutPrefix(msg, "--v "); ok {
		if i := strings.LastIndex(rest, " (got "); i >= 0 {
			rest = rest[:i]
		}
		return strconv.Quote(value) + " " + rest
	}
	return msg
}

// valueTypes maps resolved Go type spellings to their reflect.Type for the builtins and
// rotini's own type vocabulary.
var valueTypes = map[string]reflect.Type{
	"time.Time": reflect.TypeFor[time.Time](),
	"string":    reflect.TypeFor[string](), "bool": reflect.TypeFor[bool](),
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

// lintObjectFlags enforces the contract of an object-valued input, one whose schema is a
// named object (`$ref: '#/schemas/DB'`) or a list of them. Arguments cannot be objects;
// env and config inputs are decoded by recon and skipped. An object flag rejects scalar-only
// keys (its rules live in the named schema), and its default is validated against that schema.
func lintObjectFlags(spec *Spec) []error {
	var problems []error
	schemas := spec.Command.Schemas
	walkCommandsAt(spec, func(c *Command, path, ptr string) {
		eachInputAt(c, ptr, func(channel, name, ptr string, schema *InputSchema) {
			ref := objectRef(schema, schemas)
			if ref == "" {
				return
			}
			add := func(msg string) { problems = append(problems, inputProblem(ptr, path, channel, name, msg)) }
			if channel == "argument" {
				add(fmt.Sprintf("refers to the object schema %q; an object value is taken by a flag, whose name gives its fields somewhere to go (--%s host=…, --%s.host=…); declare it as a flag", refTypeName(ref), name, name))
				return
			}
			if channel != "flag" {
				return // env and config inputs decode objects through recon
			}
			if bad := scalarOnlyKeys(schema); len(bad) > 0 {
				add(fmt.Sprintf("an object flag, so %s cannot apply; an object's rules belong in its schema %q", keyList(bad), refTypeName(ref)))
			}
			if schema.Default != nil {
				if msg := objectDefaultProblem(schema, schemas); msg != "" {
					add("has a `default` that " + msg + "; " + defaultFails)
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

// lintLayout restricts layout to time inputs and requires a usable Go layout (or unix /
// unixmilli). It catches notations like `YYYY-MM-DD`, which Go reads as literal text.
func lintLayout(spec *Spec) []error {
	var problems []error
	walkCommandsAt(spec, func(c *Command, path, ptr string) {
		eachInputAt(c, ptr, func(channel, name, ptr string, schema *InputSchema) {
			if schema == nil || schema.Layout == "" {
				return
			}
			add := func(msg string) { problems = append(problems, inputProblem(ptr, path, channel, name, msg)) }
			if t := strings.TrimPrefix(getSchemaType(schema), "[]"); t != "time.Time" && t != "*time.Time" {
				add(fmt.Sprintf("sets `layout` but its type is %s; a layout says how a time is written, so it applies to time, datetime and date", displayType(getSchemaType(schema))))
				return
			}
			if msg := layoutProblem(schema.Layout); msg != "" {
				add(msg)
			}
		})
	})
	return problems
}

// sampleTime differs from Go's reference time in every field, so formatting it under a layout
// with no reference elements returns the layout unchanged.
var sampleTime = time.Date(2019, time.November, 23, 17, 38, 49, 0, time.UTC)

// layoutProblem reports why a layout cannot parse anything, or "".
func layoutProblem(layout string) string {
	if layout == "unix" || layout == "unixmilli" {
		return ""
	}
	if sampleTime.Format(layout) == layout {
		return fmt.Sprintf("sets `layout` %q, which contains no part of Go's reference time, so no value could ever match; Go layouts write the reference time Mon Jan 2 15:04:05 MST 2006 the way yours is, e.g. %q for a date (or use unix / unixmilli)", layout, "2006-01-02")
	}
	if _, err := time.Parse(layout, sampleTime.Format(layout)); err != nil {
		return fmt.Sprintf("sets `layout` %q, which cannot read back the times it writes (%v)", layout, err)
	}
	return ""
}

// measuredTypes are the non-numeric types numeric bounds apply to, in their own unit
// (nanoseconds, bytes). It mirrors a matching runtime table.
var measuredTypes = map[string]bool{"time.Duration": true, rotiniPkgName + ".ByteSize": true}

// measuredValue parses text as a measured type's value in its unit using the runtime's
// parser, so a bound like `1h30m` or `1.5Gi` reads exactly as the same value would.
func measuredValue(goType, text string) (float64, bool) {
	rt, ok := valueTypes[goType]
	if !ok || !measuredTypes[goType] {
		return 0, false
	}
	v, complaint := parseAsRuntime(rt, rotini.FlagDef{Name: "v", Identifiers: []string{"--v"}, Type: goType}, text)
	if complaint != "" {
		return 0, false
	}
	return float64(v.Int()), true
}
