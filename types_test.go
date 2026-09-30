package rotini

import (
	"errors"
	"net"
	"net/mail"
	"net/netip"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestByteSize_parses(t *testing.T) {
	for in, want := range map[string]int64{
		"4096":   4096,
		"512B":   512,
		"1K":     1000,
		"1kb":    1000,
		"10MB":   10_000_000,
		"1Ki":    1024,
		"512Mi":  512 << 20,
		"512MiB": 512 << 20,
		"512mi":  512 << 20, // letters are case-insensitive; the `i` is what makes it binary
		"1.5Gi":  3 << 29,
		"2Ti":    2 << 40,
		" 64Mi ": 64 << 20,
	} {
		var b ByteSize
		if err := b.UnmarshalText([]byte(in)); err != nil {
			t.Errorf("%q: %v", in, err)
			continue
		}
		if int64(b) != want {
			t.Errorf("%q = %d, want %d", in, b, want)
		}
	}
}

func TestByteSize_rejects(t *testing.T) {
	for _, in := range []string{"", "MB", "12XB", "-5Mi", "1.2.3G", "99999999Ei"} {
		var b ByteSize
		if err := b.UnmarshalText([]byte(in)); err == nil {
			t.Errorf("%q accepted as %d", in, b)
		}
	}
}

// String must round-trip: a size printed in help or a log parses back to the same value.
func TestByteSize_stringRoundTrips(t *testing.T) {
	for _, n := range []int64{0, 1, 1000, 1024, 512 << 20, 3 << 29, 1<<40 + 1} {
		s := ByteSize(n).String()
		var back ByteSize
		if err := back.UnmarshalText([]byte(s)); err != nil || int64(back) != n {
			t.Errorf("%d → %q → %d (%v)", n, s, back, err)
		}
	}
	if got := ByteSize(512 << 20).String(); got != "512Mi" {
		t.Errorf("String() = %q, want 512Mi", got)
	}
}

func TestHexBytes(t *testing.T) {
	for _, in := range []string{"deadbeef", "DEADBEEF", "0xdeadbeef", "0XDEADBEEF"} {
		var h HexBytes
		if err := h.UnmarshalText([]byte(in)); err != nil || h.String() != "deadbeef" {
			t.Errorf("%q → %q (%v)", in, h.String(), err)
		}
	}
	var h HexBytes
	if err := h.UnmarshalText([]byte("xyz")); err == nil {
		t.Error("non-hex accepted")
	}
}

func TestBase64Bytes(t *testing.T) {
	// "hi?>" encodes differently in the standard and URL-safe alphabets, and with and without
	// padding; all four spellings must decode to the same bytes.
	for _, in := range []string{"aGk/Pg==", "aGk/Pg", "aGk_Pg==", "aGk_Pg"} {
		var b Base64Bytes
		if err := b.UnmarshalText([]byte(in)); err != nil || string(b) != "hi?>" {
			t.Errorf("%q → %q (%v)", in, string(b), err)
		}
	}
	var b Base64Bytes
	if err := b.UnmarshalText([]byte("not base64!!")); err == nil {
		t.Error("invalid base64 accepted")
	}
}

func TestParseDuration_daysAndWeeks(t *testing.T) {
	for in, want := range map[string]time.Duration{
		"7d":    7 * 24 * time.Hour,
		"2w":    14 * 24 * time.Hour,
		"1d12h": 36 * time.Hour,
		"1.5d":  36 * time.Hour,
		"2w3d":  17 * 24 * time.Hour,
		"-1d":   -24 * time.Hour,
		"90m":   90 * time.Minute, // Go's own units are untouched
		"1h30m": 90 * time.Minute,
		"250ms": 250 * time.Millisecond,
	} {
		got, err := parseDuration(in)
		if err != nil || got != want {
			t.Errorf("%q = %v (%v), want %v", in, got, err, want)
		}
	}
	for _, in := range []string{"7x", "d", "1dd"} {
		if _, err := parseDuration(in); err == nil {
			t.Errorf("%q accepted", in)
		}
	}
}

// coerce is where every flag value becomes a Go value, so each new type is driven through it —
// the same path a real parse takes — rather than only through its own parser.
func TestCoerce_valueTypes(t *testing.T) {
	var fields struct {
		URL   *url.URL
		Mail  mail.Address
		TZ    *time.Location
		MAC   net.HardwareAddr
		Size  ByteSize
		Hex   HexBytes
		B64   Base64Bytes
		Dur   time.Duration
		Sizes []ByteSize
	}
	v := reflect.ValueOf(&fields).Elem()
	set := func(name string, raw ...string) {
		t.Helper()
		if err := coerce(v.FieldByName(name), raw); err != nil {
			t.Fatalf("%s %v: %v", name, raw, err)
		}
	}
	set("URL", "https://example.com:8443/v1?x=1")
	set("Mail", "Ada Lovelace <ada@example.com>")
	set("TZ", "Europe/Berlin")
	set("MAC", "01:23:45:67:89:ab")
	set("Size", "512Mi")
	set("Hex", "0xcafe")
	set("B64", "aGk=")
	set("Dur", "7d")
	set("Sizes", "1Ki", "2Ki")

	switch {
	case fields.URL.Host != "example.com:8443" || fields.URL.Path != "/v1":
		t.Errorf("url = %v", fields.URL)
	case fields.Mail.Address != "ada@example.com" || fields.Mail.Name != "Ada Lovelace":
		t.Errorf("mail = %+v", fields.Mail)
	case fields.TZ.String() != "Europe/Berlin":
		t.Errorf("tz = %v", fields.TZ)
	case fields.MAC.String() != "01:23:45:67:89:ab":
		t.Errorf("mac = %v", fields.MAC)
	case fields.Size != 512<<20:
		t.Errorf("size = %v", fields.Size)
	case fields.Hex.String() != "cafe":
		t.Errorf("hex = %v", fields.Hex)
	case string(fields.B64) != "hi":
		t.Errorf("b64 = %q", fields.B64)
	case fields.Dur != 7*24*time.Hour:
		t.Errorf("dur = %v", fields.Dur)
	case len(fields.Sizes) != 2 || fields.Sizes[1] != 2048:
		t.Errorf("sizes = %v", fields.Sizes)
	}
}

// A bad value is a typed coerceError naming the value and the type — which is what lets the
// caller redact a secret and name the flag the user typed.
func TestCoerce_valueTypesRejectBadInput(t *testing.T) {
	var fields struct {
		URL  *url.URL
		Mail mail.Address
		TZ   *time.Location
		MAC  net.HardwareAddr
		IP   netip.Addr
		Size ByteSize
	}
	v := reflect.ValueOf(&fields).Elem()
	for name, bad := range map[string]string{
		"URL":  "example.com", // no scheme: a bare host is a classic mistake
		"Mail": "not an address",
		"TZ":   "Mars/Olympus_Mons",
		"MAC":  "01:23",
		"IP":   "999.1.1.1",
		"Size": "12XB",
	} {
		field := v.FieldByName(name)
		err := coerce(field, []string{bad})
		// The message names the kind of value the user got wrong, never the Go type behind it.
		if want := "is not a valid " + valueTypeLabels[field.Type()]; err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: message %v, want it to say %q", name, err, want)
		}
		var ce *coerceError
		if !errors.As(err, &ce) || ce.Value != bad {
			t.Errorf("%s %q: err = %v, want a coerceError carrying the value", name, bad, err)
		}
		if err != nil && !strings.Contains(err.Error(), bad) {
			t.Errorf("%s: message %q does not name the bad value", name, err)
		}
	}
}

