package rotini

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"time"
)

// Relative time directions: the values of [FlagDef.Relative] and [ArgDef.Relative].
const (
	relativePast   = "past"
	relativeFuture = "future"
	relativeBoth   = "both"
)

// WithClock sets the clock relative time values read: `2h` ago, `today`, `tomorrow`. A run
// reads it once, on first use, and every relative value in the run, and [Context.Now], uses
// that reading, so `--since 1h --until now` agree however long the run takes. nil restores
// time.Now.
func (p *Program) WithClock(now func() time.Time) *Program {
	p.clock = now
	return p
}

// WithClock sets the clock relative time values read. See [Program.WithClock]. It is for a
// Context built with [NewContextFor]; during a run, it replaces the run's reading. nil
// restores time.Now.
func (rtx *Context) WithClock(now func() time.Time) *Context {
	if rtx != nil {
		rtx.clock.set(now)
		rtx.mu.Lock()
		rtx.view = rtx.view.withClock(&rtx.clock)
		rtx.mu.Unlock()
	}
	return rtx
}

// Now returns the run's clock reading ([Program.WithClock]): the instant relative time values
// are measured from. The first call, or the first relative value, reads the clock; later calls
// return the same instant.
func (rtx *Context) Now() time.Time {
	if rtx == nil {
		return time.Now()
	}
	return rtx.clock.now()
}

// runClock is one run's clock and its single reading.
type runClock struct {
	mu   sync.Mutex
	fn   func() time.Time // nil reads time.Now
	at   time.Time
	read bool
}

// set replaces the clock and forgets any reading.
func (c *runClock) set(fn func() time.Time) {
	c.mu.Lock()
	c.fn, c.read = fn, false
	c.mu.Unlock()
}

// now reads the clock on first use and returns that reading from then on. A nil clock reads
// time.Now every time.
func (c *runClock) now() time.Time {
	if c == nil {
		return time.Now()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.read {
		fn := c.fn
		if fn == nil {
			fn = time.Now
		}
		c.at, c.read = fn(), true
	}
	return c.at
}

// timeSpec is how a time input reads its text: its layouts, tried in order (none means RFC
// 3339), and the relative forms it also accepts, measured from clock.
type timeSpec struct {
	layout   string   // the first layout, "" for RFC 3339
	layouts  []string // every layout, when there are several; nil means layout alone
	relative string   // "", past, future or both
	clock    *runClock
}

// flagTimeSpec is the time spec of flag fd, measured from clock.
func flagTimeSpec(fd FlagDef, clock *runClock) timeSpec {
	return timeSpec{layout: fd.Layout, layouts: fd.Layouts, relative: fd.Relative, clock: clock}
}

// argTimeSpec is the time spec of argument ad, measured from clock.
func argTimeSpec(ad ArgDef, clock *runClock) timeSpec {
	return timeSpec{layout: ad.Layout, layouts: ad.Layouts, relative: ad.Relative, clock: clock}
}

// plain reports whether the spec reads RFC 3339 only, as an undeclared time does.
func (ts timeSpec) plain() bool {
	return ts.layout == "" && len(ts.layouts) == 0 && ts.relative == ""
}

// list is every layout, in order; nil for RFC 3339.
func (ts timeSpec) list() []string {
	if len(ts.layouts) > 0 {
		return ts.layouts
	}
	if ts.layout != "" {
		return []string{ts.layout}
	}
	return nil
}

// dateOnly reports whether every layout writes a calendar date and no time of day, as
// `type: date` does: relative values then land on a date.
func (ts timeSpec) dateOnly() bool {
	list := ts.list()
	if len(list) == 0 {
		return false
	}
	for _, l := range list {
		if !isDateLayout(l) {
			return false
		}
	}
	return true
}

// isDateLayout reports whether layout writes no time of day: formatting an instant with one
// and reading it back gives that day's midnight.
func isDateLayout(layout string) bool {
	if layout == layoutUnix || layout == layoutUnixMilli {
		return false
	}
	ref := time.Date(2006, 1, 2, 15, 4, 5, 0, time.UTC)
	t, err := time.Parse(layout, ref.Format(layout))
	if err != nil {
		return false
	}
	h, m, s := t.Clock()
	return h == 0 && m == 0 && s == 0 && t.Nanosecond() == 0
}

// label names the spec's kind of value for an error message.
func (ts timeSpec) label() string {
	if ts.dateOnly() || (len(ts.layouts) == 0 && ts.layout == layoutDate) {
		return "date"
	}
	return "time"
}

// parseTimeValue reads s under ts. A relative input tries, in order: a word (now, today,
// yesterday, tomorrow), each layout (or RFC 3339), then an offset from the clock (2h, -3d).
// Absolute values come before offsets, so declaring `relative` never changes what an absolute
// value means. The error is a [*coerceError] naming what the input accepts.
func parseTimeValue(s string, ts timeSpec) (time.Time, error) {
	s = strings.TrimSpace(s)
	if ts.relative != "" {
		if t, ok, err := relativeWord(s, ts); ok {
			if err != nil {
				return time.Time{}, &coerceError{Value: s, TypeName: ts.label(), Cause: err}
			}
			return t, nil
		}
	}
	list := ts.list()
	if len(list) == 0 {
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			return t, nil
		}
	}
	var firstErr error
	for _, l := range list {
		t, err := parseTimeLayout(s, l)
		if err == nil {
			return t, nil
		}
		if firstErr == nil {
			firstErr = err
		}
	}
	if ts.relative != "" {
		t, ok, err := relativeOffset(s, ts)
		if ok {
			if err != nil {
				return time.Time{}, &coerceError{Value: s, TypeName: ts.label(), Cause: err}
			}
			return t, nil
		}
		return time.Time{}, &coerceError{Value: s, TypeName: ts.label(), Cause: errors.New("write it as " + ts.accepted())}
	}
	switch {
	case len(list) == 0:
		return time.Time{}, &coerceError{Value: s, TypeName: "time", Cause: errors.New("write it as RFC 3339, e.g. 2026-09-29T14:00:00Z; or declare `type: date` or a `layout:`")}
	case len(list) == 1:
		return time.Time{}, &coerceError{Value: s, TypeName: layoutLabel(list[0]), Cause: firstErr}
	}
	return time.Time{}, &coerceError{Value: s, TypeName: ts.label(), Cause: errors.New("write it as " + orList(describeLayouts(list)))}
}

