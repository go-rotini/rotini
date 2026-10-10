package contractdiff

import (
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
)

// direction says whose values a schema describes: a caller's (in) or the program's (out).
// Narrowing what a caller may pass breaks callers; widening what the program writes breaks
// readers.
type direction int

const (
	in direction = iota
	out
)

// enumMeta carries what an input entry adds to its schema's enum: per-value details, and
// whether the enum follows an output path. It applies to the input's own value and, for a
// list, its items.
type enumMeta struct {
	note             string               // added to every enum value finding
	skipEnumPresence bool                 // values_from was added or removed, which adds or removes the enum
	oldEnum, newEnum map[string]enumValue // the entries' enum_values
	typed            bool                 // both entries state a rotini type, which is compared instead of the JSON type
	skipFormat       bool                 // the format follows the entries' layouts or relative values, compared instead
}

// walk compares two schemas in a direction. A `$ref` naming the same definition on both sides
// isn't entered: definitions are compared once, on their own. A `$ref` whose target changed is
// followed and compared structurally.
func (d *differ) walk(st stability, dir direction, where string, o, n any, meta *enumMeta) {
	d.walkSchema(st, dir, where, o, n, meta, map[[2]string]bool{})
}

func (d *differ) walkSchema(st stability, dir direction, where string, o, n any, meta *enumMeta, seen map[[2]string]bool) {
	om, nm := asMap(o), asMap(n)
	if om == nil || nm == nil {
		return
	}
	oref, nref := refName(om), refName(nm)
	if oref != "" || nref != "" {
		if oref == nref && d.old.Definitions[oref] != nil && d.new.Definitions[nref] != nil {
			return
		}
		key := [2]string{oref, nref}
		if seen[key] {
			return
		}
		seen[key] = true
		if oref != "" {
			om = asMap(d.old.Definitions[oref])
		}
		if nref != "" {
			nm = asMap(d.new.Definitions[nref])
		}
		if om == nil || nm == nil {
			return
		}
	}
	if meta == nil || !meta.typed {
		d.jsonType(st, dir, where, om, nm)
	}
	d.enum(st, dir, where, om, nm, meta)
	if dir == in {
		d.bounds(st, where, om, nm)
		d.pattern(st, where, om, nm)
		d.defaults(st, where, om, nm)
		if meta == nil || !meta.skipFormat {
			d.format(st, where, om, nm)
		}
	}
	if oi, ni := om["items"], nm["items"]; oi != nil && ni != nil {
		d.walkSchema(st, dir, where+"[]", oi, ni, meta, seen)
	}
	d.properties(st, dir, where, om, nm, seen)
	d.composition(st, dir, where, om, nm, seen)
}

// definitions compares each definition both contracts hold, once per direction it is used in:
// input when a caller's value reaches it, output when the program's does.
func (d *differ) definitions() {
	oldUse, newUse := d.old.definitionUse(), d.new.definitionUse()
	for _, name := range slices.Sorted(maps.Keys(d.old.Definitions)) {
		nd, ok := d.new.Definitions[name]
		if !ok {
			continue // a reference to it changed too, and is followed where it is made
		}
		od := d.old.Definitions[name]
		users := mergeUse(oldUse[name], newUse[name])
		note := ""
		if len(users.commands) > 0 {
			note = "used by " + strings.Join(users.commands, ", ")
		}
		start := len(d.findings)
		for _, dir := range users.directions() {
			d.walk("", dir, "definitions."+name, od, nd, nil)
		}
		for i := start; i < len(d.findings); i++ {
			d.findings[i].Note = joinNote(d.findings[i].Note, note)
		}
	}
}

// use records how a definition is used: by which commands, and in which directions.
type use struct {
	in, out  bool
	commands []string
}

func (u use) directions() []direction {
	var dirs []direction
	if u.in {
		dirs = append(dirs, in)
	}
	if u.out {
		dirs = append(dirs, out)
	}
	return dirs
}

func mergeUse(a, b use) use {
	cmds := slices.Concat(a.commands, b.commands)
	slices.Sort(cmds)
	return use{in: a.in || b.in, out: a.out || b.out, commands: slices.Compact(cmds)}
}

