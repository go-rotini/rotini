package rotini

import (
	"io"
	"math"
	"testing"
	"time"
)

// A size at or past 2^63 bytes is too large on every architecture, and whole numbers stay
// exact rather than rounding through float64.
func TestByteSize_range(t *testing.T) {
	for _, s := range []string{"8Ei", "8EiB", "9223372036854775808", "9223372036854775809", "18446744073709551616", "8.0Ei", "9.3E"} {
		if n, err := parseByteSize(s); err == nil {
			t.Errorf("parseByteSize(%q) = %d, want too large", s, n)
		}
	}
	for s, want := range map[string]int64{
		"9223372036854775807": math.MaxInt64,
		"9007199254740993":    9007199254740993,
		"7Ei":                 7 << 60,
		"1.5Ki":               1536,
		"0.5E":                500000000000000000,
	} {
		if n, err := parseByteSize(s); err != nil || n != want {
			t.Errorf("parseByteSize(%q) = %d, %v; want %d", s, n, err, want)
		}
	}
}

// layout: unix accepts only [-]digits[.digits] seconds that fit in int64.
func TestUnixLayout_strict(t *testing.T) {
	for _, s := range []string{"NaN", "Inf", "-Inf", "1e30", "0x10", "1e3", "99999999999999999999", "1.", ".5", "-", "+5", "1.2.3"} {
		if got, err := parseTimeLayout(s, layoutUnix); err == nil {
			t.Errorf("parseTimeLayout(%q, unix) = %v, want an error", s, got)
		}
	}
	for s, want := range map[string]time.Time{
		"1759104000":     time.Unix(1759104000, 0),
		"-1":             time.Unix(-1, 0),
		"1.5":            time.Unix(1, 5e8),
		"-1.5":           time.Unix(-2, 5e8),
		"0.000000001":    time.Unix(0, 1),
		"1759104000.250": time.Unix(1759104000, 25e7),
	} {
		if got, err := parseTimeLayout(s, layoutUnix); err != nil || !got.Equal(want) {
			t.Errorf("parseTimeLayout(%q, unix) = %v, %v; want %v", s, got, err, want)
		}
	}
}

// A nil registered under an interface type reads back as registered.
func TestGetDependency_nilInterface(t *testing.T) {
	dep := NewDependency[io.Writer]("out")
	rtx := NewContextFor(Definition{Name: "app"}, nil)
	if _, ok := rtx.GetDependency(dep); ok {
		t.Fatal("unregistered dependency reads as registered")
	}
	rtx.SetDependency(dep, nil)
	if w, ok := rtx.GetDependency(dep); !ok || w != nil {
		t.Fatalf("GetDependency = %v, %v; want nil, true", w, ok)
	}
	if w := rtx.MustGetDependency(dep); w != nil {
		t.Fatalf("MustGetDependency = %v, want nil", w)
	}
	other := NewDependency[*int]("out")
	if _, ok := rtx.GetDependency(other); ok {
		t.Fatal("a nil interface reads as a *int")
	}
}
