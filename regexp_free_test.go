package rotini

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Reference regular expressions for StripANSI, day/week durations and byte sizes. The
// hand-written parsers must accept and produce exactly what these do.
var (
	regexpANSI      = regexp.MustCompile(`\x1b\[[\x30-\x3f]*[\x20-\x2f]*[\x40-\x7e]|\x1b[\]PX^_][^\x07\x1b]*(?:\x07|\x1b\\)|\x1b(?:[\x20-\x2f]+[\x30-\x7e]|[\x30-\x4f\x51-\x57\x59\x5a\x5c\x60-\x7e])`)
	regexpDays      = regexp.MustCompile(`(\d*\.?\d+)([dw])`)
	regexpByteSize  = regexp.MustCompile(`^\s*(\d*\.?\d+)\s*([a-zA-Z]*)\s*$`)
	regexpSizeUnits = map[string]float64{
		"": 1, "b": 1,
		"k": 1e3, "kb": 1e3, "m": 1e6, "mb": 1e6, "g": 1e9, "gb": 1e9,
		"t": 1e12, "tb": 1e12, "p": 1e15, "pb": 1e15, "e": 1e18, "eb": 1e18,
		"ki": 1 << 10, "kib": 1 << 10, "mi": 1 << 20, "mib": 1 << 20, "gi": 1 << 30, "gib": 1 << 30,
		"ti": 1 << 40, "tib": 1 << 40, "pi": 1 << 50, "pib": 1 << 50, "ei": 1 << 60, "eib": 1 << 60,
	}
)

func regexpStripANSI(text string) string {
	if !strings.ContainsRune(text, esc) {
		return text
	}
	return regexpANSI.ReplaceAllString(text, "")
}

func regexpExpandDays(s string) (string, error) {
	var convErr error
	expanded := regexpDays.ReplaceAllStringFunc(s, func(part string) string {
		m := regexpDays.FindStringSubmatch(part)
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

func regexpParseDuration(s string) (time.Duration, error) {
	expanded := s
	if strings.ContainsAny(s, "dw") {
		var err error
		if expanded, err = regexpExpandDays(s); err != nil {
			return 0, err
		}
	}
	d, err := time.ParseDuration(expanded)
	if err != nil {
		return 0, fmt.Errorf("%q is not a duration such as 90s, 1h30m or 7d: %w", s, err)
	}
	return d, nil
}

func regexpParseByteSize(s string) (int64, error) {
	m := regexpByteSize.FindStringSubmatch(s)
	if m == nil {
		return 0, fmt.Errorf("%q is not a size; write a number with an optional unit, e.g. 512Mi, 10MB, 1.5GiB", s)
	}
	mult, ok := regexpSizeUnits[strings.ToLower(m[2])]
	if !ok {
		return 0, fmt.Errorf("%q has an unknown unit %q; use B, K/KB, M/MB, G/GB, T/TB, P/PB, E/EB (decimal) or Ki, Mi, Gi, Ti, Pi, Ei (binary)", s, m[2])
	}
	return byteCount(s, m[1], mult) // the number's conversion is shared; this pins the grammar
}

// errText is err's message, "" for nil.
func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

var ansiSeeds = []string{
	"plain", "\x1b[1mbold\x1b[0m", "\x1b[38;5;196mred\x1b[m", "\x1b]8;;https://x.dev\x07link\x1b]8;;\x07",
	"\x1b]8;;u\x1b\\t\x1b]8;;\x1b\\", "\x1b", "a\x1b", "\x1b[", "\x1b[1", "\x1b[1;2 ", "\x1b[?25h", "\x1b[ !\"/@",
	"\x1b]", "\x1b]no end", "\x1b]a\x1bb", "\x1b]a\x1b", "\x1b\x1b[1m", "\x1b[\x1b[1mx", "\x1b]\xff\xfe\x07",
	"\x1b[1\xffm", "é\x1b[1mé", "\x1b[:;?0~", "\x1b[0\x7f", "\x1b]\x1b\\", "\x1bX", "\x1b[1;2\x1b]x\x07",
	"\x1b(Bplain\x1b[m", "\x1b[>4;2mx", "\x1b[=5hx", "\x1b[<1;2Mx", "\x1bcx", "\x1b7x\x1b8", "\x1b(", "\x1b([x",
	"\x1bPq#0\x1b\\x", "\x1b_app\x07x", "\x1b^pm\x1b\\x", "\x1bXsos\x07x", "\x1b\x1f", "\x1b\x7f", "\x1b\\x",
}

func TestStripANSI_matchesRegexp(t *testing.T) {
	for _, s := range ansiSeeds {
		if got, want := StripANSI(s), regexpStripANSI(s); got != want {
			t.Errorf("StripANSI(%q) = %q, want %q", s, got, want)
		}
	}
}

func FuzzStripANSI(f *testing.F) {
	for _, s := range ansiSeeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if got, want := StripANSI(s), regexpStripANSI(s); got != want {
			t.Fatalf("StripANSI(%q) = %q, want %q", s, got, want)
		}
	})
}

var durationSeeds = []string{
	"7d", "1.5w", "1d12h", "2w3d", "1.5d", ".5d", "1.d", "1.2.3d", "d", "w", "1dd", "0d", "00.50d",
	"-1d", "+2w", "1d-2h", "12x3d", "123.d4d", "1..5d", "1.5.d", "3wd", "1e3d", "1_000d", "１d",
	strings.Repeat("9", 400) + "d", "1" + strings.Repeat("0", 400) + "d2" + strings.Repeat("0", 400) + "w",
	"1h30m", "90s", "", "dw", "5 d", "1d 2h", "0.000000001d", "9223372036854775807d",
}

func TestParseDuration_matchesRegexp(t *testing.T) {
	for _, s := range durationSeeds {
		checkDurationParity(t, s)
	}
}

func checkDurationParity(t *testing.T, s string) {
	t.Helper()
	got, gotErr := parseDuration(s)
	want, wantErr := regexpParseDuration(s)
	if got != want || errText(gotErr) != errText(wantErr) {
		t.Errorf("parseDuration(%q) = %v, %v; want %v, %v", s, got, gotErr, want, wantErr)
	}
	gotExp, gotErr := expandDays(s)
	wantExp, wantErr := regexpExpandDays(s)
	if gotExp != wantExp || errText(gotErr) != errText(wantErr) {
		t.Errorf("expandDays(%q) = %q, %v; want %q, %v", s, gotExp, gotErr, wantExp, wantErr)
	}
}

func FuzzDuration(f *testing.F) {
	for _, s := range durationSeeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		checkDurationParity(t, s)
	})
}