// definitionUse works out, for each definition, which commands reach it and in which
// direction, through other definitions too.
func (doc *document) definitionUse() map[string]use {
	uses := map[string]use{}
	mark := func(dir direction, cmd string, schema any) {
		queue := refsIn(schema)
		seen := map[string]bool{}
		for len(queue) > 0 {
			name := queue[0]
			queue = queue[1:]
			if seen[name] {
				continue
			}
			seen[name] = true
			u := uses[name]
			if dir == in {
				u.in = true
			} else {
				u.out = true
			}
			if !slices.Contains(u.commands, cmd) {
				u.commands = append(u.commands, cmd)
			}
			uses[name] = u
			queue = append(queue, refsIn(doc.Definitions[name])...)
		}
	}
	for _, c := range doc.Commands {
		for _, list := range [][]input{c.Arguments, c.Flags, c.Env, c.Config} {
			for _, e := range list {
				if !e.Inherited {
					mark(in, c.Name, e.Schema)
				}
			}
		}
		if c.Stdin != nil {
			mark(in, c.Name, stripLocal(c.Stdin.Schema))
		}
		mark(out, c.Name, stripLocal(c.Output))
		for _, e := range c.ExitStatus {
			mark(out, c.Name, stripLocal(e.Output))
		}
	}
	return uses
}

// stripLocal drops a self-contained schema's own copy of the definitions it reaches, so only
// its references are followed.
func stripLocal(v any) any {
	m := asMap(v)
	if m == nil {
		return v
	}
	c := maps.Clone(m)
	delete(c, "definitions")
	return c
}

// refsIn lists the definition names a schema references, skipping copied definitions.
func refsIn(v any) []string {
	var out []string
	switch t := v.(type) {
	case map[string]any:
		if name := refName(t); name != "" {
			out = append(out, name)
		}
		for _, k := range slices.Sorted(maps.Keys(t)) {
			if k != "definitions" {
				out = append(out, refsIn(t[k])...)
			}
		}
	case []any:
		for _, e := range t {
			out = append(out, refsIn(e)...)
		}
	}
	return out
}

func refName(m map[string]any) string {
	ref, _ := m["$ref"].(string)
	name, _ := strings.CutPrefix(ref, "#/definitions/")
	return name
}

func asMap(v any) map[string]any { m, _ := v.(map[string]any); return m }

