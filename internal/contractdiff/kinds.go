package contractdiff

import (
	"fmt"
	"maps"
	"slices"
)

// A stream whose items come in several kinds declares its output as `oneOf` object schemas,
// each tagged by the same required string property holding a `const`. Kinds are matched by
// that value, not by position: a kind added is safe, a kind removed (or renamed, which removes
// its old value) breaks readers, and a kind kept is compared as any output.

// outputKinds compares two outputs when either lists kinds, reporting true when it did.
func (d *differ) outputKinds(st stability, where string, o, n any) bool {
	otag, okinds := streamKinds(d.old, o)
	ntag, nkinds := streamKinds(d.new, n)
	switch {
	case okinds == nil && nkinds == nil:
		return false
	case okinds == nil:
		d.add(st, Breaking, RuleOutputTypeChanged, where, "items now come in several kinds, told apart by "+ntag, "")
		return true
	case nkinds == nil:
		d.add(st, Breaking, RuleOutputTypeChanged, where, "items no longer come in several kinds", "")
		return true
	case otag != ntag:
		d.add(st, Breaking, RuleOutputKindTagChanged, where, fmt.Sprintf("an item's kind is now named by %s, was %s", ntag, otag), "")
		return true
	}
	for _, v := range slices.Sorted(maps.Keys(okinds)) {
		w := fmt.Sprintf("%s[%s=%s]", where, otag, v)
		if nk, ok := nkinds[v]; ok {
			d.walk(st, out, w, okinds[v], nk, nil)
		} else {
			d.add(st, Breaking, RuleOutputKindNoDelete, w, "kind no longer written", "")
		}
	}
	for _, v := range slices.Sorted(maps.Keys(nkinds)) {
		if _, ok := okinds[v]; !ok {
			d.add(st, Safe, RuleOutputKindAdded, fmt.Sprintf("%s[%s=%s]", where, ntag, v), "kind added", "")
		}
	}
	return true
}

// streamKinds returns the tag property and each kind's schema, by tag value, of an output
// that lists kinds under oneOf; nil when it doesn't, or when no one property tags them all.
func streamKinds(doc *document, schema any) (string, map[string]any) {
	members := asSlice(asMap(schema)["oneOf"])
	if len(members) == 0 {
		return "", nil
	}
	resolved := make([]map[string]any, len(members))
	var common map[string]bool
	for i, m := range members {
		r := asMap(m)
		if name := refName(r); name != "" {
			r = asMap(doc.Definitions[name])
		}
		resolved[i] = r
		tags := map[string]bool{}
		required := stringList(r["required"])
		for p, ps := range asMap(r["properties"]) {
			if _, ok := asMap(ps)["const"].(string); ok && slices.Contains(required, p) {
				tags[p] = true
			}
		}
		if common == nil {
			common = tags
			continue
		}
		for p := range common {
			if !tags[p] {
				delete(common, p)
			}
		}
	}
	if len(common) != 1 {
		return "", nil
	}
	var tag string
	for p := range common {
		tag = p
	}
	kinds := map[string]any{}
	for i, r := range resolved {
		value, _ := asMap(asMap(r["properties"])[tag])["const"].(string)
		kinds[value] = members[i]
	}
	return tag, kinds
}
