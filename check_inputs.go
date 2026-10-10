package rotini

import (
	"encoding"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"
)

// CheckInputs reports whether v satisfies the spec for the running command: required inputs,
// enums, bounds, lengths, patterns, item counts, object schemas, flag groups and flag
// dependencies. It is the input-side partner of [Context.CheckOutput], for inputs the program
// collected itself: from a prompt, a secrets service, a test, or handler code.
//
// set says which fields were supplied. Presence rules (required inputs, flag groups and flag
// dependencies) read it, and value rules apply only to the fields it names, so a zero value
// left unset is not checked against its enum or bounds. As on the command line, a list flag or
// variadic argument left out has zero items, so its minItems applies unless it has a default.
// [PresenceOf] builds a set from v's non-zero fields; build one by hand when a zero value must
// count as supplied. A declared default satisfies a required input, as it does for
// [Context.Inputs].
//
// A streamed stdin field (an iterator) counts as supplied when it is non-nil; its items are
// checked as they are read, not here.
//
// It reads no channel, applies no defaults and never changes v. T must describe the running
// command, as for Context.Inputs. Each input is named by its canonical spelling: a flag's first
// long identifier, an argument's <name>, an environment variable's name, a config key. A
// short-circuit flag ([FlagDef.ShortCircuit]) set true in v and named in set waives every check.
//
// Failures are the same values [Context.Inputs] returns: a [*ParseError] for flags, arguments
// and the cross-flag rules, an [*InputError] for environment, config and stdin inputs, both in
// [CategoryUsage]. A T that does not describe the running command is a [ParseKindInternal]
// error.
//
//	in := askForMissing(rtx, argv.Values)
//	if err := rtx.CheckInputs(in, rotini.PresenceOf(in)); err != nil {
//		rtx.HaltWith(err)
//		return
//	}
func (rtx *Context) CheckInputs[T any](v T, set Presence) error {
	chain, err := layerChain(rtx)
	if err != nil {
		return err
	}
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Struct {
		return &ParseError{Kind: ParseKindInternal, Msg: "rotini: CheckInputs needs a generated inputs struct"}
	}
	anchor, err := layerAnchor(rtx, rv, chain)
	if err != nil {
		return err
	}
	if err := checkChainAlignment(rv, chain, anchor); err != nil {
		return err
	}
	if typedShortCircuited(rv, chain, anchor, set) {
		return nil
	}

	store := presenceStore(rv, chain, anchor, set)
	if err := requiredErrors(chain, store); err != nil {
		return err
	}
	if err := checkTypedValues(rv, chain, anchor, func(p FieldPath) bool { _, ok := set[p]; return ok }, readerFor(rtx).stdinSchemas, rtx.osView()); err != nil {
		return err
	}
	if err := checkAbsentItemCounts(rv, chain, anchor, set); err != nil {
		return err
	}
	if err := checkTypedChannelPresence(rv, chain, anchor, set); err != nil {
		return err
	}
	if err := validateFlagGroups(chain, store); err != nil {
		return err
	}
	return validateFlagDependencies(chain, store)
}

// PresenceOf returns a [Presence] marking every non-zero input field of v as supplied, in the
// shape [Context.CheckInputs] and a hand-built [InputLayer] read. A field holding its zero value
// (false, 0, "") is not marked, so build the Presence by hand when a zero value must count as
// supplied. A non-nil pointer to a zero value (a nullable input) is marked. Each field's source
// is Layer "custom"; in a merge, the hand-built [InputLayer]'s Name replaces it.
func PresenceOf[T any](v T) Presence {
	set := Presence{}
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Struct {
		return set
	}
	for i := range rv.NumField() {
		ci := rv.Field(i)
		if ci.Kind() != reflect.Struct {
			continue
		}
		top := rv.Type().Field(i).Name
		for _, channel := range []string{"Flags", "Arguments", "Env", "Config"} {
			eachTaggedField(ci, channel, func(fieldName, _ string, _ reflect.StructTag, f reflect.Value) {
				if !f.IsZero() {
					set[fieldPath(top, channel, fieldName)] = InputSource{Layer: "custom"}
				}
			})
		}
		if sf := ci.FieldByName("Stdin"); stdinSet(sf) {
			set[fieldPath(top, "Stdin")] = InputSource{Layer: "custom"}
		}
	}
	return set
}

