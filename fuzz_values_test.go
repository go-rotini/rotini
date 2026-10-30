package rotini

import (
	"bytes"
	"net/url"
	"path"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

// FuzzHexBytes checks that HexBytes never panics and that what it accepts survives MarshalText.
func FuzzHexBytes(f *testing.F) {
	for _, s := range []string{"", "0x", "0X", "deadbeef", "0xDEADBEEF", " ab ", "abc", "zz", "0x0x00", "\x00"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		var h HexBytes
		if err := h.UnmarshalText([]byte(s)); err != nil {
			return
		}
		text, _ := h.MarshalText()
		var back HexBytes
		if err := back.UnmarshalText(text); err != nil || !bytes.Equal(back, h) {
			t.Fatalf("%q: %x does not round-trip through %q: %v", s, []byte(h), text, err)
		}
	})
}

// FuzzBase64Bytes checks that Base64Bytes never panics and that what it accepts survives
// MarshalText.
func FuzzBase64Bytes(f *testing.F) {
	for _, s := range []string{"", "QQ==", "QQ", "QR==", "/w==", "_w", "-_8", "a", "====", " QQ== ", "Q\nQ=="} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		var b Base64Bytes
		if err := b.UnmarshalText([]byte(s)); err != nil {
			return
		}
		text, _ := b.MarshalText()
		var back Base64Bytes
		if err := back.UnmarshalText(text); err != nil || !bytes.Equal(back, b) {
			t.Fatalf("%q: %x does not round-trip through %q: %v", s, []byte(b), text, err)
		}
	})
}

