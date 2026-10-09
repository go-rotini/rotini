package rotini

import (
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"net"
	"net/mail"
	"net/netip"
	"net/url"
	"path"
	"path/filepath"
	"reflect"
	"regexp"
	"regexp/syntax"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Value types beyond Go's builtins and encoding.TextUnmarshaler, by two mechanisms:
//
//   - A distinct standard-library type (*url.URL, mail.Address, *time.Location,
//     net.HardwareAddr) is parsed through [valueParsers], keyed on the type itself.
//   - A value whose Go representation is ambiguous (a byte size is an int64; hex and base64 are
//     []byte) gets a named type that parses itself: [ByteSize], [HexBytes], [Base64Bytes].

// ByteSize is a count of bytes that parses human sizes: `512Mi`, `10MB`, `1.5GiB`, `4096`.
//
// Suffixes follow the SI and IEC standards; the `i` makes a unit binary:
//
//	B                  1
//	K  KB   M  MB  …   1000, 1000², … (decimal)
//	Ki KiB  Mi MiB …   1024, 1024², … (binary)
//
// Letters are case-insensitive and fractions are allowed (`1.5Gi`); the result is rounded to a
// whole number of bytes. A bare `m` is decimal (megabytes), unlike some tools that read it as
// binary.
//
// A spec declares one with `type: bytesize`.
type ByteSize int64

// UnmarshalText implements [encoding.TextUnmarshaler].
func (b *ByteSize) UnmarshalText(text []byte) error {
	n, err := parseByteSize(string(text))
	if err != nil {
		return err
	}
	*b = ByteSize(n)
	return nil
}

// MarshalText implements [encoding.TextMarshaler], rendering the size as [ByteSize.String] does.
func (b ByteSize) MarshalText() ([]byte, error) { return []byte(b.String()), nil }

// String renders the size in the largest binary unit it is an exact multiple of — `512Mi`, `2Gi`
// — and in plain bytes otherwise, so it always parses back to the same value.
func (b ByteSize) String() string {
	n := int64(b)
	if n == 0 {
		return "0"
	}
	units := []string{"Ei", "Pi", "Ti", "Gi", "Mi", "Ki"}
	for i, u := range units {
		size := int64(1) << (10 * (len(units) - i))
		if n%size == 0 {
			return strconv.FormatInt(n/size, 10) + u
		}
	}
	return strconv.FormatInt(n, 10)
}

var byteSizeSyntax = sync.OnceValue(func() *regexp.Regexp {
	return regexp.MustCompile(`^\s*(\d*\.?\d+)\s*([a-zA-Z]*)\s*$`)
})

// byteSizeUnits maps a lowercased suffix to its multiplier. The trailing `b` is optional.
var byteSizeUnits = map[string]float64{
	"": 1, "b": 1,
	"k": 1e3, "kb": 1e3, "m": 1e6, "mb": 1e6, "g": 1e9, "gb": 1e9,
	"t": 1e12, "tb": 1e12, "p": 1e15, "pb": 1e15, "e": 1e18, "eb": 1e18,
	"ki": 1 << 10, "kib": 1 << 10, "mi": 1 << 20, "mib": 1 << 20, "gi": 1 << 30, "gib": 1 << 30,
	"ti": 1 << 40, "tib": 1 << 40, "pi": 1 << 50, "pib": 1 << 50, "ei": 1 << 60, "eib": 1 << 60,
}

func parseByteSize(s string) (int64, error) {
	m := byteSizeSyntax().FindStringSubmatch(s)
	if m == nil {
		return 0, fmt.Errorf("%q is not a size; write a number with an optional unit, e.g. 512Mi, 10MB, 1.5GiB", s)
	}
	mult, ok := byteSizeUnits[strings.ToLower(m[2])]
	if !ok {
		return 0, fmt.Errorf("%q has an unknown unit %q; use B, K/KB, M/MB, G/GB, T/TB, P/PB, E/EB (decimal) or Ki, Mi, Gi, Ti, Pi, Ei (binary)", s, m[2])
	}
	n, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0, fmt.Errorf("%q is not a size: %w", s, err)
	}
	bytes := math.Round(n * mult)
	if bytes > math.MaxInt64 {
		return 0, fmt.Errorf("%q is too large to count in bytes", s)
	}
	return int64(bytes), nil
}