// typedShortCircuited reports whether a short-circuit flag is supplied as true in v.
func typedShortCircuited(v reflect.Value, chain []Command, anchor int, set Presence) bool {
	found := false
	walkCommandStructs(v, chain, anchor, func(top string, scope int, ci reflect.Value) {
		eachTaggedField(ci, "Flags", func(fieldName, logical string, _ reflect.StructTag, f reflect.Value) {
			fd, ok := findFlagDef(chain[scope].Flags, logical)
			if _, given := set[fieldPath(top, "Flags", fieldName)]; !ok || !given || !fd.ShortCircuit {
				return
			}
			if f = indirectValue(f); f.IsValid() && f.Kind() == reflect.Bool && f.Bool() {
				found = true
			}
		})
	})
	return found
}

// presenceStore builds the store the presence rules read ([requiredErrors],
// [validateFlagGroups], [validateFlagDependencies]) from a Presence: a supplied flag is present
// and set; a flag or argument with a declared default is present but not set, as it is for
// Context.Inputs. It holds no values.
func presenceStore(v reflect.Value, chain []Command, anchor int, set Presence) *parsedInputs {
	store := &parsedInputs{scopes: make([]scopeInputs, len(chain)), argvSet: make([]map[string]bool, len(chain))}
	store.span = &[2]int{anchor, anchor + v.NumField()}
	walkCommandStructs(v, chain, anchor, func(top string, scope int, ci reflect.Value) {
		frame := chain[scope]
		eachTaggedField(ci, "Flags", func(fieldName, logical string, _ reflect.StructTag, f reflect.Value) {
			fd, ok := findFlagDef(frame.Flags, logical)
			if !ok {
				return
			}
			_, given := set[fieldPath(top, "Flags", fieldName)]
			if given || len(flagDefaults(fd)) > 0 {
				if store.scopes[scope].flags == nil {
					store.scopes[scope].flags = map[string][]string{}
				}
				store.scopes[scope].flags[logical] = nil
			}
			if given {
				if store.argvSet[scope] == nil {
					store.argvSet[scope] = map[string]bool{}
				}
				store.argvSet[scope][logical] = true
				store.scopes[scope].noteDependencyValue(logical, f)
			}
		})
		if scope != len(chain)-1 {
			return
		}
		// Positionals fill in order, so the leading run of supplied (or defaulted) arguments is
		// what counts toward the required check.
		present := func(ad ArgDef) bool {
			path, ok := argPath(ci, top, ad.Name)
			_, given := set[path]
			return (ok && given) || ad.Default != ""
		}
		store.scopes[scope].args = make([]string, suppliedArgs(frame.Arguments, present))
	})
	return store
}

// suppliedArgs counts the positionals that stand for the present arguments: the leading run
// of present ones, since positionals fill in order. Fixed arguments after a variadic bind from
// the end, so they count only when all are present, behind the leading run and the variadic.
func suppliedArgs(args []ArgDef, present func(ArgDef) bool) int {
	n := 0
	v := variadicIndex(args)
	for i, ad := range args {
		if !present(ad) || (i == v && hasArgTail(args)) {
			break
		}
		n++
	}
	if hasArgTail(args) && n == v {
		tail := args[v+1:]
		if !slices.ContainsFunc(tail, func(ad ArgDef) bool { return !present(ad) }) {
			if present(args[v]) {
				n++
			}
			n += len(tail)
		}
	}
	return n
}

