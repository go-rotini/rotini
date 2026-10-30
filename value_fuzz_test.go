package rotini

import (
	"runtime"
	"strings"
	"testing"
	"time"
)

// Fuzz targets for the value readers that had none: time values, ${VAR} expansion and an
// object flag's key=value pairs. Each checks a property, not just the absence of a panic.

// FuzzParseTimeValue reads s under every time spec a spec can declare. A relative value lands
// on the side of the clock its direction allows, an RFC 3339 value round-trips, and every
// failure is a *coerceError, the shape the input errors render.
func FuzzParseTimeValue(f *testing.F) {
	for _, s := range []string{"2026-09-29T14:00:00Z", "2h", "-3d", "+1w", "now", "TODAY", "tomorrow", "0", "1.5", "2026-09-29", " 2h ", "+", "-", "--1h", "1e9"} {
		f.Add(s)
	}
	now := time.Date(2026, 10, 8, 14, 30, 0, 0, time.FixedZone("X", 2*3600))
	f.Fuzz(func(t *testing.T, s string) {
		for _, layouts := range [][]string{nil, {layoutDate}, {layoutUnix}, {layoutUnixMilli}, {"2006-01-02 15:04", layoutDate}} {
			for _, rel := range []string{"", relativePast, relativeFuture, relativeBoth} {
				ts := timeSpec{layouts: layouts, relative: rel, clock: fixedClock(now)}
				got, err := parseTimeValue(s, ts)
				if err != nil {
					if _, ok := err.(*coerceError); !ok {
						t.Fatalf("%q %v %q: error %T %v, want a *coerceError", s, layouts, rel, err, err)
					}
					continue
				}
				absolute, aerr := parseTimeValue(s, timeSpec{layouts: layouts, clock: fixedClock(now)})
				if aerr == nil && !got.Equal(absolute) {
					t.Fatalf("%q %v %q: relative read %v, absolute read %v; an absolute value must mean the same", s, layouts, rel, got, absolute)
				}
				if aerr != nil && !ts.dateOnly() && !strings.EqualFold(strings.TrimSpace(s), "today") { // today is its midnight
					switch rel {
					case relativePast:
						if got.After(now) {
							t.Fatalf("%q past: %v is after now", s, got)
						}
					case relativeFuture:
						if got.Before(now) {
							t.Fatalf("%q future: %v is before now", s, got)
						}
					}
				}
				if layouts == nil && rel == "" {
					back, err := time.Parse(time.RFC3339, got.Format(time.RFC3339Nano))
					if err != nil || !back.Equal(got) {
						t.Fatalf("%q: %v does not round-trip through RFC 3339: %v, %v", s, got, back, err)
					}
				}
			}
		}
	})
}

// FuzzExpandEnv checks ${VAR} expansion: text with every $ doubled expands back to itself, a
// word without $ is untouched, and a set variable expands to its value in both spellings.
func FuzzExpandEnv(f *testing.F) {
	for _, s := range []string{"", "$", "$$", "a$b", "${A}", "${A", "${A:-x}", "$1", "C$", "x$A$A"} {
		f.Add(s)
	}
	view := newOSView([]string{"A=alpha", "B_2=beta"}, "", runtime.GOOS)
	f.Fuzz(func(t *testing.T, s string) {
		if got, err := expandEnv(s, strings.ReplaceAll(s, "$", "$$"), view); err != nil || got != s {
			t.Fatalf("%q with every $ doubled: %q, %v", s, got, err)
		}
		if !strings.Contains(s, "$") {
			if got, err := expandEnv(s, s, view); err != nil || got != s {
				t.Fatalf("%q: %q, %v; want it untouched", s, got, err)
			}
		}
		plain := strings.ReplaceAll(s, "$", "$$")
		for _, ref := range []string{"$A", "${A}"} {
			// A name character after $A would continue the name, so the braced form is
			// the only one that can sit before any text.
			if ref == "$A" && plain != "" && isVarByte(plain[0]) {
				continue
			}
			got, err := expandEnv(ref+plain, ref+plain, view)
			if err != nil || got != "alpha"+s {
				t.Fatalf("%q: %q, %v; want %q", ref+plain, got, err, "alpha"+s)
			}
		}
		_, _ = expandEnv(s, s, view)
	})
}

// FuzzObjectPairsJoined checks an object flag's key=value pairs: quotePair's output splits back into
// exactly the pair it wrote, whatever the value holds, and pairs joined with commas split back
// into themselves.
func FuzzObjectPairsJoined(f *testing.F) {
	for _, s := range [][2]string{{"host", "h"}, {"tags", `a,"b"`}, {"k", ""}, {"k", `""`}, {"k", " , "}} {
		f.Add(s[0], s[1])
	}
	f.Fuzz(func(t *testing.T, key, value string) {
		if key == "" || strings.ContainsAny(key, `,"= `) {
			return
		}
		one := quotePair(key, value)
		got, err := splitPairs(one)
		if err != nil || len(got) != 1 || got[0] != key+"="+value {
			t.Fatalf("splitPairs(%q) = %q, %v; want [%q]", one, got, err, key+"="+value)
		}
		got, err = splitPairs(one + "," + quotePair(key, value))
		if err != nil || len(got) != 2 || got[0] != got[1] || got[0] != key+"="+value {
			t.Fatalf("two pairs %q: %q, %v", one+","+one, got, err)
		}
		_, _ = splitPairs(value)
	})
}