var byteSizeSeeds = []string{
	"512Mi", "10MB", "1.5GiB", "4096", " 1 k ", "\t2\nkb\r", "\f3\f", "\v3", " 3", "3 ", "1.", ".5k",
	"1.5.", "5Mi!", "5 k b", "", " ", "k", "1e3", "-1", "+1", "1,5", "12XB", "1Ki", "8Ei", "16Ei",
	strings.Repeat("9", 400), "1" + strings.Repeat("0", 400) + "k", "0.0000001", "١٢", "1Ｋ", "1 KiB ",
	"9223372036854775807", "9223372036854775808",
}

func TestParseByteSize_matchesRegexp(t *testing.T) {
	for _, s := range byteSizeSeeds {
		checkByteSizeParity(t, s)
	}
}

func checkByteSizeParity(t *testing.T, s string) {
	t.Helper()
	got, gotErr := parseByteSize(s)
	want, wantErr := regexpParseByteSize(s)
	if got != want || errText(gotErr) != errText(wantErr) {
		t.Errorf("parseByteSize(%q) = %v, %v; want %v, %v", s, got, gotErr, want, wantErr)
	}
}

func FuzzByteSize(f *testing.F) {
	for _, s := range byteSizeSeeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		checkByteSizeParity(t, s)
	})
}

// TestByteSizeUnit_matchesTable pins the unit switch to the table it replaced.
func TestByteSizeUnit_matchesTable(t *testing.T) {
	for unit, want := range regexpSizeUnits {
		if got, ok := byteSizeUnit(unit); !ok || got != want {
			t.Errorf("byteSizeUnit(%q) = %v, %v; want %v", unit, got, ok, want)
		}
	}
	for _, unit := range []string{"x", "kk", "kibb", "K", "bi", "i"} {
		if _, ok := byteSizeUnit(unit); ok {
			t.Errorf("byteSizeUnit(%q) is a unit", unit)
		}
	}
}