// withHandBuilt returns a copy of the store in which every input a hand-built layer supplied
// is present for the presence rules (and set, for flag groups and dependencies) without
// adding a value the value rules would read.
func (p *parsedInputs) withHandBuilt(merged reflect.Value, chain []Command, anchor int, handBuilt map[FieldPath]bool) *parsedInputs {
	out := &parsedInputs{scopes: make([]scopeInputs, len(p.scopes)), argvSet: make([]map[string]bool, len(p.argvSet)), span: p.span, detached: p.detached, dashedFirst: p.dashedFirst, lateFlag: p.lateFlag}
	for i, si := range p.scopes {
		cp := si
		cp.flags = maps.Clone(si.flags)
		cp.depValues = maps.Clone(si.depValues)
		cp.args = append([]string(nil), si.args...)
		out.scopes[i] = cp
	}
	for i, m := range p.argvSet {
		out.argvSet[i] = maps.Clone(m)
	}
	walkCommandStructs(merged, chain, anchor, func(top string, scope int, ci reflect.Value) {
		si := &out.scopes[scope]
		eachTaggedField(ci, "Flags", func(fieldName, logical string, _ reflect.StructTag, f reflect.Value) {
			if !handBuilt[fieldPath(top, "Flags", fieldName)] {
				return
			}
			if si.flags == nil {
				si.flags = map[string][]string{}
			}
			if si.presenceOnly == nil {
				si.presenceOnly = map[string]bool{}
			}
			si.flags[logical] = nil
			si.presenceOnly[logical] = true
			si.noteDependencyValue(logical, f)
			if out.argvSet[scope] == nil {
				out.argvSet[scope] = map[string]bool{}
			}
			out.argvSet[scope][logical] = true
		})
		if scope != len(chain)-1 {
			return
		}
		for i, ad := range chain[scope].Arguments {
			path, ok := argPath(ci, top, ad.Name)
			if !ok || !handBuilt[path] {
				continue
			}
			if i < len(si.args) {
				if si.handBuiltArgs == nil {
					si.handBuiltArgs = map[int]bool{}
				}
				si.handBuiltArgs[i] = true
				continue
			}
			for len(si.args) <= i {
				si.args = append(si.args, "")
				si.placeholderArgs++
			}
		}
	})
	return out
}

// argPath returns the FieldPath of the Arguments field bound to the logical argument name.
func argPath(ci reflect.Value, top, name string) (FieldPath, bool) {
	var path FieldPath
	found := false
	eachTaggedField(ci, "Arguments", func(fieldName, logical string, _ reflect.StructTag, _ reflect.Value) {
		if logical == name {
			path, found = fieldPath(top, "Arguments", fieldName), true
		}
	})
	return path, found
}

// findArgDef finds an argument definition by its logical name.
func findArgDef(defs []ArgDef, name string) (ArgDef, bool) {
	for _, d := range defs {
		if d.Name == name {
			return d, true
		}
	}
	return ArgDef{}, false
}

