package rotini

import (
	"encoding"
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/go-rotini/jsonschema"
	"github.com/go-rotini/recon"
)

// Compiled schemas, keyed by their text, so each compiles once per process. The text never
// changes for a given key, so the cache is safe across runs and goroutines. Configuration
// files, object flags and stdin documents share it.
var compiledSchemas sync.Map // schema text → *jsonschema.Schema

// compileSchema returns the compiled form of a JSON Schema, or jsonschema's error.
func compileSchema(schema string) (*jsonschema.Schema, error) {
	if cached, ok := compiledSchemas.Load(schema); ok {
		if s, ok := cached.(*jsonschema.Schema); ok {
			return s, nil
		}
	}
	s, err := jsonschema.Compile([]byte(schema))
	if err != nil {
		return nil, err //nolint:wrapcheck // each caller says which schema
	}
	cached, _ := compiledSchemas.LoadOrStore(schema, s)
	if c, ok := cached.(*jsonschema.Schema); ok {
		return c, nil
	}
	return s, nil
}

// schemaCheck checks a decoded document against a compiled schema, reporting each failure as
// a [*recon.ValidationError] with its key path, inside a [*recon.MultiError]. A compound
// keyword (anyOf, oneOf, $ref) reports the failures beneath it, as a stdin document's check
// does.
type schemaCheck struct{ schema *jsonschema.Schema }

// Validate checks m; a nil m is the empty object. An unsigned integer in m, such as a uint64
// above the int64 maximum, is checked as the number it is, and a time (a TOML date-time, local
// date or local time) as its text, as JSON writes it.
func (c schemaCheck) Validate(m map[string]any) error {
	var input any = m
	if m == nil {
		input = map[string]any{}
	}
	input, _ = schemaValue(input)
	result, err := c.schema.ValidateValue(input)
	if err != nil {
		// The check stopped before it finished, such as at the schema's evaluation limit: the
		// document wasn't found invalid.
		return fmt.Errorf("%w: %w: %w", recon.ErrValidation, errSchemaUnfinished, err)
	}
	if result.Valid {
		return nil
	}
	return schemaFailures(result.Errors)
}

// schemaValue is v as the schema check reads it: each unsigned integer as a json.Number, which
// the check reads as a number of any size, and each value with its own text form (time.Time,
// a TOML local date or time) as that text, as a JSON round trip writes it. A map or list holding
// one is copied, never changed; changed reports whether v held one.
func schemaValue(v any) (out any, changed bool) {
	switch t := v.(type) {
	case uint:
		return json.Number(strconv.FormatUint(uint64(t), 10)), true
	case uint8:
		return json.Number(strconv.FormatUint(uint64(t), 10)), true
	case uint16:
		return json.Number(strconv.FormatUint(uint64(t), 10)), true
	case uint32:
		return json.Number(strconv.FormatUint(uint64(t), 10)), true
	case uint64:
		return json.Number(strconv.FormatUint(t, 10)), true
	case map[string]any:
		var cp map[string]any
		for k, e := range t {
			if ne, ok := schemaValue(e); ok {
				if cp == nil {
					cp = maps.Clone(t)
				}
				cp[k] = ne
			}
		}
		if cp != nil {
			return cp, true
		}
	case []any:
		var cp []any
		for i, e := range t {
			if ne, ok := schemaValue(e); ok {
				if cp == nil {
					cp = slices.Clone(t)
				}
				cp[i] = ne
			}
		}
		if cp != nil {
			return cp, true
		}
	case encoding.TextMarshaler:
		if text, err := t.MarshalText(); err == nil {
			return string(text), true
		}
	}
	return v, false
}

// schemaFailures turns a failed check's errors into recon validation errors, descending into
// each compound error's causes to the failures beneath it. A value that matches no branch of an
// anyOf or oneOf, each branch failing on the value itself, is one failure naming every branch's
// problem as alternatives. Each failure's Cause is the jsonschema error it reports, as recon's
// own schema validator sets it.
func schemaFailures(errs []jsonschema.ValidationError) *recon.MultiError {
	multi := &recon.MultiError{}
	var walk func(errs []jsonschema.ValidationError)
	walk = func(errs []jsonschema.ValidationError) {
		for i := range errs {
			e := &errs[i]
			if msg, ok := noBranchMatches(*e); ok {
				multi.Append(&recon.ValidationError{Path: pointerPath(e.InstanceLocation), Rule: e.Keyword, Msg: msg, Cause: e})
				continue
			}
			if len(e.Causes) > 0 {
				walk(e.Causes)
				continue
			}
			msg := e.Message
			if e.Keyword == "false" && strings.HasSuffix(e.KeywordLocation, "/additionalProperties") {
				msg = unknownKeyMsg // a key its object's schema doesn't list
			}
			multi.Append(&recon.ValidationError{Path: pointerPath(e.InstanceLocation), Rule: e.Keyword, Msg: msg, Cause: e})
		}
	}
	walk(errs)
	return multi
}