// HexBytes is binary data written as hexadecimal, with or without a `0x` prefix: `deadbeef`,
// `0xDEADBEEF`. A spec declares one with `type: hexbytes`.
type HexBytes []byte

// UnmarshalText implements [encoding.TextUnmarshaler].
func (h *HexBytes) UnmarshalText(text []byte) error {
	s := strings.TrimSpace(string(text))
	s = strings.TrimPrefix(strings.TrimPrefix(s, "0x"), "0X")
	b, err := hex.DecodeString(s)
	if err != nil {
		return fmt.Errorf("%q is not hexadecimal: %w", string(text), err)
	}
	*h = b
	return nil
}

// MarshalText implements [encoding.TextMarshaler] as lowercase hex without a prefix.
func (h HexBytes) MarshalText() ([]byte, error) { return []byte(h.String()), nil }

// String renders the bytes as lowercase hexadecimal.
func (h HexBytes) String() string { return hex.EncodeToString(h) }

// Base64Bytes is binary data written as base64. Standard and URL-safe alphabets are both
// accepted, padded or not. A spec declares one with `type: base64bytes`.
type Base64Bytes []byte

// UnmarshalText implements [encoding.TextUnmarshaler].
func (b *Base64Bytes) UnmarshalText(text []byte) error {
	s := strings.TrimSpace(string(text))
	for _, enc := range []*base64.Encoding{
		base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding,
	} {
		if out, err := enc.DecodeString(s); err == nil {
			*b = out
			return nil
		}
	}
	return fmt.Errorf("%q is not base64", string(text))
}

// MarshalText implements [encoding.TextMarshaler] with the standard, padded alphabet.
func (b Base64Bytes) MarshalText() ([]byte, error) { return []byte(b.String()), nil }

// String renders the bytes as standard, padded base64.
func (b Base64Bytes) String() string { return base64.StdEncoding.EncodeToString(b) }

// Glob is a validated path.Match pattern: `*` and `?` match within one path segment, `[a-z]` a
// class, and `\` escapes the next character. It matches slash-separated names, and `**` is not
// recursive: it means two `*`. A spec declares one with `type: glob`.
type Glob string

// UnmarshalText implements [encoding.TextUnmarshaler], rejecting a malformed pattern.
func (g *Glob) UnmarshalText(text []byte) error {
	if _, err := path.Match(string(text), ""); err != nil {
		return fmt.Errorf("check its brackets and escapes: %w", err)
	}
	*g = Glob(text)
	return nil
}

// MarshalText implements [encoding.TextMarshaler].
func (g Glob) MarshalText() ([]byte, error) { return []byte(g), nil }

// Match reports whether name matches the pattern. name's separators are read as slashes, so a
// Windows path matches the same pattern a Unix one does. A malformed pattern, which
// [Glob.UnmarshalText] rejects, matches nothing.
func (g Glob) Match(name string) bool {
	ok, err := path.Match(string(g), filepath.ToSlash(name))
	return ok && err == nil
}