// checkTypedValues runs the value rules over the typed fields include selects: enum, bounds,
// lengths, patterns, path checks, item counts and object schemas for flags and arguments, the
// tag-declared enum and constraints for environment and config fields, and the stdin schema
// (when stdinSchemas is given). It is the typed half of the rule core the command line shares.
// view names environment variables and resolves relative paths.
func checkTypedValues(v reflect.Value, chain []Command, anchor int, include func(FieldPath) bool, stdinSchemas map[string]string, view *osView) error {
	var err error
	var dash dashOnce // "-" may name stdin once across the inputfile fields
	walkCommandStructs(v, chain, anchor, func(top string, scope int, ci reflect.Value) {
		if err != nil {
			return
		}
		frame := chain[scope]
		eachTaggedField(ci, "Flags", func(fieldName, logical string, _ reflect.StructTag, f reflect.Value) {
			if err != nil || !include(fieldPath(top, "Flags", fieldName)) {
				return
			}
			if fd, ok := findFlagDef(frame.Flags, logical); ok {
				err = dash.typed(checkTypedFlag(fd, f, view.base()), fd.Type, flagLabel(fd), flagLabel(fd), f)
			}
		})
		if err == nil && scope == len(chain)-1 {
			eachTaggedField(ci, "Arguments", func(fieldName, logical string, _ reflect.StructTag, f reflect.Value) {
				if err != nil || !include(fieldPath(top, "Arguments", fieldName)) {
					return
				}
				if ad, ok := findArgDef(frame.Arguments, logical); ok {
					err = dash.typed(checkTypedArg(ad, f, view.base()), ad.Type, "<"+ad.Name+">", "", f)
				}
			})
		}
		for _, channel := range []string{"Env", "Config"} {
			eachTaggedField(ci, channel, func(fieldName, _ string, tag reflect.StructTag, f reflect.Value) {
				if err != nil || !include(fieldPath(top, channel, fieldName)) {
					return
				}
				err = checkTypedChannelField(channel, tag, f, view)
			})
		}
		if err == nil && stdinSchemas != nil && scope == len(chain)-1 && include(fieldPath(top, "Stdin")) {
			err = checkTypedStdin(ci, stdinSchemas)
		}
	})
	return err
}

// checkAbsentItemCounts applies minItems to the list flags and arguments set leaves out, as
// the command line does: an absent list has zero items. A declared default supplies the items
// instead. Environment and config lists are checked only when supplied, as when they are read.
func checkAbsentItemCounts(v reflect.Value, chain []Command, anchor int, set Presence) error {
	var err error
	walkCommandStructs(v, chain, anchor, func(top string, scope int, ci reflect.Value) {
		frame := chain[scope]
		eachTaggedField(ci, "Flags", func(fieldName, logical string, _ reflect.StructTag, _ reflect.Value) {
			fd, ok := findFlagDef(frame.Flags, logical)
			if _, given := set[fieldPath(top, "Flags", fieldName)]; err != nil || !ok || given || len(flagDefaults(fd)) > 0 {
				return
			}
			err = checkItemCount(flagLabel(fd), fd.Constraints, 0)
		})
		if scope != len(chain)-1 {
			return
		}
		eachTaggedField(ci, "Arguments", func(fieldName, logical string, _ reflect.StructTag, _ reflect.Value) {
			ad, ok := findArgDef(frame.Arguments, logical)
			if _, given := set[fieldPath(top, "Arguments", fieldName)]; err != nil || !ok || given || !ad.Variadic || ad.Default != "" {
				return
			}
			err = checkItemCount("<"+ad.Name+">", ad.Constraints, 0)
		})
	})
	return err
}

// checkTypedFlag applies a flag's value rules to its typed field, naming the flag canonically.
func checkTypedFlag(fd FlagDef, f reflect.Value, dir string) error {
	label := flagLabel(fd)
	elems, count, ok := typedElems(f)
	if !ok {
		return nil
	}
	if fd.ObjectSchema != "" {
		if fd.UniqueItems && isArrayType(fd.Type) {
			if err := checkUniqueTyped(label, elems, fd.Secret); err != nil {
				return err
			}
		}
		return checkTypedObject(label, fd.ObjectSchema, elems)
	}
	enum := flagEnum(fd)
	for _, s := range typedTexts(elems) {
		if enum.declared() && !enum.has(s) {
			return enum.violation(label, label, s, fd.Secret)
		}
	}
	return checkTypedConstraints(label, fd.Type, fd.Constraints, count, elems, fd.Secret, dir)
}

