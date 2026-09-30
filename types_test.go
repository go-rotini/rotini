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
