package rotini

import (
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"math/bits"
	"path"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"time"
)

// Value types beyond Go's builtins and encoding.TextUnmarshaler, by two mechanisms:
//
//   - A distinct standard-library type (*url.URL, mail.Address, *time.Location,
//     net.HardwareAddr, the netip types, *regexp.Regexp) is a value kind, wired by its feature
//     ([URLValues] and the rest).
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
// — and in plain bytes otherwise, so a size that isn't negative parses back to the same value.
// A negative size renders with a minus sign, which [ByteSize.UnmarshalText] refuses.
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

// byteSizeUnit is the multiplier of a lowercased suffix. The trailing `b` is optional.
func byteSizeUnit(lower string) (float64, bool) {
	switch lower {
	case "", "b":
		return 1, true
	case "k", "kb":
		return 1e3, true
	case "m", "mb":
		return 1e6, true
	case "g", "gb":
		return 1e9, true
	case "t", "tb":
		return 1e12, true
	case "p", "pb":
		return 1e15, true
	case "e", "eb":
		return 1e18, true
	case "ki", "kib":
		return 1 << 10, true
	case "mi", "mib":
		return 1 << 20, true
	case "gi", "gib":
		return 1 << 30, true
	case "ti", "tib":
		return 1 << 40, true
	case "pi", "pib":
		return 1 << 50, true
	case "ei", "eib":
		return 1 << 60, true
	}
	return 0, false
}

// splitByteSize splits a size into its number and its unit: blanks (space, tab, newline,
// carriage return, form feed), a decimal number (digits, or digits and a fraction, or a bare
// fraction such as .5), blanks, ASCII letters, blanks. ok is false for anything else.
func splitByteSize(s string) (number, unit string, ok bool) {
	i := skipSizeBlanks(s, 0)
	end, ok := scanDecimal(s, i)
	if !ok {
		return "", "", false
	}
	number = s[i:end]
	i = skipSizeBlanks(s, end)
	j := i
	for j < len(s) && isASCIILetter(s[j]) {
		j++
	}
	unit = s[i:j]
	if skipSizeBlanks(s, j) != len(s) {
		return "", "", false
	}
	return number, unit, true
}

// scanDecimal reads a decimal number at s[i:]: digits, optionally followed by "." and at least
// one digit, or "." and at least one digit alone. It returns the index after the number.
func scanDecimal(s string, i int) (int, bool) {
	j := skipDigits(s, i)
	if j < len(s) && s[j] == '.' {
		if k := skipDigits(s, j+1); k > j+1 {
			return k, true
		}
	}
	return j, j > i
}

// skipDigits returns the index of the first byte at or after i that is not an ASCII digit.
func skipDigits(s string, i int) int {
	for i < len(s) && isDigit(s[i]) {
		i++
	}
	return i
}

// skipSizeBlanks returns the index of the first byte at or after i that is not a blank.
func skipSizeBlanks(s string, i int) int {
	for i < len(s) && (s[i] == ' ' || s[i] == '\t' || s[i] == '\n' || s[i] == '\r' || s[i] == '\f') {
		i++
	}
	return i
}