// checkTypedArg applies an argument's value rules to its typed field.
func checkTypedArg(ad ArgDef, f reflect.Value, dir string) error {
	label := "<" + ad.Name + ">"
	elems, count, ok := typedElems(f)
	if !ok {
		return nil
	}
	enum := argEnum(ad)
	for _, s := range typedTexts(elems) {
		if enum.declared() && !enum.has(s) {
			return enum.violation(label, "", s, ad.Secret)
		}
	}
	return checkTypedConstraints(label, ad.Type, ad.Constraints, count, elems, ad.Secret, dir)
}

// checkTypedChannelField applies an environment or config field's tag-declared enum and
// constraints, labeled as the channel validation labels it.
func checkTypedChannelField(channel string, tag reflect.StructTag, f reflect.Value, view *osView) error {
	c, has := channelConstraints(tag)
	enum := channelEnum(tag)
	if !has && !enum.declared() && tag.Get("path") == "" {
		return nil
	}
	body := tag.Get("recon")
	key := reconKey(body)
	label := tag.Get("rotini")
	if label == "" {
		label = key
	}
	ch := channelConfig
	if channel == "Env" {
		ch = channelEnv
		if env := tag.Get("env"); env != "" {
			label = chosenEnv(view, env)
		}
	}
	elems, count, ok := typedElems(f)
	if !ok {
		return nil
	}
	secret := reconHasSecret(body)
	if err := checkChannelEnum(ch, label, enum, typedTexts(elems), secret); err != nil {
		return err
	}
	typ := channelFieldType(tag, f.Type())
	if isPathType(typ) && len(elems) == 1 && typedText(elems[0]) == "" {
		typ = channelGoType(f.Type()) // an empty path is no path: nothing to check
	}
	return checkTypedConstraints(label, typ, c, count, elems, secret, view.base())
}

// checkTypedChannelPresence reports a required environment, config or stdin input the
// Presence does not supply. A recon default satisfies it.
func checkTypedChannelPresence(v reflect.Value, chain []Command, anchor int, set Presence) error {
	var err error
	walkCommandStructs(v, chain, anchor, func(top string, scope int, ci reflect.Value) {
		for _, channel := range []string{"Env", "Config"} {
			eachTaggedField(ci, channel, func(fieldName, _ string, tag reflect.StructTag, _ reflect.Value) {
				if err != nil {
					return
				}
				body := tag.Get("recon")
				if !reconHasOption(body, "required") || reconDefault(body) != "" {
					return
				}
				if _, given := set[fieldPath(top, channel, fieldName)]; given {
					return
				}
				key := reconKey(body)
				if channel == "Env" {
					var names []string
					if tag.Get("env") != "" {
						names = strings.Split(tag.Get("env"), ",")
					}
					err = usageBind(channelEnv, key, envUnsetLabel(names, key)+" is required", nil)
					return
				}
				err = usageBind(channelConfig, key, "config key "+key+" is required", nil)
			})
		}
		if err != nil || scope != len(chain)-1 {
			return
		}
		field, ok := ci.Type().FieldByName("Stdin")
		if !ok {
			return
		}
		tag := parseStdinTag(field.Tag.Get("stdin"))
		if !tag.required || (tag.unless != "" && typedArgGiven(ci, tag.unless)) {
			return // not required, or the file argument is the input
		}
		if _, given := set[fieldPath(top, "Stdin")]; !given {
			err = usageBind(channelStdin, "", "required stdin payload is missing", nil)
		}
	})
	return err
}

// reconHasOption reports whether a recon struct-tag body carries the bare option opt.
func reconHasOption(body, opt string) bool {
	for _, o := range strings.Split(body, ",")[1:] {
		if strings.TrimSpace(o) == opt {
			return true
		}
	}
	return false
}

