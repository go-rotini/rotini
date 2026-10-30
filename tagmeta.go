package rotini

import (
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// Env and config fields carry their enum and layout metadata in struct tags. A list tag holds
// items separated by ",", a map tag key=value pairs separated by ","; a "\" escapes a "\", ","
// or "=" inside an item, key or value:
//
//	enumlist:"<list>"     the main values, in declared order
//	enumaliases:"<map>"   each alias and the value it stands for
//	enumhide:"<list>"     the hidden and deprecated values, which messages leave out
//	layoutlist:"<list>"   every layout, when there are several
//
// A tag with any other escape reads as absent. The JSON forms enum:"<array>",
// enumalias:"<object>", enumunlisted:"<array>" and layouts:"<array>" are also accepted, and are
// read when none of the list tags is present.

// tagTimeSpec reads an env or config field's time tags: layout:"<first>", layoutlist (or the
// JSON layouts) when there are several, and relative:"past"; and an integer field's
// base:"prefixed". A malformed list reads as the first layout alone.
func tagTimeSpec(tag reflect.StructTag) timeSpec {
	ts := timeSpec{layout: tag.Get("layout"), relative: tag.Get("relative"), date: tag.Get("date") == "true", base: tag.Get("base")}
	if v, ok := tag.Lookup("layoutlist"); ok {
		if list, ok := splitTagList(v); ok {
			ts.layouts = list
		}
	} else if v := tag.Get("layouts"); v != "" {
		if list, ok := jsonStrings(v); ok {
			ts.layouts = list
		}
	}
	return ts
}

// channelEnum reads an env or config field's enum tags: the main values, ignorecase:"true",
// the aliases and the hidden values; and base:"prefixed", which matches integers by value.
func channelEnum(tag reflect.StructTag) enumSet {
	var e enumSet
	e.ignoreCase = tag.Get("ignorecase") == "true"
	e.prefixed = tag.Get("base") == basePrefixed
	var aliases map[string]string
	var unlisted []string
	_, hasList := tag.Lookup("enumlist")
	_, hasAliases := tag.Lookup("enumaliases")
	_, hasHide := tag.Lookup("enumhide")
	if hasList || hasAliases || hasHide {
		e.values = tagList(tag, "enumlist")
		aliases = tagMap(tag, "enumaliases")
		unlisted = tagList(tag, "enumhide")
	} else {
		// A malformed JSON tag keeps what decoding it as JSON would have kept.
		if v := tag.Get("enum"); v != "" {
			e.values, _ = jsonStrings(v)
		}
		if v := tag.Get("enumalias"); v != "" {
			aliases, _ = jsonStringMap(v)
		}
		if v := tag.Get("enumunlisted"); v != "" {
			unlisted, _ = jsonStrings(v)
		}
	}
	if len(aliases) == 0 && len(unlisted) == 0 {
		return e
	}
	for _, m := range e.values {
		d := EnumValue{Value: m, Hidden: slices.Contains(unlisted, m)}
		for a, to := range aliases {
			if to == m {
				d.Aliases = append(d.Aliases, a)
			}
		}
		sort.Strings(d.Aliases)
		e.described = append(e.described, d)
	}
	return e
}

// checkChannelEnum is the env and config counterpart of the argv enum check in [validate], so an
// enum advertised in help is enforced whichever channel supplied the value.
func checkChannelEnum(channel, label string, enum enumSet, vals []string, secret bool) error {
	if !enum.declared() {
		return nil
	}
	for _, v := range vals {
		if !enum.has(v) {
			listed := enum.listed()
			e := usageBind(channel, label, fmt.Sprintf("invalid value %q for %s (one of: %s)",
				redactValue(v, secret), label, strings.Join(listed, ", ")), nil)
			e.Token, e.Candidates = redactValue(v, secret), listed
			return e
		}
	}
	return nil
}

// boundStrings is the values an enum is checked against: a bound []string field's elements,
// since an env list arrives as one "a,b" string that only binding splits; else the channel's
// own rendering.
func boundStrings(f reflect.Value, fallback []string) []string {
	if f.Kind() == reflect.Slice && f.Type().Elem().Kind() == reflect.String {
		out := make([]string, f.Len())
		for i := range f.Len() {
			out[i] = f.Index(i).String()
		}
		return out
	}
	return fallback
}

// canonicalizeField rewrites a bound string, or each element of a bound []string, to the
// main value it matched: through an alias, or case-insensitively.
func canonicalizeField(f reflect.Value, enum enumSet) {
	switch {
	case !f.CanSet():
	case f.Kind() == reflect.String:
		f.SetString(enum.canonical([]string{f.String()})[0])
	case f.Kind() == reflect.Slice && f.Type().Elem().Kind() == reflect.String:
		for i := range f.Len() {
			e := f.Index(i)
			e.SetString(enum.canonical([]string{e.String()})[0])
		}
	}
}

// channelConstraints reads the validation struct-tags codegen emits on a channel field into a
// [Constraints]. A pointer bound is set whenever its tag is present, so min:"0" enforces >= 0.
func channelConstraints(tag reflect.StructTag) (Constraints, bool) {
	var c Constraints
	has := false
	// An absent tag reads as "", which never parses: skip it rather than build a parse error.
	floatTag := func(name string, dst **float64) {
		s := tag.Get(name)
		if s == "" {
			return
		}
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			*dst, has = new(f), true
		}
	}
	intTag := func(name string, dst *int) {
		s := tag.Get(name)
		if s == "" {
			return
		}
		if n, err := strconv.Atoi(s); err == nil {
			*dst, has = n, true
		}
	}
	intPtrTag := func(name string, dst **int) {
		s := tag.Get(name)
		if s == "" {
			return
		}
		if n, err := strconv.Atoi(s); err == nil {
			*dst, has = new(n), true
		}
	}
	floatTag("min", &c.Minimum)
	floatTag("max", &c.Maximum)
	floatTag("xmin", &c.ExclusiveMinimum)
	floatTag("xmax", &c.ExclusiveMaximum)
	floatTag("multipleof", &c.MultipleOf)
	intTag("minlen", &c.MinLength)
	intPtrTag("maxlen", &c.MaxLength)
	intTag("minitems", &c.MinItems)
	intPtrTag("maxitems", &c.MaxItems)
	if v := tag.Get("pattern"); v != "" {
		c.Pattern, has = v, true
	}
	c.PatternMessage = tag.Get("patternmsg")
	if tag.Get("unique") == "true" {
		c.UniqueItems, has = true, true
	}
	return c, has
}
