package rotini

import (
	"encoding/csv"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"
)

// csvRead is what splitCSV replaces: encoding/csv reading value's first record.
func csvRead(value string, comma rune) ([]string, error) {
	r := csv.NewReader(strings.NewReader(value))
	r.Comma = comma
	r.TrimLeadingSpace = true
	r.LazyQuotes = true
	r.FieldsPerRecord = -1
	return r.Read()
}

// sameSplit reports whether splitCSV and encoding/csv agree on value: the same items, or errors
// with the same text.
func sameSplit(t *testing.T, value string, comma rune) {
	t.Helper()
	got, gerr := splitCSV(value, comma)
	want, werr := csvRead(value, comma)
	if (gerr == nil) != (werr == nil) || (gerr != nil && gerr.Error() != werr.Error()) || !slices.Equal(got, want) {
		t.Fatalf("splitCSV(%q, %q) = %q, %v; encoding/csv = %q, %v", value, comma, got, gerr, want, werr)
	}
}

var splitCommas = []rune{',', ';', ' ', '\t', '|', 'é', ' ', ' ', 0, '"', '\r', '\n', 0xFFFD, 'a'}

func TestSplitCSV_matchesEncodingCSV(t *testing.T) {
	pieces := []string{"a", "b", ",", ";", `"`, `""`, " ", "\t", "\n", "\r", "\r\n", "é", " ", " ", "|", "\xff", "\x00", "ab c", "\v"}
	rng := rand.New(rand.NewPCG(1, 2))
	for range 300000 {
		var sb strings.Builder
		for range rng.IntN(12) {
			sb.WriteString(pieces[rng.IntN(len(pieces))])
		}
		sameSplit(t, sb.String(), splitCommas[rng.IntN(len(splitCommas))])
	}
}

func FuzzSplitCSV(f *testing.F) {
	for _, seed := range []string{"a,b", `"a,b",c`, "\"a\r\nb\"", "\"a\r", " , ,", `a"b,"c""d"e`, "\r", "\n\n x"} {
		f.Add(seed, uint8(0))
	}
	f.Fuzz(func(t *testing.T, value string, c uint8) {
		sameSplit(t, value, splitCommas[int(c)%len(splitCommas)])
	})
}

func BenchmarkSplitCSV(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		_, _ = splitCSV("blue, canary,\"a,b\"", ',')
	}
}

func BenchmarkSplitEncodingCSV(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		_, _ = csvRead("blue, canary,\"a,b\"", ',')
	}
}