// jsonTypes is a schema's `type` as a set; nil when it states none.
func jsonTypes(m map[string]any) []string {
	switch t := m["type"].(type) {
	case string:
		return []string{t}
	case []any:
		var out []string
		for _, e := range t {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		slices.Sort(out)
		return out
	}
	return nil
}

// covers reports whether type set a accepts every value type set b does.
func covers(a, b []string) bool {
	for _, t := range b {
		if !slices.Contains(a, t) && (t != "integer" || !slices.Contains(a, "number")) {
			return false
		}
	}
	return true
}

func (d *differ) jsonType(st stability, dir direction, where string, o, n map[string]any) {
	ot, nt := jsonTypes(o), jsonTypes(n)
	if ot == nil || nt == nil || slices.Equal(ot, nt) {
		return
	}
	msg := "type " + strings.Join(ot, "|") + " → " + strings.Join(nt, "|")
	if dir == in {
		if covers(nt, ot) {
			d.add(st, Safe, RuleInputTypeWidened, where, msg, "")
		} else {
			d.add(st, Breaking, RuleInputTypeChanged, where, msg, "")
		}
		return
	}
	if !covers(ot, nt) {
		d.add(st, Breaking, RuleOutputTypeChanged, where, msg, "")
	}
}

// enumOf is a schema's allowed values: its enum, or its const as a one-value enum.
func enumOf(m map[string]any) ([]any, bool) {
	if e, ok := m["enum"].([]any); ok {
		return e, true
	}
	if c, ok := m["const"]; ok {
		return []any{c}, true
	}
	return nil, false
}

func containsValue(list []any, v any) bool {
	return slices.ContainsFunc(list, func(e any) bool { return deepEqual(e, v) || jsonText(e) == jsonText(v) })
}

// enum compares allowed values. For a caller's value, removing a value or adding an enum
// breaks; for the program's, adding a value may break a reader that switches on it.
func (d *differ) enum(st stability, dir direction, where string, o, n map[string]any, meta *enumMeta) {
	oe, oHas := enumOf(o)
	ne, nHas := enumOf(n)
	note := ""
	skipPresence := false
	if meta != nil {
		note, skipPresence = meta.note, meta.skipEnumPresence
	}
	switch {
	case !oHas && !nHas:
		return
	case !oHas:
		if !skipPresence {
			if dir == in {
				d.add(st, Breaking, RuleInputEnumAdded, where, "now limited to "+valueList(ne), note)
			} else {
				d.add(st, Safe, RuleOutputEnumAdded, where, "now limited to "+valueList(ne), note)
			}
		}
		return
	case !nHas:
		if !skipPresence {
			if dir == in {
				d.add(st, Safe, RuleInputEnumRemoved, where, "no longer limited to a set of values", note)
			} else {
				d.add(st, PossiblyBreaking, RuleOutputEnumRemoved, where, "no longer limited to a set of values", note)
			}
		}
		return
	}
	if dir == in && meta != nil {
		// A hidden or deprecated value is left out of the schema's enum but still accepted.
		oe, ne = withDetailed(oe, meta.oldEnum), withDetailed(ne, meta.newEnum)
	}
	for _, v := range oe {
		if containsValue(ne, v) {
			continue
		}
		vw := where + " value " + jsonText(v)
		if dir == out {
			d.add(st, Safe, RuleOutputEnumValueRemoved, vw, "value "+jsonText(v)+" no longer written", note)
			continue
		}
		var ev enumValue
		if meta != nil {
			ev = meta.oldEnum[jsonText(v)]
		}
		if ev.ReplacedBy != "" && containsValue(ne, ev.ReplacedBy) {
			d.add(st, Expected, RuleEnumValueReplaced, vw, "value "+jsonText(v)+" removed; replaced by "+ev.ReplacedBy, note)
			continue
		}
		d.removalWith(st, RuleEnumValueNoDelete, vw, "value "+jsonText(v)+" no longer accepted", ev.RemovedIn, ev.Hidden, note)
	}
	for _, v := range ne {
		if containsValue(oe, v) {
			continue
		}
		vw := where + " value " + jsonText(v)
		if dir == out {
			d.add(st, PossiblyBreaking, RuleOutputEnumValueAdded, vw, "value "+jsonText(v)+" may be written", joinNote(note, "a reader that switches on the value may not expect it"))
		} else {
			d.add(st, Safe, RuleEnumValueAdded, vw, "value "+jsonText(v)+" accepted", note)
		}
	}
	if dir == in && meta != nil {
		d.enumValues(st, where, oe, ne, meta)
	}
}

// enumValues compares what an input's enum values declare beyond their spelling: aliases,
// hidden and deprecation.
func (d *differ) enumValues(st stability, where string, oe, ne []any, meta *enumMeta) {
	for _, v := range oe {
		if !containsValue(ne, v) {
			continue
		}
		key := jsonText(v)
		ov, nv := meta.oldEnum[key], meta.newEnum[key]
		vw := where + " value " + key
		now := slices.Concat(nv.Aliases, nv.DeprecatedAliases)
		for _, a := range slices.Concat(ov.Aliases, ov.DeprecatedAliases) {
			if slices.Contains(now, a) {
				continue
			}
			aw := where + " value " + key + " alias " + a
			if slices.Contains(ov.DeprecatedAliases, a) {
				d.removalWith(st, RuleEnumAliasNoDelete, aw, "deprecated alias "+a+" no longer accepted", ov.RemovedIn, false, "was deprecated")
			} else {
				d.add(st, Breaking, RuleEnumAliasNoDelete, aw, "alias "+a+" no longer accepted", "")
			}
		}
		for _, a := range missing(now, slices.Concat(ov.Aliases, ov.DeprecatedAliases)) {
			d.add(st, Safe, RuleEnumAliasAdded, where+" value "+key+" alias "+a, "alias "+a+" accepted", "")
		}
		d.hidden(st, RuleEnumValueHidden, RuleEnumValueUnhidden, vw, ov.Hidden, nv.Hidden)
		d.lifecycle(st, vw, lifecycleFacts{deprecated: ov.Deprecated, since: ov.DeprecatedSince, removedIn: ov.RemovedIn, replacedBy: ov.ReplacedBy},
			lifecycleFacts{deprecated: nv.Deprecated, since: nv.DeprecatedSince, removedIn: nv.RemovedIn, replacedBy: nv.ReplacedBy})
	}
}

func valueList(vs []any) string {
	parts := make([]string, 0, len(vs))
	for _, v := range vs {
		parts = append(parts, jsonText(v))
	}
	return strings.Join(parts, ", ")
}

// bound is a numeric keyword and the direction that narrows it: +1 when a larger value
// accepts less (minimum), -1 when a smaller one does (maximum).
type bound struct {
	key string
	dir float64
}

var bounds = []bound{
	{"minimum", 1}, {"exclusiveMinimum", 1}, {"minLength", 1}, {"minItems", 1}, {"minProperties", 1},
	{"maximum", -1}, {"exclusiveMaximum", -1}, {"maxLength", -1}, {"maxItems", -1}, {"maxProperties", -1},
}

// bounds compares a caller's value's bounds, lengths, counts, multipleOf and uniqueItems. One
// finding per kind of change lists every keyword it covers.
func (d *differ) bounds(st stability, where string, o, n map[string]any) {
	var added, narrowed, widened, removed []string
	for _, b := range bounds {
		ov, oHas := o[b.key].(float64)
		nv, nHas := n[b.key].(float64)
		switch {
		case !oHas && nHas:
			added = append(added, fmt.Sprintf("%s %v", b.key, nv))
		case oHas && !nHas:
			removed = append(removed, fmt.Sprintf("%s %v", b.key, ov))
		case oHas && ov != nv && (nv-ov)*b.dir > 0:
			narrowed = append(narrowed, fmt.Sprintf("%s %v → %v", b.key, ov, nv))
		case oHas && ov != nv:
			widened = append(widened, fmt.Sprintf("%s %v → %v", b.key, ov, nv))
		}
	}
	om, oHas := o["multipleOf"].(float64)
	nmv, nHas := n["multipleOf"].(float64)
	switch {
	case !oHas && nHas:
		added = append(added, fmt.Sprintf("multipleOf %v", nmv))
	case oHas && !nHas:
		removed = append(removed, fmt.Sprintf("multipleOf %v", om))
	case oHas && om != nmv:
		narrowed = append(narrowed, fmt.Sprintf("multipleOf %v → %v", om, nmv))
	}
	ou, _ := o["uniqueItems"].(bool)
	nu, _ := n["uniqueItems"].(bool)
	switch {
	case !ou && nu:
		added = append(added, "uniqueItems")
	case ou && !nu:
		removed = append(removed, "uniqueItems")
	}
	if len(added) > 0 {
		d.add(st, Breaking, RuleInputBoundAdded, where, "now limited: "+strings.Join(added, ", "), "")
	}
	if len(narrowed) > 0 {
		d.add(st, Breaking, RuleInputBoundNarrowed, where, "narrowed: "+strings.Join(narrowed, ", "), "")
	}
	if len(widened) > 0 {
		d.add(st, Safe, RuleInputBoundWidened, where, "widened: "+strings.Join(widened, ", "), "")
	}
	if len(removed) > 0 {
		d.add(st, Safe, RuleInputBoundRemoved, where, "no longer limited: "+strings.Join(removed, ", "), "")
	}
}

func (d *differ) pattern(st stability, where string, o, n map[string]any) {
	op, _ := o["pattern"].(string)
	np, _ := n["pattern"].(string)
	switch {
	case op == np:
	case op == "":
		d.add(st, Breaking, RuleInputPatternAdded, where, "must now match "+np, "")
	case np == "":
		d.add(st, Safe, RuleInputPatternRemoved, where, "no longer has to match "+op, "")
	default:
		d.add(st, PossiblyBreaking, RuleInputPatternChanged, where, "pattern "+op+" → "+np, "")
	}
}

func (d *differ) defaults(st stability, where string, o, n map[string]any) {
	od, oHas := o["default"]
	nd, nHas := n["default"]
	switch {
	case oHas && !nHas:
		d.add(st, PossiblyBreaking, RuleInputDefaultRemoved, where, "default "+jsonValue(od)+" removed", "")
	case !oHas && nHas:
		d.add(st, Safe, RuleInputDefaultAdded, where, "default "+jsonValue(nd)+" added", "")
	case oHas && !deepEqual(od, nd):
		d.add(st, PossiblyBreaking, RuleInputDefaultChanged, where, "default "+jsonValue(od)+" → "+jsonValue(nd), "the same command line behaves differently")
	}
}

func (d *differ) format(st stability, where string, o, n map[string]any) {
	of, _ := o["format"].(string)
	nf, _ := n["format"].(string)
	switch {
	case of == nf:
	case nf == "":
		d.add(st, Safe, RuleInputFormatRemoved, where, "format "+of+" no longer claimed", "")
	default:
		d.add(st, PossiblyBreaking, RuleInputFormatChanged, where, fmt.Sprintf("format %q → %q", of, nf), "")
	}
}

// properties compares an object's properties: for a caller's value, a new required property or
// a removed one that unknown keys can't stand in for breaks; for the program's, a removed or
// no-longer-required property does.
func (d *differ) properties(st stability, dir direction, where string, o, n map[string]any, seen map[[2]string]bool) {
	op, np := asMap(o["properties"]), asMap(n["properties"])
	oreq, nreq := stringList(o["required"]), stringList(n["required"])
	closed := n["additionalProperties"] == false
	for _, k := range slices.Sorted(maps.Keys(op)) {
		w := where + "." + k
		nv, ok := np[k]
		switch {
		case ok:
			d.walkSchema(st, dir, w, op[k], nv, nil, seen)
		case dir == out:
			d.add(st, Breaking, RuleOutputPropertyNoDelete, w, "property no longer written", "")
		case closed:
			d.add(st, Breaking, RuleInputPropertyNoDelete, w, "property removed, and unknown properties are refused", "")
		}
	}
	for _, k := range slices.Sorted(maps.Keys(np)) {
		if _, ok := op[k]; ok {
			continue
		}
		w := where + "." + k
		switch {
		case dir == out:
			d.add(st, Safe, RuleOutputPropertyAdded, w, "property added", "")
		case !slices.Contains(nreq, k):
			d.add(st, Safe, RuleInputPropertyAdded, w, "optional property added", "")
		}
	}
	for _, k := range nreq {
		if !slices.Contains(oreq, k) && dir == in {
			d.add(st, Breaking, RuleInputPropertyRequiredAdded, where+"."+k, "property now required", "")
		}
	}
	for _, k := range oreq {
		if !slices.Contains(nreq, k) && dir == out {
			if _, still := np[k]; still {
				d.add(st, Breaking, RuleOutputPropertyRequiredRemove, where+"."+k, "property no longer always written", "")
			}
		}
	}
	if dir == in && o["additionalProperties"] != false && closed && op != nil {
		d.add(st, Breaking, RuleInputClosed, where, "unknown properties are now refused", "")
	}
	if oa, na := asMap(o["additionalProperties"]), asMap(n["additionalProperties"]); oa != nil && na != nil {
		d.walkSchema(st, dir, where+".*", oa, na, nil, seen)
	}
}

// composition compares anyOf, oneOf and allOf branch by branch. A changed number of branches
// can't be compared and is possibly breaking.
func (d *differ) composition(st stability, dir direction, where string, o, n map[string]any, seen map[[2]string]bool) {
	for _, key := range []string{"allOf", "anyOf", "oneOf"} {
		ob, _ := o[key].([]any)
		nb, _ := n[key].([]any)
		switch {
		case len(ob) == 0 && len(nb) == 0:
		case len(ob) != len(nb):
			d.add(st, PossiblyBreaking, RuleSchemaCompositionChanged, where, fmt.Sprintf("%s has %d branches, was %d", key, len(nb), len(ob)), "")
		default:
			for i := range ob {
				d.walkSchema(st, dir, fmt.Sprintf("%s.%s[%d]", where, key, i), ob[i], nb[i], nil, seen)
			}
		}
	}
}

func stringList(v any) []string {
	var out []string
	for _, e := range asSlice(v) {
		if s, ok := e.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func asSlice(v any) []any { s, _ := v.([]any); return s }

// isEmptySchema reports whether a schema constrains nothing: exactly {}.
func isEmptySchema(v any) bool {
	m, ok := v.(map[string]any)
	return ok && len(m) == 0
}

func hasType(v any) bool { return jsonTypes(asMap(v)) != nil }

// hasEnum reports whether a schema, or a list's items, lists allowed values.
func hasEnum(v any, doc *document) bool {
	m := asMap(v)
	if name := refName(m); name != "" {
		m = asMap(doc.Definitions[name])
	}
	if m == nil {
		return false
	}
	if _, ok := enumOf(m); ok {
		return true
	}
	return hasEnum(m["items"], doc)
}

func deepEqual(a, b any) bool { return reflect.DeepEqual(a, b) }

// jsonValue renders a value as JSON, for messages.
func jsonValue(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}

// withDetailed adds to an enum the values its entry details that the list leaves out.
func withDetailed(list []any, detailed map[string]enumValue) []any {
	out := slices.Clone(list)
	for _, k := range slices.Sorted(maps.Keys(detailed)) {
		if !slices.ContainsFunc(out, func(v any) bool { return jsonText(v) == k }) {
			out = append(out, k)
		}
	}
	return out
}
