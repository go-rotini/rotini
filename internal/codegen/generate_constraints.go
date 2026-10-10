package codegen

import (
	"fmt"
	"strings"
	"time"
)

// Declared constraints as the pages show them: a compact note for a help row, and full
// sentences for man and markdown.

// constraintText describes the constraints declared on an input's schema. compact is the help
// row's note without parentheses (`1..65535`, `length 3..20, repeatable`), "" when none; rules
// are the same constraints as sentences for man and markdown, plus the pattern and separator,
// which help leaves out. channel is the input's channel ("flag", "argument", "env", "config");
// only a flag repeats, so only a flag is marked repeatable, or `once` when it may not repeat. A
// list whose values must differ (uniqueItems) is marked `unique`.
func constraintText(s *InputSchema, channel string) (compact string, rules []string) {
	if s == nil {
		return "", nil
	}
	var parts []string
	add := func(c, rule string) {
		if c != "" {
			parts = append(parts, c)
		}
		if rule != "" {
			rules = append(rules, rule)
		}
	}
	t := getSchemaType(s)
	list := strings.HasPrefix(t, "[]")
	repeated := list || strings.HasPrefix(t, "map[")
	elem := strings.TrimPrefix(t, "[]")

	if repeated {
		add(countConstraint(s.MinItems, s.MaxItems))
	}
	// A list's value constraints apply to each value. Those declared on its items are already
	// on the list itself: normalization hoists them.
	each := ""
	if list {
		each = "each value "
	}
	for _, c := range valueConstraintText(&s.BaseSchema, elem) {
		add(c[0], each+c[1])
	}
	switch {
	case repeated && s.Separator == "nul":
		add("", "NUL-separated in files")
	case repeated && s.Separator != "" && channel == "env":
		add("", fmt.Sprintf("several values in the variable, separated by %q", s.Separator))
	case repeated && s.Separator != "":
		add("", fmt.Sprintf("several values per occurrence, separated by %q", s.Separator))
	}
	if channel == "flag" {
		switch {
		case s.Repeatable != nil && !*s.Repeatable:
			add("once", "given at most once")
		case s.Type == "count":
			add("repeatable", "repeat to count")
		case repeated:
			add("repeatable", "repeatable")
		}
	}
	if list && s.UniqueItems {
		add("unique", "each value at most once")
	}
	return strings.Join(parts, ", "), rules
}

// valueConstraintText returns the compact and sentence forms of each value constraint declared on
// b: the numeric range, multipleOf, the length range and the pattern (sentence only).
func valueConstraintText(b *BaseSchema, elem string) [][2]string {
	var out [][2]string
	if c, rule := rangeConstraint(b, elem); c != "" {
		out = append(out, [2]string{c, rule})
	}
	if b.MultipleOf != nil {
		m := formatBoundValue(b.MultipleOf, elem)
		out = append(out, [2]string{"multiple of " + m, "a multiple of " + m})
	}
	maxLen := b.MaxLength
	switch {
	case b.MinLength > 0 && maxLen != nil:
		out = append(out, [2]string{fmt.Sprintf("length %d..%d", b.MinLength, *maxLen), fmt.Sprintf("%d to %d characters", b.MinLength, *maxLen)})
	case b.MinLength > 0:
		out = append(out, [2]string{fmt.Sprintf("length >= %d", b.MinLength), fmt.Sprintf("at least %d %s", b.MinLength, plural(b.MinLength, "character"))})
	case maxLen != nil:
		out = append(out, [2]string{fmt.Sprintf("length <= %d", *maxLen), fmt.Sprintf("at most %d %s", *maxLen, plural(*maxLen, "character"))})
	}
	switch {
	case b.PatternMessage != "":
		out = append(out, [2]string{"", b.PatternMessage})
	case b.Pattern != "":
		out = append(out, [2]string{"", "matches " + codeSpan(b.Pattern)})
	}
	return out
}

// rangeConstraint renders a schema's numeric bounds: `1..65535` / "between 1 and 65535" when
// both inclusive bounds are set, else each side on its own (`> 0, <= 10`).
func rangeConstraint(b *BaseSchema, elem string) (compact, rule string) {
	if b.Minimum != nil && b.Maximum != nil && b.ExclusiveMinimum == nil && b.ExclusiveMaximum == nil {
		lo, hi := formatBoundValue(b.Minimum, elem), formatBoundValue(b.Maximum, elem)
		return lo + ".." + hi, "between " + lo + " and " + hi
	}
	var parts, words []string
	side := func(v any, op, word string) {
		if v != nil {
			s := formatBoundValue(v, elem)
			parts = append(parts, op+" "+s)
			words = append(words, word+" "+s)
		}
	}
	side(b.Minimum, ">=", "at least")
	side(b.ExclusiveMinimum, ">", "greater than")
	side(b.Maximum, "<=", "at most")
	side(b.ExclusiveMaximum, "<", "less than")
	return strings.Join(parts, ", "), strings.Join(words, " and ")
}

// countConstraint renders minItems and maxItems: `2..5 values` / "2 to 5 values".
func countConstraint(minItems int, maxItems *int) (compact, rule string) {
	switch {
	case minItems > 0 && maxItems != nil:
		return fmt.Sprintf("%d..%d values", minItems, *maxItems), fmt.Sprintf("%d to %d values", minItems, *maxItems)
	case minItems > 0:
		return fmt.Sprintf(">= %d %s", minItems, plural(minItems, "value")), fmt.Sprintf("at least %d %s", minItems, plural(minItems, "value"))
	case maxItems != nil:
		return fmt.Sprintf("<= %d %s", *maxItems, plural(*maxItems, "value")), fmt.Sprintf("at most %d %s", *maxItems, plural(*maxItems, "value"))
	}
	return "", ""
}

// formatBoundValue renders a bound as the author would write it: text as written, a duration
// or size in its own spelling, and a number without float noise.
func formatBoundValue(v any, elem string) string {
	if text, ok := v.(string); ok {
		return text
	}
	f := bound(v)
	if f == nil {
		return fmt.Sprint(v)
	}
	if elem == "time.Duration" {
		return shortDuration(time.Duration(int64(*f)))
	}
	return formatBoundIn(elem, *f)
}

// shortDuration renders d without the zero units time.Duration.String keeps: 1h, not 1h0m0s.
func shortDuration(d time.Duration) string {
	s := d.String()
	if strings.HasSuffix(s, "m0s") {
		s = strings.TrimSuffix(s, "0s")
	}
	if strings.HasSuffix(s, "h0m") {
		s = strings.TrimSuffix(s, "0m")
	}
	return s
}

func plural(n int, word string) string {
	if n == 1 {
		return word
	}
	return word + "s"
}

// codeSpan wraps s in a markdown code span whose backtick fence is longer than any run of
// backticks in s, padding with spaces when s starts or ends with one.
func codeSpan(s string) string {
	longest, run := 0, 0
	for _, r := range s {
		if r == '`' {
			run++
			longest = max(longest, run)
		} else {
			run = 0
		}
	}
	fence := strings.Repeat("`", longest+1)
	if strings.HasPrefix(s, "`") || strings.HasSuffix(s, "`") {
		s = " " + s + " "
	}
	return fence + s + fence
}
