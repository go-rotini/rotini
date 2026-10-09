package rotini

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/go-rotini/recon"
)

// Object-valued flags: a flag whose schema is a named object (`$ref: '#/schemas/DB'`) takes a
// structured value in any of four spellings:
//
//	--db '{"host":"db.internal","port":5432}'    JSON, recognized by its leading {
//	--db host=db.internal,port=5432,tls=true     key=value pairs, CSV-quoted; dotted keys nest
//	--db @db.yaml                                a file (with from: [file]), JSON or YAML
//	--db.host=db.internal --db.port=5432         one field per flag
//
// Every spelling decodes to a JSON-shaped document, is validated against the named schema (the
// same validator a stdin payload meets), and is bound into the generated struct. For a single
// object the occurrences merge in argv order, a later key winning; for a list of objects
// (`type: array, items: {$ref: …}`) each occurrence is one element.

// isObjectFlag reports whether a flag takes a structured value.
func isObjectFlag(fd FlagDef) bool { return fd.ObjectSchema != "" }

// bindObjectFlag decodes, validates and binds an object flag's raw occurrences into f, which is
// a struct, a pointer to one, or a slice of either.
func bindObjectFlag(f reflect.Value, raw []string, fd FlagDef) error {
	if len(raw) == 0 {
		return nil
	}
	if f.Kind() == reflect.Slice {
		elem := f.Type().Elem()
		out := reflect.MakeSlice(f.Type(), 0, len(raw))
		docs := make([]map[string]any, 0, len(raw))
		for _, r := range raw {
			doc, err := decodeObject(r, elem)
			if err != nil {
				return err
			}
			v := reflect.New(elem).Elem()
			if err := validateAndBindObject(v, doc, fd.ObjectSchema); err != nil {
				return err
			}
			out = reflect.Append(out, v)
			docs = append(docs, doc)
		}
		if fd.UniqueItems {
			if dup := duplicateDocument(docs); dup != "" {
				return duplicateObjectError(redactValue(dup, fd.Secret))
			}
		}
		f.Set(out)
		return nil
	}
	doc := map[string]any{}
	for _, r := range raw {
		m, err := decodeObject(r, f.Type())
		if err != nil {
			return err
		}
		mergeObject(doc, m)
	}
	return validateAndBindObject(f, doc, fd.ObjectSchema)
}

// decodeObject turns one occurrence into a JSON-shaped document: JSON when it starts with "{",
// YAML when it spans lines (the usual shape of an @file), key=value pairs otherwise. Pairs are
// typed from the target struct's fields, since argv text carries no type.
func decodeObject(s string, t reflect.Type) (map[string]any, error) {
	trimmed := strings.TrimSpace(s)
	switch {
	case strings.HasPrefix(trimmed, "{"):
		return decodeDocument("json", trimmed)
	case strings.Contains(trimmed, "\n"):
		return decodeDocument("yaml", trimmed)
	case trimmed == "":
		return map[string]any{}, nil
	}
	pairs, err := splitPairs(trimmed)
	if err != nil {
		return nil, err
	}
	doc := map[string]any{}
	for _, pair := range pairs {
		key, val, ok := strings.Cut(pair, "=")
		if !ok || key == "" {
			return nil, fmt.Errorf("%q is not key=value; write host=db.internal,port=5432, or JSON", pair)
		}
		path := strings.Split(key, ".")
		v, list, err := typedField(t, path, val)
		if err != nil {
			return nil, err
		}
		setObjectPath(doc, path, v, list)
	}
	return doc, nil
}

// decodeDocument decodes JSON or YAML text with recon's codecs, the same ones stdin uses, so a
// value is typed identically on either channel.
func decodeDocument(format, text string) (map[string]any, error) {
	codec, ok := recon.DefaultCodecs().ByName(format)
	if !ok {
		return nil, fmt.Errorf("no %s decoder", format)
	}
	m, err := codec.Decode([]byte(text))
	if err != nil {
		return nil, fmt.Errorf("could not read the value as %s: %w", strings.ToUpper(format), err)
	}
	return m, nil
}

