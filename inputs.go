package rotini

import (
	"encoding"
	"reflect"
	"strconv"
	"strings"
	"time"
)

// parsedInputs is the runtime's parsed argv for one invocation, keyed by
// command-name scope. The parser (program.Execute) populates it before any
// handler hook runs; [Inputs] reads it. Keying by command name — not the
// parent-prefixed path — is what lets a statically-composed child read its
// inputs through its own generated types unchanged.
type parsedInputs struct {
	scopes map[string]scopeInputs
}

// scopeInputs holds one command scope's parsed values: flag values by logical
// name (a repeatable flag keeps every value) and the positional arguments.
type scopeInputs struct {
	flags map[string][]string
	args  []string
}

// bindParsed stores the parsed inputs for retrieval via [Inputs]. The runtime
// calls it once, before hooks run.
func (r *Rtx) bindParsed(p *parsedInputs) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.parsed = p
}

// Inputs returns the typed inputs struct for the running command, e.g.
// Inputs[rtg.MycliGreetInputs](rtx). It builds T by reflection from the parsed
// argv on rtx using the `rotini:"…"` struct tags the framework package emits:
// each per-command field carries a `scope=<command-name>` tag, and each flag or
// argument field carries its logical name. Values are coerced to the field's Go
// type — built-ins natively and any type implementing encoding.TextUnmarshaler.
// Positional arguments bind by declaration order; a trailing []string field
// absorbs the remaining positionals.
//
// Inputs returns the zero value of T when argv has not been parsed (e.g. before
// the runtime is wired). Type validation is the parser's responsibility, so the
// binder is best-effort and never panics on malformed input.
func Inputs[T any](rtx Context) T {
	var out T
	if rtx == nil {
		return out
	}
	rtx.mu.RLock()
	p := rtx.parsed
	rtx.mu.RUnlock()
	if p == nil {
		return out
	}
	bindInputs(reflect.ValueOf(&out).Elem(), p)
	return out
}

// bindInputs fills a <Cmd>Inputs struct: one field per command scope, each
// tagged `rotini:"scope=<command-name>"`.
func bindInputs(v reflect.Value, p *parsedInputs) {
	if v.Kind() != reflect.Struct {
		return
	}
	t := v.Type()
	for i := range v.NumField() {
		scope, ok := strings.CutPrefix(t.Field(i).Tag.Get("rotini"), "scope=")
		if !ok {
			continue
		}
		if si, ok := p.scopes[scope]; ok {
			bindCommandInputs(v.Field(i), si)
		}
	}
}

// bindCommandInputs fills a <Cmd>CommandInputs struct's Flags and Arguments.
func bindCommandInputs(v reflect.Value, si scopeInputs) {
	if v.Kind() != reflect.Struct {
		return
	}
	t := v.Type()
	for i := range v.NumField() {
		switch t.Field(i).Name {
		case "Flags":
			bindFlags(v.Field(i), si.flags)
		case "Arguments":
			bindArgs(v.Field(i), si.args)
		}
	}
}

// bindFlags fills a <Cmd>Flags struct by matching each field's `rotini:"<name>"`
// tag against the parsed flag values.
func bindFlags(v reflect.Value, flags map[string][]string) {
	if v.Kind() != reflect.Struct {
		return
	}
	t := v.Type()
	for i := range v.NumField() {
		name := t.Field(i).Tag.Get("rotini")
		if name == "" {
			continue
		}
		if raw, ok := flags[name]; ok {
			coerce(v.Field(i), raw)
		}
	}
}

// bindArgs fills a <Cmd>Arguments struct positionally; a trailing []string
// field is variadic and absorbs all remaining positionals.
func bindArgs(v reflect.Value, args []string) {
	if v.Kind() != reflect.Struct {
		return
	}
	idx := 0
	for i := range v.NumField() {
		f := v.Field(i)
		if f.Kind() == reflect.Slice && f.Type().Elem().Kind() == reflect.String {
			coerce(f, args[min(idx, len(args)):])
			idx = len(args)
			continue
		}
		if idx < len(args) {
			coerce(f, args[idx:idx+1])
			idx++
		}
	}
}

var (
	durationType        = reflect.TypeOf(time.Duration(0))
	textUnmarshalerType = reflect.TypeOf((*encoding.TextUnmarshaler)(nil)).Elem()
)

// coerce sets f from the raw string value(s). It is best-effort: malformed
// values (which the parser should have rejected) leave the field at its zero
// value rather than panicking.
func coerce(f reflect.Value, raw []string) {
	if len(raw) == 0 {
		return
	}
	if f.Kind() == reflect.Pointer {
		if f.IsNil() {
			f.Set(reflect.New(f.Type().Elem()))
		}
		coerce(f.Elem(), raw)
		return
	}
	last := raw[len(raw)-1]

	if f.Type() == durationType {
		if d, err := time.ParseDuration(last); err == nil {
			f.SetInt(int64(d))
		}
		return
	}
	if f.CanAddr() && f.Addr().Type().Implements(textUnmarshalerType) {
		_ = f.Addr().Interface().(encoding.TextUnmarshaler).UnmarshalText([]byte(last))
		return
	}

	switch f.Kind() {
	case reflect.Bool:
		if b, err := strconv.ParseBool(last); err == nil {
			f.SetBool(b)
		}
	case reflect.String:
		f.SetString(last)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if n, err := strconv.ParseInt(last, 10, 64); err == nil {
			f.SetInt(n)
		}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		if n, err := strconv.ParseUint(last, 10, 64); err == nil {
			f.SetUint(n)
		}
	case reflect.Float32, reflect.Float64:
		if x, err := strconv.ParseFloat(last, 64); err == nil {
			f.SetFloat(x)
		}
	case reflect.Slice:
		if f.Type().Elem().Kind() == reflect.String {
			f.Set(reflect.ValueOf(append([]string{}, raw...)))
		}
	}
}