func TestParseBool_spellings(t *testing.T) {
	for in, want := range map[string]bool{
		"true": true, "TRUE": true, "t": true, "1": true, "yes": true, "Yes": true, "y": true, "on": true, "ON": true,
		"false": false, "F": false, "0": false, "no": false, "n": false, "off": false, "Off": false,
	} {
		got, err := parseBool(in)
		if err != nil || got != want {
			t.Errorf("parseBool(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, in := range []string{"", "maybe", "2", "yess", "enabled"} {
		if _, err := parseBool(in); err == nil {
			t.Errorf("parseBool(%q) accepted", in)
		}
	}
}

// The spelling reaches a bool flag through the one coercion path every source shares.
func TestParse_boolFlagSpellings(t *testing.T) {
	def := Definition{
		Name: "app", Handler: "App",
		Flags: []FlagDef{{Name: "cache", Identifiers: []string{"--cache"}, Type: "bool", Default: "true"}},
	}
	for in, want := range map[string]bool{"--cache=off": false, "--cache=yes": true, "--cache=N": false} {
		var out struct {
			App struct {
				Flags struct {
					Cache bool `rotini:"cache"`
				}
			}
		}
		if err := NewParser().Parse(NewContextFor(def, []string{in}), &out); err != nil {
			t.Fatalf("%s: %v", in, err)
		}
		if out.App.Flags.Cache != want {
			t.Errorf("%s → %v, want %v", in, out.App.Flags.Cache, want)
		}
	}
}

func TestParseTimeLayout(t *testing.T) {
	for _, tc := range []struct {
		in, layout string
		want       time.Time
	}{
		{"2026-09-29", "2006-01-02", time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)}, // a bare date is UTC midnight
		{"29/09/2026", "02/01/2006", time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)},
		{"Sep 29 2026 14:05", "Jan 2 2006 15:04", time.Date(2026, 9, 29, 14, 5, 0, 0, time.UTC)},
		{"1759104000", "unix", time.Unix(1759104000, 0).UTC()},
		{"1759104000.5", "unix", time.Unix(1759104000, 5e8).UTC()},
		{"1759104000123", "unixmilli", time.UnixMilli(1759104000123).UTC()},
	} {
		got, err := parseTimeLayout(tc.in, tc.layout)
		if err != nil || !got.Equal(tc.want) || got.Location() != time.UTC {
			t.Errorf("parseTimeLayout(%q, %q) = %v, %v; want %v UTC", tc.in, tc.layout, got, err, tc.want)
		}
	}
	for _, tc := range [][2]string{{"2026-13-01", "2006-01-02"}, {"2026-09-29T10:00:00Z", "2006-01-02"}, {"soon", "unix"}, {"1.5", "unixmilli"}} {
		if _, err := parseTimeLayout(tc[0], tc[1]); err == nil {
			t.Errorf("parseTimeLayout(%q, %q) accepted", tc[0], tc[1])
		}
	}
}