// checkTypedStdin validates a supplied stdin document against the command's stdin schema, or
// each record of a JSON Lines payload against its record schema. A streamed stdin is not
// checked here: its items are checked as they are read.
func checkTypedStdin(ci reflect.Value, schemas map[string]string) error {
	sf := ci.FieldByName("Stdin")
	if !sf.IsValid() || sf.Kind() != reflect.Pointer || sf.IsNil() {
		return nil
	}
	if field, ok := ci.Type().FieldByName("Stdin"); ok && parseStdinTag(field.Tag.Get("stdin")).format == "jsonl" {
		return checkTypedRecords(sf.Elem(), schemas)
	}
	js := schemas[sf.Type().Elem().Name()]
	if js == "" {
		return nil
	}
	if sf.Type().Elem().Kind() != reflect.Struct {
		raw, err := json.Marshal(sf.Interface())
		if err != nil {
			return internalBind(channelStdin, "", "could not encode the stdin payload", err)
		}
		return validateDocumentJSON(js, raw)
	}
	doc, err := toDocument(sf.Interface())
	if err != nil {
		return internalBind(channelStdin, "", "could not encode the stdin payload", err)
	}
	validator, err := schemaValidator(js)
	if err != nil {
		return internalBind(channelStdin, "", "invalid stdin schema", err)
	}
	if err := validator.Validate(doc); err != nil {
		applyPatternMessages(js, err)
		return reconBind(channelStdin, err)
	}
	return nil
}

// checkTypedObject validates each object value against the flag's JSON Schema.
func checkTypedObject(label, schema string, elems []reflect.Value) error {
	for _, e := range elems {
		doc, err := toDocument(e.Interface())
		if err == nil {
			err = validateObject(doc, schema)
		}
		if err != nil {
			return &ParseError{Kind: ParseKindInvalidValue, Msg: fmt.Sprintf("%s: %v", label, err), Flag: label}
		}
	}
	return nil
}

// toDocument turns a typed value into the JSON-shaped document a schema validator reads.
func toDocument(v any) (map[string]any, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("could not encode the value: %w", err)
	}
	doc := map[string]any{}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("the value is not an object: %w", err)
	}
	return doc, nil
}