// noBranchMatches words an anyOf or oneOf failure whose causes are all leaf failures of the
// value itself: "matches none of: <branch> or <branch>", a branch failing several keywords
// joined with "and", repeated wordings given once. ok is false for any other failure, which is
// reported through its causes.
func noBranchMatches(e jsonschema.ValidationError) (string, bool) {
	if (e.Keyword != "anyOf" && e.Keyword != "oneOf") || len(e.Causes) == 0 || e.KeywordLocation == "" {
		return "", false
	}
	prefix := e.KeywordLocation + "/"
	var order []string
	branches := map[string][]string{}
	for _, c := range e.Causes {
		rest, ok := strings.CutPrefix(c.KeywordLocation, prefix)
		if !ok || len(c.Causes) > 0 || c.InstanceLocation != e.InstanceLocation {
			return "", false
		}
		idx, _, _ := strings.Cut(rest, "/")
		if _, seen := branches[idx]; !seen {
			order = append(order, idx)
		}
		if !slices.Contains(branches[idx], c.Message) {
			branches[idx] = append(branches[idx], c.Message)
		}
	}
	var alts []string
	for _, idx := range order {
		if alt := strings.Join(branches[idx], " and "); !slices.Contains(alts, alt) {
			alts = append(alts, alt)
		}
	}
	if len(alts) == 1 {
		return alts[0], true
	}
	return "matches none of: " + strings.Join(alts, " or "), true
}

// schemaValidator returns the checker for a JSON Schema.
func schemaValidator(schema string) (schemaCheck, error) {
	s, err := compileSchema(schema)
	if err != nil {
		return schemaCheck{}, fmt.Errorf("compile schema: %w: %w", recon.ErrSchemaInvalid, err)
	}
	return schemaCheck{schema: s}, nil
}

// typeFact is fact(v), remembered in cache by v's type: for questions whose answer depends on
// an inputs type alone, asked on every read.
func typeFact(cache *sync.Map, v reflect.Value, fact func(reflect.Value) bool) bool {
	if !v.IsValid() {
		return false
	}
	t := v.Type()
	if got, ok := cache.Load(t); ok {
		b, _ := got.(bool)
		return b
	}
	got := fact(v)
	cache.Store(t, got)
	return got
}

// unknownKeyMsg is a schema check's message for a key its object's schema forbids
// (`additionalProperties: false`).
const unknownKeyMsg = "unknown key"

// forbiddenKey reports whether ve is a key its object's schema forbids.
func forbiddenKey(ve *recon.ValidationError) bool {
	return ve.Rule == "false" && ve.Msg == unknownKeyMsg && len(ve.Path) > 0
}

// unknownFileKey is the usage error for a key in configuration file f, found at where, that
// the file's schema forbids. Its Token and Candidates are the key and the keys its object's
// schema declares, which [SuggestionFacts] reads.
func unknownFileKey(f ConfigFileDef, where string, ve *recon.ValidationError, err error) *InputError {
	key := ve.Path[len(ve.Path)-1]
	known := schemaKeysAt(f.Schema, ve.Path[:len(ve.Path)-1])
	msg := fmt.Sprintf("configuration file %s has unknown key %s", where, ve.Path.String())
	if len(known) > 0 {
		msg += " (keys: " + strings.Join(known, ", ") + ")"
	}
	e := usageBind(InputErrorKindUnknownKey, channelConfig, f.Name, msg, err)
	e.Token, e.Candidates = key, known
	return e
}

// schemaKeysAt is the property names, sorted, that the object at path in a document checked
// against schema (a JSON Schema's text) declares, through properties, items and references to
// its definitions; nil when there is no such object.
func schemaKeysAt(schema string, path []string) []string {
	var root map[string]any
	if json.Unmarshal([]byte(schema), &root) != nil {
		return nil
	}
	defs, _ := root["definitions"].(map[string]any)
	resolve := func(n map[string]any) map[string]any {
		for range len(defs) + 1 { // a chain of references, never a loop
			ref, ok := n["$ref"].(string)
			if !ok {
				return n
			}
			next, ok := defs[strings.TrimPrefix(ref, "#/definitions/")].(map[string]any)
			if !ok {
				return nil
			}
			n = next
		}
		return n
	}
	n := resolve(root)
	for _, seg := range path {
		if n == nil {
			return nil
		}
		props, _ := n["properties"].(map[string]any)
		next, ok := props[seg].(map[string]any)
		if !ok {
			next, _ = n["items"].(map[string]any)
		}
		n = resolve(next)
	}
	if n == nil {
		return nil
	}
	props, _ := n["properties"].(map[string]any)
	return slices.Sorted(maps.Keys(props))
}