// FuzzGlob checks that a pattern Glob accepts is well-formed against every name, so Match never
// hides a pattern error, and that a pattern with no metacharacters matches itself.
func FuzzGlob(f *testing.F) {
	for _, s := range [][2]string{{"*.go", "a.go"}, {"[a-z]", "q"}, {`\*`, "*"}, {"[", ""}, {`a\`, "a"}, {"[^]a]", "b"}, {"**", "a/b"}} {
		f.Add(s[0], s[1])
	}
	f.Fuzz(func(t *testing.T, pattern, name string) {
		var g Glob
		if err := g.UnmarshalText([]byte(pattern)); err != nil {
			return
		}
		if _, err := path.Match(pattern, name); err != nil {
			t.Fatalf("accepted pattern %q fails against %q: %v", pattern, name, err)
		}
		if !strings.ContainsAny(pattern, `*?[\`) && !g.Match(pattern) {
			t.Fatalf("literal pattern %q does not match itself", pattern)
		}
		text, _ := g.MarshalText()
		if string(text) != pattern {
			t.Fatalf("MarshalText = %q, want %q", text, pattern)
		}
	})
}

// FuzzByteSizeRoundTrip checks the String claim: a size that isn't negative parses back to the
// same value.
func FuzzByteSizeRoundTrip(f *testing.F) {
	for _, n := range []int64{0, 1, 1023, 1024, 1 << 20, 3 << 30, 1<<63 - 1, 1 << 62, 7 << 60} {
		f.Add(n)
	}
	f.Fuzz(func(t *testing.T, n int64) {
		if n < 0 {
			return
		}
		text, _ := ByteSize(n).MarshalText()
		var back ByteSize
		if err := back.UnmarshalText(text); err != nil || int64(back) != n {
			t.Fatalf("%d renders %q, which reads back as %d (%v)", n, text, back, err)
		}
	})
}

// FuzzFormatDuration checks that a duration written as a bound prints in a form a duration
// input reads back to the same value.
func FuzzFormatDuration(f *testing.F) {
	for _, d := range []int64{0, 1, int64(time.Second), int64(90 * time.Minute), int64(36 * time.Hour), -int64(24 * time.Hour), 1<<63 - 1, -1 << 63} {
		f.Add(d)
	}
	f.Fuzz(func(t *testing.T, n int64) {
		d := time.Duration(n)
		s := formatDuration(d)
		back, err := parseDuration(s)
		if err != nil {
			// The extremes print as days of more hours than a duration holds.
			if d > 290*365*24*time.Hour || d < -290*365*24*time.Hour {
				return
			}
			t.Fatalf("%d prints %q, which does not read back: %v", n, s, err)
		}
		if back != d {
			t.Fatalf("%d prints %q, which reads back as %d", n, s, back)
		}
	})
}

// fuzzTimeLayouts are the layouts FuzzTimeValue picks from, by index.
var fuzzTimeLayouts = [][]string{nil, {layoutDate}, {layoutUnix}, {layoutUnixMilli}, {time.Kitchen}, {layoutDate, time.RFC1123}, {"2006-01-02 15:04"}}

// FuzzTimeValue checks parseTimeValue on every layout and direction: it never panics, its
// failures are coercion errors, declaring `relative` never changes what an absolute value means,
// and a past (future) input never lands after (before) the clock.
func FuzzTimeValue(f *testing.F) {
	for _, s := range []string{"2026-09-29T14:00:00Z", "2026-09-29", "now", "today", "Yesterday", "tomorrow", "2h", "-2h", "+2h", "3d", "-1.5w", "1759104000", "-0.5", "1759104000000", "3:04PM", "", "+", "-", "--2h", "+-2h", "1d2h", "99999999999999999999d"} {
		for i := range fuzzTimeLayouts {
			f.Add(s, uint8(i), uint8(i%4))
		}
	}
	fixed := time.Date(2026, 9, 29, 14, 30, 0, 0, time.UTC)
	directions := []string{"", relativePast, relativeFuture, relativeBoth}
	f.Fuzz(func(t *testing.T, s string, layoutIdx, dirIdx uint8) {
		layouts := fuzzTimeLayouts[int(layoutIdx)%len(fuzzTimeLayouts)]
		dir := directions[int(dirIdx)%len(directions)]
		spec := func(relative string) timeSpec {
			ts := timeSpec{relative: relative, clock: &runClock{fn: func() time.Time { return fixed }}}
			if len(layouts) == 1 {
				ts.layout = layouts[0]
			} else if len(layouts) > 1 {
				ts.layout, ts.layouts = layouts[0], layouts
			}
			return ts
		}
		abs, absErr := parseTimeValue(s, spec(""))
		if absErr != nil {
			if _, ok := asError[*coerceError](absErr); !ok {
				t.Fatalf("%q: error %T is not a coercion error", s, absErr)
			}
		}
		if dir == "" {
			return
		}
		got, err := parseTimeValue(s, spec(dir))
		if err != nil {
			if _, ok := asError[*coerceError](err); !ok {
				t.Fatalf("%q: error %T is not a coercion error", s, err)
			}
		}
		word := slices.Contains([]string{"now", "today", "yesterday", "tomorrow"}, strings.ToLower(strings.TrimSpace(s)))
		if absErr == nil && !word && (err != nil || !got.Equal(abs)) {
			t.Fatalf("%q under %s: absolute %v became %v (%v)", s, dir, abs, got, err)
		}
		if err != nil || absErr == nil || word {
			return
		}
		ref := spec(dir).landed(fixed)
		switch dir {
		case relativePast:
			if got.After(ref) {
				t.Fatalf("%q under past landed at %v, after %v", s, got, ref)
			}
		case relativeFuture:
			if got.Before(ref) {
				t.Fatalf("%q under future landed at %v, before %v", s, got, ref)
			}
		}
	})
}

// FuzzURLValue checks the url kind's claim: what it accepts has a scheme and a host (or an
// opaque part that is not a bare port).
func FuzzURLValue(f *testing.F) {
	for _, s := range []string{"https://example.com", "localhost:8080/api", "mailto:a@b", "tel:5551234", "x", "", "://", "http://", "a:65535", "a:65536"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		v, err := parseURLValue(s)
		if err != nil {
			return
		}
		u := v.Interface().(*url.URL)
		if u.Scheme == "" || (u.Host == "" && (u.Opaque == "" || isPortOpaque(u.Opaque))) {
			t.Fatalf("%q accepted as %#v", s, u)
		}
	})
}

// FuzzObjectPairs checks key=value object text: decoding never panics, and quotePair writes a
// pair splitPairs reads back whole.
func FuzzObjectPairs(f *testing.F) {
	for _, s := range []string{"host=h,port=5", `host="a,b"`, `"host=a,b"`, `a=""""`, "m.a.b=5", "x", "=", "a=1, b=2", `"`, "tags=a,tags=b", "n.x=1"} {
		f.Add(s, "k", s)
	}
	type target struct {
		Host string         `json:"host"`
		Port int            `json:"port"`
		Tags []string       `json:"tags"`
		M    map[string]any `json:"m"`
		N    struct {
			X uint8 `json:"x"`
		} `json:"n"`
	}
	typ := reflect.TypeFor[target]()
	fs := allFeatures()
	f.Fuzz(func(t *testing.T, text, key, value string) {
		_, _ = decodeObject(fs, text, typ)
		if strings.ContainsAny(key, `,"=`) {
			return
		}
		pairs, err := splitPairs(quotePair(key, value))
		if err != nil || len(pairs) != 1 || pairs[0] != key+"="+value {
			t.Fatalf("quotePair(%q, %q) splits as %q (%v)", key, value, pairs, err)
		}
	})
}