// relativeWord reads one of the words a relative input accepts. ok is false when s is none of
// them; err is set when it is one the input's direction rules out.
func relativeWord(s string, ts timeSpec) (t time.Time, ok bool, err error) {
	word := strings.ToLower(s)
	var days int
	switch word {
	case "now", "today":
	case "yesterday":
		if ts.relative == relativeFuture {
			return time.Time{}, true, errors.New("yesterday is in the past; this input takes future times")
		}
		days = -1
	case "tomorrow":
		if ts.relative == relativePast {
			return time.Time{}, true, errors.New("tomorrow is in the future; this input takes past times")
		}
		days = 1
	default:
		return time.Time{}, false, nil
	}
	now := ts.clock.now()
	if word == "now" {
		return ts.landed(now), true, nil
	}
	y, m, d := now.Date()
	return ts.landed(time.Date(y, m, d, 0, 0, 0, 0, now.Location()).AddDate(0, 0, days)), true, nil
}

// relativeOffset reads a duration measured from the clock, with the sign rules of the input's
// direction: under past a bare 2h means ago and +2h is an error, under future it means from now
// and -2h is an error, and under both a sign is required. ok is false when s is no duration.
func relativeOffset(s string, ts timeSpec) (t time.Time, ok bool, err error) {
	sign, body := 0, s
	switch {
	case strings.HasPrefix(s, "+"):
		sign, body = 1, s[1:]
	case strings.HasPrefix(s, "-"):
		sign, body = -1, s[1:]
	}
	if body == "" || strings.HasPrefix(body, "+") || strings.HasPrefix(body, "-") {
		return time.Time{}, false, nil
	}
	d, perr := parseDuration(body)
	if perr != nil {
		return time.Time{}, false, nil //nolint:nilerr // not a duration: the caller says what the input accepts
	}
	switch ts.relative {
	case relativePast:
		if sign > 0 {
			return time.Time{}, true, fmt.Errorf("%s is a time from now; this input takes past times, such as %s", s, body)
		}
		sign = -1
	case relativeFuture:
		if sign < 0 {
			return time.Time{}, true, fmt.Errorf("%s is a time ago; this input takes future times, such as %s", s, body)
		}
		sign = 1
	default:
		if sign == 0 {
			return time.Time{}, true, fmt.Errorf("write -%s for %s ago or +%s for %s from now", s, s, s, s)
		}
	}
	if ts.dateOnly() && d%(24*time.Hour) != 0 {
		return time.Time{}, true, errors.New("a date moves in whole days, such as 1d or 2w")
	}
	return ts.landed(ts.clock.now().Add(time.Duration(sign) * d)), true, nil
}

