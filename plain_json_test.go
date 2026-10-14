package rotini

import (
	"bytes"
	"encoding/json"
	"math"
	"math/rand/v2"
	"strings"
	"testing"
	"time"

	"github.com/go-rotini/recon"
)

// plainValueRef is the reference shape marshalPlain must match through json.Marshal.
func plainValueRef(v recon.Value) any {
	switch v.Kind() {
	case recon.MapKind:
		m, _ := v.AsMap()
		out := make(map[string]any, len(m))
		for k, e := range m {
			out[k] = plainValueRef(e)
		}
		return out
	case recon.SliceKind:
		items, _ := v.AsSlice()
		out := make([]any, len(items))
		for i, e := range items {
			out[i] = plainValueRef(e)
		}
		return out
	}
	return v.Any()
}

func checkPlain(t *testing.T, v recon.Value) {
	t.Helper()
	want, werr := json.Marshal(plainValueRef(v))
	got, gerr := marshalPlain(v)
	if (werr != nil) != (gerr != nil) || !bytes.Equal(got, want) {
		t.Fatalf("value %#v:\n got %q (%v)\nwant %q (%v)", v.Any(), got, gerr, want, werr)
	}
}

var plainStrings = []string{
	"", "a", "<b>&</b>", "\"q\"\\", "\x00\x01\x07\x08\x09\x0a\x0b\x0c\x0d\x1f\x7f", "é ü 世界 🎉",
	"\u2028\u2029", "bad\xffutf8", "\xc3", "\xed\xa0\x80", "\xf4\x90\x80\x80", "tab\there", "</script>",
	"\ufeff", "\ufffd", strings.Repeat("x", 300),
}

var plainScalars = []any{
	nil, true, false, int64(0), int64(-1), int64(math.MaxInt64), int64(math.MinInt64),
	uint64(math.MaxUint64), 0.0, math.Copysign(0, -1), 1.5, -2.25, 1e-6, 9.99e-7, 1e-7, 1e20, 1e21, 123456789e15,
	math.MaxFloat64, math.SmallestNonzeroFloat64, 0.1, 1.0 / 3, -1e-10, 5e-324, 1e100,
	math.NaN(), math.Inf(1), math.Inf(-1),
	time.Duration(0), 90 * time.Second, -time.Nanosecond,
	time.Date(2024, 2, 29, 12, 30, 45, 123456789, time.UTC),
	time.Date(2024, 2, 29, 12, 30, 45, 0, time.FixedZone("x", -7*3600)),
	time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(-1, 1, 1, 0, 0, 0, 0, time.UTC),
	time.Date(2024, 1, 1, 0, 0, 0, 0, time.FixedZone("s", 3600+30)),
	json.Number("12345678901234567890123"), json.Number("-99999999999999999999999"),
	recon.RawValue{Format: "yaml", Data: []byte("a: <1>")}, recon.RawValue{Format: "", Data: nil},
	recon.RawValue{Format: "x", Data: []byte{}},
}

func TestMarshalPlainMatchesEncodingJSON(t *testing.T) {
	for _, s := range plainStrings {
		checkPlain(t, recon.NewValue(map[string]any{s: s}))
		checkPlain(t, recon.NewValue([]any{s}))
	}
	for _, x := range plainScalars {
		checkPlain(t, recon.NewValue(map[string]any{"k": x}))
		checkPlain(t, recon.NewValue([]any{x, map[string]any{"n": x}}))
	}
	// Big integers recon keeps as text.
	checkPlain(t, recon.NewValue(map[string]any{"u": uint64(math.MaxUint64), "b": json.Number("1e400")}))
	checkPlain(t, recon.NewValue(map[string]any{}))
	checkPlain(t, recon.NewValue([]any{}))
	// Raw values.
	checkPlain(t, recon.NewValue(map[string]any{"r": recon.NewRawValue("json", []byte(`{"a":1}`))}))
	// Key order, including keys that sort differently by rune and by byte.
	checkPlain(t, recon.NewValue(map[string]any{"b": 1, "a": 2, "B": 3, "é": 4, "\xff": 5, "": 6, "a\x00": 7}))
}

func TestMarshalPlainDepth(t *testing.T) {
	for _, n := range []int{9998, 9999, 10000, 10001} {
		var v any = "x"
		for range n {
			v = []any{v}
		}
		checkPlain(t, recon.NewValue(map[string]any{"d": v}))
	}
}

func TestMarshalPlainRandom(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	runes := []rune{'a', 'Z', '<', '>', '&', '"', '\\', '\n', 0, 0x1f, 0x7f, 'é', '世', 0x2028, 0x2029, 0xfffd, 0x1f600}
	randString := func() string {
		var b strings.Builder
		for range r.IntN(8) {
			if r.IntN(10) == 0 {
				b.WriteByte(byte(0x80 + r.IntN(0x80)))
				continue
			}
			b.WriteRune(runes[r.IntN(len(runes))])
		}
		return b.String()
	}
	var gen func(depth int) any
	gen = func(depth int) any {
		switch k := r.IntN(10); {
		case k == 0 && depth < 4:
			m := map[string]any{}
			for range r.IntN(5) {
				m[randString()] = gen(depth + 1)
			}
			return m
		case k == 1 && depth < 4:
			s := make([]any, r.IntN(5))
			for i := range s {
				s[i] = gen(depth + 1)
			}
			return s
		case k == 2:
			return math.Float64frombits(r.Uint64())
		case k == 3:
			return r.NormFloat64() * math.Pow(10, float64(r.IntN(60)-30))
		case k == 4:
			return int64(r.Uint64())
		case k == 5:
			return randString()
		case k == 6:
			return time.Unix(r.Int64N(1<<40)-1<<39, r.Int64N(1e9)).In(time.FixedZone("z", r.IntN(50000)-25000))
		case k == 7:
			return r.IntN(2) == 0
		case k == 8:
			return float64(r.Int64N(1<<53)) / float64(int64(1)<<r.IntN(60))
		default:
			return nil
		}
	}
	for range 200000 {
		checkPlain(t, recon.NewValue(map[string]any{randString(): gen(0)}))
	}
}

func FuzzMarshalPlain(f *testing.F) {
	for _, s := range plainStrings {
		f.Add(s, 1.5, int64(3))
	}
	f.Fuzz(func(t *testing.T, s string, x float64, n int64) {
		checkPlain(t, recon.NewValue(map[string]any{s: []any{s, x, n, map[string]any{s: x}}}))
	})
}