// typedField resolves a dotted key against t's JSON field names and converts the text to the
// JSON value the field holds. A list field reports list=true so a repeated key appends
// (tags=a,tags=b). An unknown key is an error naming the keys that exist.
func typedField(t reflect.Type, path []string, text string) (value any, list bool, err error) {
	for i, seg := range path {
		t = derefType(t)
		if t.Kind() == reflect.Map {
			t = t.Elem() // the key is free; the value type still applies
			continue
		}
		if t.Kind() != reflect.Struct {
			return text, false, nil
		}
		field, ok := jsonField(t, seg)
		if !ok {
			return nil, false, fmt.Errorf("unknown key %q (known: %s)", strings.Join(path[:i+1], "."), strings.Join(jsonFieldNames(t), ", "))
		}
		t = field.Type
	}
	t = derefType(t)
	if t.Kind() == reflect.Slice && t.Elem().Kind() != reflect.Uint8 {
		v, _, err := typedField(t.Elem(), nil, text)
		return v, true, err
	}
	switch t.Kind() {
	case reflect.Bool:
		b, err := parseBool(text)
		return b, false, err
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n, err := strconv.ParseInt(text, 10, 64)
		if err != nil {
			return nil, false, fmt.Errorf("%s: %q is not an integer", strings.Join(path, "."), text)
		}
		return n, false, nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		n, err := strconv.ParseUint(text, 10, 64)
		if err != nil {
			return nil, false, fmt.Errorf("%s: %q is not a non-negative integer", strings.Join(path, "."), text)
		}
		return n, false, nil
	case reflect.Float32, reflect.Float64:
		n, err := strconv.ParseFloat(text, 64)
		if err != nil {
			return nil, false, fmt.Errorf("%s: %q is not a number", strings.Join(path, "."), text)
		}
		return n, false, nil
	case reflect.Struct:
		return nil, false, fmt.Errorf("%s is an object; set its fields as %s.<key>=…", strings.Join(path, "."), strings.Join(path, "."))
	case reflect.Interface:
		return inferScalar(text), false, nil
	}
	return text, false, nil
}

// inferScalar types key=value text the schema says nothing about (inside a free-form object or
// a dotted_keys map) as its JSON spelling would decode: true and false are booleans, null is
// nil, a JSON number is a float64, and anything else is the text itself. This keeps
// `replicas=5` and `{"replicas":5}` equivalent. Only JSON scalar forms are inferred; "007",
// "1_000", "yes" and "0x10" stay text.
func inferScalar(text string) any {
	switch text {
	case "true":
		return true
	case "false":
		return false
	case "null":
		return nil
	}
	if text == "" || !strings.ContainsAny(text[:1], "-0123456789") || strings.TrimSpace(text) != text {
		return text
	}
	var n float64
	if err := json.Unmarshal([]byte(text), &n); err == nil {
		return n
	}
	return text
}

func derefType(t reflect.Type) reflect.Type {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t
}

// jsonField finds the struct field a JSON key names, by its json tag (or its Go name).
func jsonField(t reflect.Type, key string) (reflect.StructField, bool) {
	for f := range t.Fields() {
		if name := jsonName(f); name == key {
			return f, true
		}
	}
	return reflect.StructField{}, false
}

func jsonName(f reflect.StructField) string {
	name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
	if name == "" {
		return f.Name
	}
	return name
}

func jsonFieldNames(t reflect.Type) []string {
	var out []string
	for f := range t.Fields() {
		if n := jsonName(f); n != "-" {
			out = append(out, n)
		}
	}
	slices.Sort(out)
	return out
}

// setObjectPath sets doc[path] = v, creating intermediate objects; a list field appends.
func setObjectPath(doc map[string]any, path []string, v any, list bool) {
	for _, seg := range path[:len(path)-1] {
		next, ok := doc[seg].(map[string]any)
		if !ok {
			next = map[string]any{}
			doc[seg] = next
		}
		doc = next
	}
	last := path[len(path)-1]
	if list {
		existing, _ := doc[last].([]any)
		doc[last] = append(existing, v)
		return
	}
	doc[last] = v
}