// checkTypedConstraints is [checkConstraints] over typed values: count is the collection's
// size (for item-count bounds) and elems its values, each checked by the input's element type.
func checkTypedConstraints(label, typ string, c Constraints, count int, elems []reflect.Value, secret bool, dir string) error {
	if isArrayType(typ) || isMapType(typ) {
		if err := checkItemCount(label, c, count); err != nil {
			return err
		}
	}
	if c.UniqueItems && isArrayType(typ) {
		if err := checkUniqueTyped(label, elems, secret); err != nil {
			return err
		}
	}
	elem := constraintElemType(typ)
	for _, e := range elems {
		var err error
		switch {
		case isNumericType(elem):
			if n, ok := typedNumber(e); ok {
				err = checkNumberBounds(label, c, n, redactValue(typedText(e), secret), formatNum)
			}
		case measuredTypes[elem] != nil:
			if n, ok := typedNumber(e); ok {
				m := measuredTypes[elem]
				err = checkNumberBounds(label, c, n, redactValue(m.format(n), secret), m.format)
			}
		case isPathType(elem):
			s := typedText(e)
			if err = checkStringBounds(label, c, s, secret); err == nil {
				err = checkPathExists(label, elem, s, dir)
			}
		case elem == "string":
			err = checkStringBounds(label, c, typedText(e), secret)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// indirectValue follows pointers, returning the invalid Value for a nil one.
func indirectValue(f reflect.Value) reflect.Value {
	for f.IsValid() && f.Kind() == reflect.Pointer {
		if f.IsNil() {
			return reflect.Value{}
		}
		f = f.Elem()
	}
	return f
}

// typedElems returns the values a field's rules apply to: a slice's elements, a scalar alone,
// and none for a map (whose rules are its item count). count is the collection size. ok is
// false for a nil pointer, which supplies nothing.
func typedElems(f reflect.Value) (elems []reflect.Value, count int, ok bool) {
	f = indirectValue(f)
	if !f.IsValid() {
		return nil, 0, false
	}
	switch {
	case f.Kind() == reflect.Map:
		return nil, f.Len(), true
	case f.Kind() == reflect.Slice && f.Type().Elem().Kind() != reflect.Uint8:
		out := make([]reflect.Value, f.Len())
		for i := range f.Len() {
			out[i] = f.Index(i)
		}
		return out, f.Len(), true
	}
	return []reflect.Value{f}, 1, true
}

// typedTexts renders values as the text enums and patterns compare.
func typedTexts(elems []reflect.Value) []string {
	out := make([]string, len(elems))
	for i, e := range elems {
		out[i] = typedText(e)
	}
	return out
}

// typed checks the typed field f of an input of type typ for "-" after its value rules passed
// (err is their result, returned when set): label is the input, flag its flag label or "".
func (d *dashOnce) typed(err error, typ, label, flag string, f reflect.Value) error {
	if err != nil || constraintElemType(typ) != typeInputFile {
		return err
	}
	elems, _, ok := typedElems(f)
	if !ok {
		return nil
	}
	return d.seen(label, flag, typedTexts(elems))
}

// typedNumber returns a numeric value's float64, false for a non-number.
func typedNumber(e reflect.Value) (float64, bool) {
	switch e.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return float64(e.Int()), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return float64(e.Uint()), true
	case reflect.Float32, reflect.Float64:
		return e.Float(), true
	}
	return 0, false
}

// typedText renders one typed value as the text a user would have typed for it.
func typedText(e reflect.Value) string {
	if e.Kind() == reflect.String {
		return e.String()
	}
	if e.Type() == durationType {
		return formatDuration(time.Duration(e.Int()))
	}
	if e.CanInterface() {
		if m, ok := reflect.TypeAssert[encoding.TextMarshaler](e); ok {
			if b, err := m.MarshalText(); err == nil {
				return string(b)
			}
		}
		if s, ok := reflect.TypeAssert[fmt.Stringer](e); ok {
			return s.String()
		}
	}
	switch e.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(e.Int(), 10)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return strconv.FormatUint(e.Uint(), 10)
	case reflect.Float32, reflect.Float64:
		return formatNum(e.Float())
	case reflect.Bool:
		return strconv.FormatBool(e.Bool())
	}
	if e.CanInterface() {
		return fmt.Sprint(e.Interface())
	}
	return ""
}

// typedArgGiven reports whether the command's argument name holds a value other than "-" in
// the typed value ci, so a stdin that reads only when no file is given is not needed.
func typedArgGiven(ci reflect.Value, name string) bool {
	given := false
	eachTaggedField(ci, "Arguments", func(_, logical string, _ reflect.StructTag, f reflect.Value) {
		if logical != name {
			return
		}
		switch f.Kind() {
		case reflect.String:
			given = f.String() != "" && f.String() != "-"
		case reflect.Slice:
			given = f.Len() > 0
			for i := range f.Len() {
				if e := f.Index(i); e.Kind() == reflect.String && e.String() == "-" {
					given = false
				}
			}
		default:
			given = !f.IsZero()
		}
	})
	return given
}

// checkTypedRecords validates each record of a JSON Lines payload against its record schema.
func checkTypedRecords(records reflect.Value, schemas map[string]string) error {
	if records.Kind() != reflect.Slice {
		return nil
	}
	js := schemas[records.Type().Elem().Name()]
	if js == "" {
		return nil
	}
	for i := range records.Len() {
		raw, err := json.Marshal(records.Index(i).Interface())
		if err != nil {
			return internalBind(channelStdin, "", "could not encode the stdin payload", err)
		}
		if err := validateDocumentJSON(js, raw); err != nil {
			if ie, ok := errors.AsType[*InputError](err); ok {
				return &InputError{Channel: channelStdin, Input: ie.Input, Msg: fmt.Sprintf("stdin record %d: %s", i+1, ie.Msg), Cause: ie.Cause, usage: ie.usage}
			}
			return err
		}
	}
	return nil
}
