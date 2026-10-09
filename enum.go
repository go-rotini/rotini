package rotini

import (
	"fmt"
	"slices"
	"strings"
)

// enumSet is an input's enum as matching reads it: its main values, what they declare beyond
// their spelling (aliases, hidden, deprecated), and whether case is ignored.
type enumSet struct {
	values     []string
	described  []EnumValue
	ignoreCase bool
}

func flagEnum(fd FlagDef) enumSet {
	return enumSet{values: fd.Enum, described: fd.EnumValues, ignoreCase: fd.IgnoreCase}
}

func argEnum(ad ArgDef) enumSet {
	return enumSet{values: ad.Enum, described: ad.EnumValues, ignoreCase: ad.IgnoreCase}
}

// declared reports whether the input has an enum at all.
func (e enumSet) declared() bool { return len(e.values) > 0 }

// match reports whether v is accepted and the main value it binds as: a main value, or one of
// a value's aliases, compared without regard to case when the enum ignores case. A main value
// wins over an alias that differs from it only in case.
func (e enumSet) match(v string) (string, bool) {
	if slices.Contains(e.values, v) {
		return v, true
	}
	for _, d := range e.described {
		if slices.Contains(d.Aliases, v) {
			return d.Value, true
		}
	}
	if !e.ignoreCase {
		return "", false
	}
	for _, m := range e.values {
		if strings.EqualFold(m, v) {
			return m, true
		}
	}
	for _, d := range e.described {
		for _, a := range d.Aliases {
			if strings.EqualFold(a, v) {
				return d.Value, true
			}
		}
	}
	return "", false
}

// has reports whether v is accepted.
func (e enumSet) has(v string) bool {
	_, ok := e.match(v)
	return ok
}

// rewrites reports whether matching can change a value's spelling: a case-insensitive enum, or
// one with aliases.
func (e enumSet) rewrites() bool {
	if e.ignoreCase {
		return true
	}
	for _, d := range e.described {
		if len(d.Aliases) > 0 {
			return true
		}
	}
	return false
}

// canonical rewrites each accepted value to the main value it binds as, leaving any other value
// for validation to reject. vals is returned as is when nothing could change.
func (e enumSet) canonical(vals []string) []string {
	if !e.declared() || !e.rewrites() {
		return vals
	}
	out := make([]string, len(vals))
	for i, v := range vals {
		out[i] = v
		if m, ok := e.match(v); ok {
			out[i] = m
		}
	}
	return out
}

// listed is the main values an error or completion offers: every one neither hidden nor
// deprecated, in declared order.
func (e enumSet) listed() []string {
	if !e.quiets() {
		return e.values
	}
	out := make([]string, 0, len(e.values))
	for _, v := range e.values {
		if d, ok := e.describe(v); !ok || (!d.Hidden && d.Deprecated == "") {
			out = append(out, v)
		}
	}
	return out
}

// quiets reports whether some value is hidden or deprecated.
func (e enumSet) quiets() bool {
	for _, d := range e.described {
		if d.Hidden || d.Deprecated != "" {
			return true
		}
	}
	return false
}

// describe finds what main value v declares beyond its spelling.
func (e enumSet) describe(v string) (EnumValue, bool) {
	for _, d := range e.described {
		if d.Value == v {
			return d, true
		}
	}
	return EnumValue{}, false
}

// deprecatedUse reports whether the value v, as typed, is a deprecated use of the enum: a
// deprecated main value or a deprecated alias, or any alias of a deprecated value. It returns
// the value's description.
func (e enumSet) deprecatedUse(v string) (EnumValue, bool) {
	m, ok := e.match(v)
	if !ok {
		return EnumValue{}, false
	}
	d, ok := e.describe(m)
	if !ok {
		return EnumValue{}, false
	}
	if d.Deprecated != "" {
		return d, true
	}
	for _, a := range d.DeprecatedAliases {
		if a == v || (e.ignoreCase && strings.EqualFold(a, v)) {
			return d, true
		}
	}
	return EnumValue{}, false
}

// violation is the error for a value outside the enum. label names the input as typed; flag is
// its flag label, "" for an argument.
func (e enumSet) violation(label, flag, v string, secret bool) *ParseError {
	listed := e.listed()
	return &ParseError{
		Kind:       ParseKindEnumViolation,
		Msg:        fmt.Sprintf("invalid value %q for %s (one of: %s)", redactValue(v, secret), label, strings.Join(listed, ", ")),
		Flag:       flag,
		Token:      redactValue(v, secret),
		Candidates: listed,
	}
}