// A layout applies to a time field, a pointer to one, and each element of a list; without one a
// time is RFC 3339 and a failure says so in words, not in Go's parse error.
func TestCoerceWithLayout(t *testing.T) {
	var f struct {
		Day   time.Time
		Maybe *time.Time
		Days  []time.Time
		At    time.Time
	}
	v := reflect.ValueOf(&f).Elem()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(coerceWithLayout(v.FieldByName("Day"), []string{"2026-09-29"}, "2006-01-02"))
	must(coerceWithLayout(v.FieldByName("Maybe"), []string{"2026-09-30"}, "2006-01-02"))
	must(coerceWithLayout(v.FieldByName("Days"), []string{"2026-01-01", "2026-12-25"}, "2006-01-02"))
	must(coerceWithLayout(v.FieldByName("At"), []string{"2026-09-29T14:00:00Z"}, ""))
	if f.Day.Day() != 29 || f.Maybe == nil || f.Maybe.Day() != 30 || len(f.Days) != 2 || f.Days[1].Month() != 12 || f.At.Hour() != 14 {
		t.Errorf("%+v", f)
	}
	err := coerceWithLayout(v.FieldByName("Day"), []string{"29-09-2026"}, "2006-01-02")
	if err == nil || !strings.Contains(err.Error(), `"29-09-2026" is not a valid date (write it as 2006-01-02)`) {
		t.Errorf("err = %v", err)
	}
	err = coerceWithLayout(v.FieldByName("At"), []string{"2026-09-29"}, "")
	if err == nil || !strings.Contains(err.Error(), "is not a valid time (write it as RFC 3339") {
		t.Errorf("err = %v", err)
	}
}

// TestFormatDuration pins how a duration bound prints in an error: as a user writes one. A
// `maximum: 30d` used to be reported as "must be <= 720h0m0s" — a spelling the user never wrote,
// in hours because Go's formatter has no days (rubectl R-32). Every output parses back to the
// same duration.
func TestFormatDuration(t *testing.T) {
	for d, want := range map[time.Duration]string{
		30 * 24 * time.Hour:          "30d",
		36 * time.Hour:               "1d12h",
		time.Hour:                    "1h",
		90 * time.Minute:             "1h30m",
		2 * time.Minute:              "2m",
		90 * time.Second:             "1m30s",
		10 * time.Second:             "10s",
		1500 * time.Millisecond:      "1.5s",
		0:                            "0s",
		-2 * time.Hour:               "-2h",
		7*24*time.Hour + time.Second: "7d1s",
	} {
		got := formatDuration(d)
		if got != want {
			t.Errorf("formatDuration(%v) = %q, want %q", d, got, want)
		}
		if back, err := parseDuration(got); err != nil || back != d {
			t.Errorf("formatDuration(%v) = %q, which parses back to %v, %v", d, got, back, err)
		}
	}
}