// mergeObject deep-merges src into dst: nested objects merge, anything else is replaced, so a
// later occurrence of a key wins.
func mergeObject(dst, src map[string]any) {
	for k, v := range src {
		if sm, ok := v.(map[string]any); ok {
			if dm, ok := dst[k].(map[string]any); ok {
				mergeObject(dm, sm)
				continue
			}
		}
		dst[k] = v
	}
}

// validateAndBindObject validates doc against the flag's JSON Schema, then binds it into f.
func validateAndBindObject(f reflect.Value, doc map[string]any, schema string) error {
	if err := validateObject(doc, schema); err != nil {
		return err
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		return fmt.Errorf("could not encode the value: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	target := f
	if f.Kind() == reflect.Pointer {
		target = reflect.New(f.Type().Elem())
		if err := dec.Decode(target.Interface()); err != nil {
			return objectDecodeError(err)
		}
		f.Set(target)
		return nil
	}
	if err := dec.Decode(target.Addr().Interface()); err != nil {
		return objectDecodeError(err)
	}
	return nil
}

// objectDecodeError rephrases encoding/json's complaint about a key the struct does not have.
func objectDecodeError(err error) error {
	if name, ok := strings.CutPrefix(err.Error(), "json: unknown field "); ok {
		return fmt.Errorf("unknown key %s", name)
	}
	return err
}

var objectValidators sync.Map // schema text → *recon.JSONSchemaValidator

// validateObject checks doc against a JSON Schema, reporting every problem with its key path.
func validateObject(doc map[string]any, schema string) error {
	cached, ok := objectValidators.Load(schema)
	if !ok {
		v, err := recon.NewJSONSchemaValidator([]byte(schema))
		if err != nil {
			return fmt.Errorf("rotini: invalid object schema: %w", err)
		}
		cached, _ = objectValidators.LoadOrStore(schema, v)
	}
	validator, ok := cached.(*recon.JSONSchemaValidator)
	if !ok {
		return errors.New("rotini: object validator cache holds a foreign value")
	}
	err := validator.Validate(doc)
	if err == nil {
		return nil
	}
	applyPatternMessages(schema, err)
	var msgs []string
	collectValidation(err, &msgs)
	if len(msgs) == 0 {
		return fmt.Errorf("the value does not match its schema: %w", err)
	}
	return errors.New(strings.Join(msgs, "; "))
}

// patternMessageCache holds each schema document's pattern messages, keyed by schema text, so
// the schema walk happens once per schema.
var patternMessageCache sync.Map // string → map[string]string

// applyPatternMessages rewrites each pattern failure in err whose property declares a
// `pattern_message`, so a JSON-Schema-validated value (an object flag, a stdin payload, a
// configuration file) reports the author's message rather than the regex. Other failures are
// untouched.
func applyPatternMessages(schema string, err error) {
	cached, ok := patternMessageCache.Load(schema)
	if !ok {
		var doc map[string]any
		msgs := map[string]string{}
		if json.Unmarshal([]byte(schema), &doc) == nil {
			defs, _ := doc["definitions"].(map[string]any)
			collectPatternMessages(doc, defs, nil, msgs, 0)
		}
		cached, _ = patternMessageCache.LoadOrStore(schema, msgs)
	}
	msgs, _ := cached.(map[string]string)
	if len(msgs) == 0 {
		return
	}
	var walk func(error)
	walk = func(err error) {
		if joined, ok := err.(interface{ Unwrap() []error }); ok {
			for _, e := range joined.Unwrap() {
				walk(e)
			}
			return
		}
		if ve, ok := errors.AsType[*recon.ValidationError](err); ok && ve.Rule == "pattern" {
			if msg, ok := msgs[wildcardIndexes(ve.Path.String())]; ok {
				ve.Msg = msg
			}
		}
	}
	walk(err)
}

// collectPatternMessages records every pattern_message in a JSON schema under the dotted path
// of the value it governs, with "*" standing for a list index. depth bounds a recursive $ref.
func collectPatternMessages(node, defs map[string]any, path []string, out map[string]string, depth int) {
	if node == nil || depth > 32 {
		return
	}
	if ref, ok := node["$ref"].(string); ok {
		if def, ok := defs[strings.TrimPrefix(ref, "#/definitions/")].(map[string]any); ok {
			collectPatternMessages(def, defs, path, out, depth+1)
		}
	}
	if msg, ok := node["pattern_message"].(string); ok && msg != "" {
		out[strings.Join(path, ".")] = msg
	}
	if props, ok := node["properties"].(map[string]any); ok {
		for key, p := range props {
			sub, _ := p.(map[string]any)
			collectPatternMessages(sub, defs, append(slices.Clip(path), key), out, depth+1)
		}
	}
	if items, ok := node["items"].(map[string]any); ok {
		collectPatternMessages(items, defs, append(slices.Clip(path), "*"), out, depth+1)
	}
}

// wildcardIndexes writes each numeric segment of a dotted value path as "*", the form
// collectPatternMessages keys list elements by: "mounts.0.src" → "mounts.*.src".
func wildcardIndexes(path string) string {
	segs := strings.Split(path, ".")
	for i, s := range segs {
		if _, err := strconv.Atoi(s); err == nil {
			segs[i] = "*"
		}
	}
	return strings.Join(segs, ".")
}

// collectValidation flattens recon's (possibly joined) validation errors into "key: problem"
// lines, naming an unexpected key as unknown rather than recon's "schema is false".
func collectValidation(err error, out *[]string) {
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		for _, e := range joined.Unwrap() {
			collectValidation(e, out)
		}
		return
	}
	if ve, ok := errors.AsType[*recon.ValidationError](err); ok {
		msg := ve.Msg
		if strings.Contains(msg, "schema is false") {
			*out = append(*out, fmt.Sprintf("unknown key %q", ve.Path.String()))
			return
		}
		if p := ve.Path.String(); p != "" {
			msg = p + ": " + msg
		}
		*out = append(*out, msg)
	}
}

// objectFieldFlag resolves a per-field flag — `--db.host` for an object flag `--db` — to the
// object flag and the field path it sets, or ok=false.
func objectFieldFlag(chain []Command, name string) (fd FlagDef, idx int, field string, ok bool) {
	if !strings.HasPrefix(name, "--") {
		return FlagDef{}, 0, "", false
	}
	base, field, found := strings.Cut(name, ".")
	if !found || field == "" {
		return FlagDef{}, 0, "", false
	}
	fd, idx, ok = findFlagIndex(chain, base)
	if !ok || !isObjectFlag(fd) || isArrayType(fd.Type) {
		return FlagDef{}, 0, "", false
	}
	return fd, idx, field, true
}

// splitPairs splits key=value,key=value on commas outside double quotes. A quoted value keeps
// its commas — host="a,b" — as does a quoted whole pair, "host=a,b"; inside quotes "" is a
// literal quote. Spaces after a comma are dropped.
func splitPairs(s string) ([]string, error) {
	var (
		pairs   []string
		cur     strings.Builder
		inQuote bool
	)
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '"' && inQuote && i+1 < len(s) && s[i+1] == '"':
			cur.WriteByte('"')
			i++
		case c == '"':
			inQuote = !inQuote
		case c == ',' && !inQuote:
			pairs = append(pairs, cur.String())
			cur.Reset()
			for i+1 < len(s) && s[i+1] == ' ' {
				i++
			}
		default:
			cur.WriteByte(c)
		}
	}
	if inQuote {
		return nil, fmt.Errorf("%q has an unclosed double quote", s)
	}
	return append(pairs, cur.String()), nil
}

// quotePair renders key=value as one pair that survives splitPairs whatever the value holds.
func quotePair(key, value string) string {
	if !strings.ContainsAny(value, `,"`) {
		return key + "=" + value
	}
	return key + `="` + strings.ReplaceAll(value, `"`, `""`) + `"`
}