func isASCIILetter(c byte) bool { return 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' }

func parseByteSize(s string) (int64, error) {
	number, unit, ok := splitByteSize(s)
	if !ok {
		return 0, fmt.Errorf("%q is not a size; write a number with an optional unit, e.g. 512Mi, 10MB, 1.5GiB", s)
	}
	mult, ok := byteSizeUnit(strings.ToLower(unit))
	if !ok {
		return 0, fmt.Errorf("%q has an unknown unit %q; use B, K/KB, M/MB, G/GB, T/TB, P/PB, E/EB (decimal) or Ki, Mi, Gi, Ti, Pi, Ei (binary)", s, unit)
	}
	return byteCount(s, number, mult)
}

// byteCount is the number of bytes number (digits, with an optional fraction) of a unit of
// mult bytes makes, for parseByteSize reading s. A whole number is counted exactly; a fraction
// is rounded to the nearest byte.
func byteCount(s, number string, mult float64) (int64, error) {
	tooLarge := fmt.Errorf("%q is too large to count in bytes", s)
	if !strings.Contains(number, ".") {
		n, err := strconv.ParseUint(number, 10, 64)
		if err != nil {
			return 0, tooLarge // number is digits only, so the error is its range
		}
		hi, lo := bits.Mul64(n, uint64(mult))
		if hi != 0 || lo > math.MaxInt64 {
			return 0, tooLarge
		}
		return int64(lo), nil
	}
	n, err := strconv.ParseFloat(number, 64)
	if err != nil {
		return 0, tooLarge // number is a plain decimal, so the error is its range
	}
	bytes := math.Round(n * mult)
	if bytes >= 1<<63 { // float64(math.MaxInt64) rounds up to 2^63, which int64 can't hold
		return 0, tooLarge
	}
	return int64(bytes), nil
}

// HexBytes is binary data written as hexadecimal, with or without a `0x` prefix: `deadbeef`,
// `0xDEADBEEF`. A spec declares one with `type: hexbytes`.
type HexBytes []byte

// UnmarshalText implements [encoding.TextUnmarshaler].
func (h *HexBytes) UnmarshalText(text []byte) error {
	s := strings.TrimSpace(string(text))
	if len(s) >= 2 && s[0] == '0' && (s[1] == 'x' || s[1] == 'X') {
		s = s[2:]
	}
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

// parseRFC3339Value reads a time.Time as RFC 3339. time.Time parses itself, but this replaces
// its unhelpful layout error with one that names the expected format.
func parseRFC3339Value(s string) (reflect.Value, error) {
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(s))
	if err != nil {
		return reflect.Value{}, errors.New("write it as RFC 3339, e.g. 2026-09-29T14:00:00Z; or declare `type: date` or a `layout:`")
	}
	return reflect.ValueOf(t), nil
}

// typeLabel names a field type for a parse error in user terms ("URL", not "*url.URL"): a
// wired value kind's label, rotini's own types' labels, else its Go name.
func typeLabel(t reflect.Type, fs *featureSet) string {
	if k := fs.valueKindOf(t); k != nil {
		return k.label
	}
	switch t {
	case timeType:
		return "time"
	case reflect.TypeFor[ByteSize]():
		return "size"
	case reflect.TypeFor[HexBytes]():
		return "hex value"
	case reflect.TypeFor[Base64Bytes]():
		return "base64 value"
	case reflect.TypeFor[Glob]():
		return "glob pattern"
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
// allowed) or in milliseconds, each with an optional leading "-" and never a "+".
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
		t, ok := parseUnixSeconds(s)
		if !ok {
			return time.Time{}, errors.New("want a Unix timestamp in seconds, e.g. 1759104000")
		}
		return t, nil
	case layoutUnixMilli:
		ms, err := strconv.ParseInt(s, 10, 64)
		if err != nil || strings.HasPrefix(s, "+") {
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

// parseUnixSeconds reads a Unix timestamp in seconds written as [-]digits[.digits], the
// fraction rounded to the nanosecond. ok is false for any other text and for seconds outside
// int64.
func parseUnixSeconds(s string) (time.Time, bool) {
	digits := strings.TrimPrefix(s, "-")
	whole, frac, _ := strings.Cut(digits, ".")
	if whole == "" || skipDigits(whole, 0) != len(whole) || skipDigits(frac, 0) != len(frac) ||
		(strings.Contains(digits, ".") && frac == "") {
		return time.Time{}, false
	}
	secs, err := strconv.ParseInt(whole, 10, 64)
	if err != nil {
		return time.Time{}, false
	}
	var nanos int64
	if frac != "" {
		f, err := strconv.ParseFloat("0."+frac, 64)
		if err != nil {
			return time.Time{}, false
		}
		nanos = int64(math.Round(f * 1e9))
	}
	if len(digits) < len(s) {
		secs, nanos = -secs, -nanos
	}
	return time.Unix(secs, nanos).UTC(), true
}

// layoutLabel names a layout's kind of value for an error message.
func layoutLabel(layout string) string {
	if layout == layoutDate {
		return "date"
	}
	return "time"
}

var timeType = reflect.TypeFor[time.Time]()

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

// expandDays rewrites each day and week component of a duration as hours. A component is a
// decimal number (see [scanDecimal]) directly followed by "d" or "w"; components are found
// left to right, each starting at the leftmost place one can. A number too large to read is
// left as written and its error returned.
func expandDays(s string) (string, error) {
	var b strings.Builder
	var convErr error
	for i := 0; i < len(s); {
		end, ok := dayComponent(s, i)
		if !ok {
			b.WriteByte(s[i])
			i++
			continue
		}
		part := s[i : end+1]
		n, err := strconv.ParseFloat(s[i:end], 64)
		if err != nil {
			convErr = err
			b.WriteString(part)
		} else {
			hours := n * 24
			if s[end] == 'w' {
				hours = n * 24 * 7
			}
			b.WriteString(strconv.FormatFloat(hours, 'f', -1, 64))
			b.WriteByte('h')
		}
		i = end + 1
	}
	return b.String(), convErr
}

// dayComponent reports whether a day or week component starts at s[i], and the index of its
// unit letter. A fraction that is not followed by a unit leaves the digits before its "." to
// stand alone, as "2d" in "1.2d" does not: there the fraction belongs to the number.
func dayComponent(s string, i int) (int, bool) {
	j := skipDigits(s, i)
	if j < len(s) && s[j] == '.' {
		if k := skipDigits(s, j+1); k > j+1 && k < len(s) && (s[k] == 'd' || s[k] == 'w') {
			return k, true
		}
	}
	if j > i && j < len(s) && (s[j] == 'd' || s[j] == 'w') {
		return j, true
	}
	return 0, false
}

// isPortOpaque reports whether a URL's opaque part reads as a port, optionally followed by a
// path: one to five digits up to 65535, then nothing or "/…". A longer number, as in
// tel:5551234, is not a port.
func isPortOpaque(opaque string) bool {
	port, _, _ := strings.Cut(opaque, "/")
	if len(port) > 5 || !isAllDigits(port) {
		return false
	}
	n, err := strconv.Atoi(port)
	return err == nil && n <= 65535
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