// landed is a relative result in the input's kind: a date input gets the UTC midnight of the
// calendar date t falls on in the clock's location, the value a typed date gives.
func (ts timeSpec) landed(t time.Time) time.Time {
	if !ts.dateOnly() {
		return t
	}
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// accepted says what a relative input accepts, for an error message.
func (ts timeSpec) accepted() string {
	abs := "RFC 3339 (2026-09-29T14:00:00Z)"
	if list := ts.list(); len(list) > 0 {
		abs = orList(describeLayouts(list))
	}
	sample := "2h or 3d"
	if ts.dateOnly() {
		sample = "3d or 1w"
	}
	var offset, words string
	switch ts.relative {
	case relativePast:
		offset, words = "a time ago such as "+sample, "now, today or yesterday"
	case relativeFuture:
		offset, words = "a time from now such as "+sample, "now, today or tomorrow"
	default:
		offset, words = "a signed offset such as -"+strings.ReplaceAll(sample, " or ", " or +"), "now, today, yesterday or tomorrow"
	}
	return abs + ", " + offset + ", or " + words
}

// describeLayouts names layouts for a message: a Go layout as written, and unix and unixmilli
// by what they mean.
func describeLayouts(list []string) []string {
	out := make([]string, len(list))
	for i, l := range list {
		switch l {
		case layoutUnix:
			out[i] = "a Unix timestamp in seconds"
		case layoutUnixMilli:
			out[i] = "a Unix timestamp in milliseconds"
		default:
			out[i] = l
		}
	}
	return out
}

// orList joins items as "a", "a or b", "a, b or c".
func orList(items []string) string {
	switch len(items) {
	case 0:
		return ""
	case 1:
		return items[0]
	}
	return strings.Join(items[:len(items)-1], ", ") + " or " + items[len(items)-1]
}

// coerceTime is [coerce] for a time input that declares layouts or relative forms: a
// time.Time field (or a pointer to one, or a list of them) reads each value under ts. Any
// other field, or a plain spec, is plain coerce.
func coerceTime(f reflect.Value, raw []string, ts timeSpec) error {
	if ts.plain() || len(raw) == 0 {
		return coerce(f, raw)
	}
	switch {
	case f.Type() == timeType:
		t, err := parseTimeValue(raw[len(raw)-1], ts)
		if err != nil {
			return err
		}
		f.Set(reflect.ValueOf(t))
		return nil
	case f.Kind() == reflect.Pointer && f.Type().Elem() == timeType:
		v := reflect.New(timeType)
		if err := coerceTime(v.Elem(), raw, ts); err != nil {
			return err
		}
		f.Set(v)
		return nil
	case f.Kind() == reflect.Slice && derefType(f.Type().Elem()) == timeType:
		out := reflect.MakeSlice(f.Type(), len(raw), len(raw))
		for i, r := range raw {
			if err := coerceTime(out.Index(i), []string{r}, ts); err != nil {
				return err
			}
		}
		f.Set(out)
		return nil
	case f.Kind() == reflect.Map && f.Type().Key().Kind() == reflect.String && derefType(f.Type().Elem()) == timeType:
		// As coerceMap: "key=value" pairs split on the first '=', malformed ones skipped.
		out := reflect.MakeMapWithSize(f.Type(), len(raw))
		for _, pair := range raw {
			k, v, ok := strings.Cut(pair, "=")
			if !ok {
				continue
			}
			ev := reflect.New(f.Type().Elem()).Elem()
			if err := coerceTime(ev, []string{v}, ts); err != nil {
				return fmt.Errorf("value for key %q: %w", k, err)
			}
			out.SetMapIndex(reflect.ValueOf(k).Convert(f.Type().Key()), ev)
		}
		f.Set(out)
		return nil
	}
	return coerce(f, raw)
}

// tagTimeSpec reads an env or config field's time tags: layout:"<first>", layouts:"<json
// array>" when there are several, and relative:"past". Codegen writes the list as JSON so a
// layout may contain any character; a malformed list reads as the first layout alone.
func tagTimeSpec(tag reflect.StructTag) timeSpec {
	ts := timeSpec{layout: tag.Get("layout"), relative: tag.Get("relative")}
	if v := tag.Get("layouts"); v != "" {
		if err := json.Unmarshal([]byte(v), &ts.layouts); err != nil {
			ts.layouts = nil
		}
	}
	return ts
}
