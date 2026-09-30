package codegen

import (
	"fmt"
	"net"
	"net/mail"
	"net/netip"
	"net/url"
	"reflect"
	"strings"
	"time"

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