// valueParsers parse standard-library input types that do not implement
// encoding.TextUnmarshaler, keyed on the exact field type. [coerce] consults it before anything
// else, including pointer dereferencing, because *url.URL and *time.Location are built by their
// parsers rather than filled in place.
var valueParsers = map[reflect.Type]func(string) (reflect.Value, error){
	reflect.TypeFor[*url.URL](): func(s string) (reflect.Value, error) {
		u, err := url.Parse(s)
		if err != nil {
			return reflect.Value{}, fmt.Errorf("want a URL such as https://example.com: %w", err)
		}
		if u.Scheme == "" || (u.Host == "" && u.Opaque == "") {
			return reflect.Value{}, errors.New("a URL needs a scheme and a host, e.g. https://example.com")
		}
		// "localhost:8080" parses as scheme "localhost" with the opaque part "8080": a host and
		// port written without a scheme.
		if u.Host == "" && isAllDigits(u.Opaque) {
			return reflect.Value{}, fmt.Errorf("a host and port need a scheme, as in http://%s", s)
		}
		return reflect.ValueOf(u), nil
	},
	reflect.TypeFor[mail.Address](): func(s string) (reflect.Value, error) {
		a, err := mail.ParseAddress(s)
		if err != nil {
			return reflect.Value{}, fmt.Errorf("want ada@example.com or \"Ada <ada@example.com>\": %w", err)
		}
		return reflect.ValueOf(*a), nil
	},
	reflect.TypeFor[*time.Location](): func(s string) (reflect.Value, error) {
		loc, err := time.LoadLocation(s)
		if err != nil {
			return reflect.Value{}, fmt.Errorf("want an IANA name such as Europe/Berlin: %w", err)
		}
		return reflect.ValueOf(loc), nil
	},
	// time.Time parses itself, but this entry replaces its unhelpful layout error with one
	// that names the expected format.
	reflect.TypeFor[time.Time](): func(s string) (reflect.Value, error) {
		t, err := time.Parse(time.RFC3339, strings.TrimSpace(s))
		if err != nil {
			return reflect.Value{}, errors.New("write it as RFC 3339, e.g. 2026-09-29T14:00:00Z; or declare `type: date` or a `layout:`")
		}
		return reflect.ValueOf(t), nil
	},
	reflect.TypeFor[net.HardwareAddr](): func(s string) (reflect.Value, error) {
		mac, err := net.ParseMAC(s)
		if err != nil {
			return reflect.Value{}, fmt.Errorf("want hex pairs such as 01:23:45:67:89:ab: %w", err)
		}
		return reflect.ValueOf(mac), nil
	},
	// *regexp.Regexp parses itself, but this entry names the problem without Go's prefix.
	reflect.TypeFor[*regexp.Regexp](): func(s string) (reflect.Value, error) {
		re, err := regexp.Compile(s)
		if err != nil {
			if se, ok := errors.AsType[*syntax.Error](err); ok {
				return reflect.Value{}, fmt.Errorf("%s: `%s`", se.Code, se.Expr)
			}
			return reflect.Value{}, fmt.Errorf("want a regular expression: %w", err)
		}
		return reflect.ValueOf(re), nil
	},
}

// valueTypeLabels name value types in a parse error in user terms ("URL", not "*url.URL").
var valueTypeLabels = map[reflect.Type]string{
	reflect.TypeFor[*url.URL]():         "URL",
	reflect.TypeFor[mail.Address]():     "email address",
	reflect.TypeFor[*time.Location]():   "time zone",
	reflect.TypeFor[net.HardwareAddr](): "MAC address",
	reflect.TypeFor[netip.Addr]():       "IP address",
	reflect.TypeFor[netip.Prefix]():     "CIDR prefix",
	reflect.TypeFor[netip.AddrPort]():   "address and port",
	reflect.TypeFor[time.Time]():        "time",
	reflect.TypeFor[ByteSize]():         "size",
	reflect.TypeFor[HexBytes]():         "hex value",
	reflect.TypeFor[Base64Bytes]():      "base64 value",
	reflect.TypeFor[*regexp.Regexp]():   "regular expression",
	reflect.TypeFor[Glob]():             "glob pattern",
}

// typeLabel names a field type for a parse error: its label when it has one, else its Go name.
func typeLabel(t reflect.Type) string {
	if l, ok := valueTypeLabels[t]; ok {
		return l
	}
	return t.String()
}

// parseBool reads true/false, t/f, 1/0, yes/no, y/n and on/off, case-insensitively: a wider
// set than strconv.ParseBool accepts.
func parseBool(s string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "t", "1", "yes", "y", "on":
		return true, nil
	case "false", "f", "0", "no", "n", "off":
		return false, nil
	}
	return false, fmt.Errorf("%q is not a boolean; use true/false, t/f, yes/no, y/n, on/off or 1/0", s)
}

// Time layouts beyond Go's reference-time layouts: a Unix timestamp in seconds (fractions
// allowed) or in milliseconds.
const (
	layoutUnix      = "unix"
	layoutUnixMilli = "unixmilli"
	// layoutDate is the layout `type: date` gets: a calendar date, parsed as UTC midnight.
	layoutDate = "2006-01-02"
)

// parseTimeLayout parses s under a time input's layout: a Go reference-time layout, or
// "unix" / "unixmilli". A layout with no zone parses as UTC, so a bare date is
// that day's UTC midnight.
func parseTimeLayout(s, layout string) (time.Time, error) {
	s = strings.TrimSpace(s)
	switch layout {
	case layoutUnix:
		secs, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return time.Time{}, errors.New("want a Unix timestamp in seconds, e.g. 1759104000")
		}
		whole, frac := math.Modf(secs)
		return time.Unix(int64(whole), int64(math.Round(frac*1e9))).UTC(), nil
	case layoutUnixMilli:
		ms, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return time.Time{}, errors.New("want a Unix timestamp in milliseconds, e.g. 1759104000000")
		}
		return time.UnixMilli(ms).UTC(), nil
	}
	t, err := time.Parse(layout, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("write it as %s", layout)
	}
	return t, nil
}

// layoutLabel names a layout's kind of value for an error message.
func layoutLabel(layout string) string {
	if layout == layoutDate {
		return "date"
	}
	return "time"
}

var timeType = reflect.TypeFor[time.Time]()

// durationDays matches a day or week component of a duration: `7d`, `1.5w`.
var durationDays = sync.OnceValue(func() *regexp.Regexp {
	return regexp.MustCompile(`(\d*\.?\d+)([dw])`)
})

// parseDuration is [time.ParseDuration] plus days (`d`, 24h) and weeks (`w`, 168h).
// Components combine as usual: `1d12h`, `2w3d`, `1.5d`.
func parseDuration(s string) (time.Duration, error) {
	expanded := s
	if strings.ContainsAny(s, "dw") {
		var err error
		if expanded, err = expandDays(s); err != nil {
			return 0, err
		}
	}
	d, err := time.ParseDuration(expanded)
	if err != nil {
		return 0, fmt.Errorf("%q is not a duration such as 90s, 1h30m or 7d: %w", s, err)
	}
	return d, nil
}

// formatDuration writes d the way a duration input is written, with whole days as "d" and no
// zero units ("30d", "1d12h", "1h30m", "90s"), so a bound such as `maximum: 30d` prints in an
// error as the spec wrote it.
func formatDuration(d time.Duration) string {
	const day = 24 * time.Hour
	// Split before negating: -d overflows for the most negative duration, but its whole days
	// and the remainder each negate safely.
	days, rest, sign := d/day, d%day, ""
	if d < 0 {
		days, rest, sign = -days, -rest, "-"
	}
	out := sign
	if days > 0 {
		out += strconv.FormatInt(int64(days), 10) + "d"
		if rest == 0 {
			return out
		}
	}
	d = rest
	s := d.String() // "1h0m0s", "2m0s", "1h30m0s", "90ms"
	if strings.HasSuffix(s, "m0s") {
		s = strings.TrimSuffix(s, "0s")
	}
	if strings.HasSuffix(s, "h0m") {
		s = strings.TrimSuffix(s, "0m")
	}
	return out + s
}

// expandDays rewrites each day and week component of a duration as hours.
func expandDays(s string) (string, error) {
	var convErr error
	expanded := durationDays().ReplaceAllStringFunc(s, func(part string) string {
		m := durationDays().FindStringSubmatch(part)
		n, err := strconv.ParseFloat(m[1], 64)
		if err != nil {
			convErr = err
			return part
		}
		hours := n * 24
		if m[2] == "w" {
			hours = n * 24 * 7
		}
		return strconv.FormatFloat(hours, 'f', -1, 64) + "h"
	})
	return expanded, convErr
}

// isAllDigits reports whether s is one or more ASCII digits.
func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := range len(s) {
		if !isDigit(s[i]) {
			return false
		}
	}
	return true
}
