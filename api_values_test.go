package rotini

import (
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"testing"
)

// TestObjectFlag_deepFreeFormValuesTypeLikeJSON pins that a key=value pair reaching two or more
// levels into a free-form value types it as JSON would, as one level does: m.a.b=5 and
// {"m":{"a":{"b":5}}} hold the same number.
func TestObjectFlag_deepFreeFormValuesTypeLikeJSON(t *testing.T) {
	type patch struct {
		M    map[string]any `json:"m"`
		Root any            `json:"root"`
	}
	fromPairs, err := decodeObject(allFeatures(), "m.a.b=5,m.a.ok=true,m.a.s=v2,root.x.y=null", reflect.TypeFor[patch]())
	if err != nil {
		t.Fatal(err)
	}
	fromJSON, err := decodeObject(allFeatures(), `{"m":{"a":{"b":5,"ok":true,"s":"v2"}},"root":{"x":{"y":null}}}`, reflect.TypeFor[patch]())
	if err != nil {
		t.Fatal(err)
	}
	pairsJSON, _ := json.Marshal(fromPairs)
	jsonJSON, _ := json.Marshal(fromJSON)
	if string(pairsJSON) != string(jsonJSON) {
		t.Errorf("key=value gave %s, JSON gave %s", pairsJSON, jsonJSON)
	}
}

// TestValueTypes_zeroAndEmpty pins the zero values and empty text of the exported value types.
func TestValueTypes_zeroAndEmpty(t *testing.T) {
	if got := ByteSize(0).String(); got != "0" {
		t.Errorf("ByteSize(0).String() = %q", got)
	}
	var b ByteSize
	if err := b.UnmarshalText(nil); err == nil {
		t.Error("ByteSize accepted empty text")
	}
	if got := ByteSize(-1024).String(); got != "-1Ki" {
		t.Errorf("ByteSize(-1024) = %q", got)
	}
	if err := b.UnmarshalText([]byte("-1Ki")); err == nil {
		t.Error("ByteSize accepted a negative size, which its doc says it refuses")
	}
	var h HexBytes
	if h.String() != "" {
		t.Errorf("zero HexBytes = %q", h.String())
	}
	if err := h.UnmarshalText([]byte("0xABC")); err == nil {
		t.Error("HexBytes accepted an odd number of digits")
	}
	var b64 Base64Bytes
	if b64.String() != "" {
		t.Errorf("zero Base64Bytes = %q", b64.String())
	}
	for _, s := range []string{"/w==", "/w", "_w==", "_w"} { // standard and URL-safe, padded or not
		if err := b64.UnmarshalText([]byte(s)); err != nil || len(b64) != 1 || b64[0] != 0xff {
			t.Errorf("Base64Bytes(%q) = %v, %v", s, []byte(b64), err)
		}
	}
	var g Glob
	if !g.Match("") || g.Match("a") {
		t.Error("the zero Glob should match only the empty name")
	}
	if err := g.UnmarshalText([]byte("[")); err == nil {
		t.Error("Glob accepted a malformed pattern")
	}
	if Glob("[").Match("[") {
		t.Error("a malformed Glob matched; its doc says it matches nothing")
	}
	if !Glob("dir/*.go").Match("dir/main.go") || Glob("*.go").Match("dir/main.go") {
		t.Error("Glob's * should match within one slash-separated segment")
	}
}

// TestParseError_categories pins that every ParseKind's error is errors.Is-able to its category
// sentinel and errors.As-able to *ParseError through a wrap.
func TestParseError_categories(t *testing.T) {
	for k := ParseKindUnspecified; k <= ParseKindMisplacedFlag; k++ {
		err := error(&ParseError{Kind: k, Msg: "m"})
		wrapped := errors.Join(errors.New("context"), err)
		var pe *ParseError
		if !errors.As(wrapped, &pe) || pe.Kind != k {
			t.Errorf("%v: not errors.As-able", k)
		}
		want, other := ErrUsage, ErrInternal
		if k == ParseKindInternal {
			want, other = ErrInternal, ErrUsage
		}
		if !errors.Is(wrapped, want) || errors.Is(wrapped, other) {
			t.Errorf("%v: wrong category", k)
		}
		if k != ParseKindUnspecified && k.String() == "unspecified" {
			t.Errorf("%d has no label", k)
		}
	}
	if got := ParseKind(-1).String(); got != "unspecified" {
		t.Errorf("an unknown kind renders %q", got)
	}
}

// TestParse_outMustPointToStruct pins that Parse rejects a pointer to anything but a struct as
// a ParseKindInternal error, rather than binding nothing and reporting success.
func TestParse_outMustPointToStruct(t *testing.T) {
	n := 0
	var m map[string]any
	var pp *runInputs
	for _, out := range []any{&n, &m, &pp} {
		err := NewParser().Parse(NewContextFor(parserTestDef(), []string{"run", "alice"}), out)
		var pe *ParseError
		if !errors.As(err, &pe) || pe.Kind != ParseKindInternal || !errors.Is(err, ErrInternal) {
			t.Errorf("Parse(%T) = %v, want an internal *ParseError", out, err)
		}
	}
}

// TestSuggestor_nilAndBounds pins the documented tolerance: For, Suggest and Closest on a nil
// Suggestor return nothing; WithMinScore ignores values outside [0,1], NaN included;
// WithMaxResults(0) uncaps.
func TestSuggestor_nilAndBounds(t *testing.T) {
	var s *Suggestor
	if s.For(&ParseError{Token: "hlep", Candidates: []string{"help"}}) != nil || s.Suggest("hlep", []string{"help"}) != nil {
		t.Error("nil Suggestor suggested")
	}
	if _, ok := s.Closest("hlep", []string{"help"}); ok {
		t.Error("nil Suggestor Closest found one")
	}
	n := NewSuggestor().WithMinScore(math.NaN()).WithMinScore(-1).WithMinScore(2)
	if n.minScore != defaultMinScore {
		t.Errorf("min score = %v", n.minScore)
	}
	cands := []string{"aaaa", "aaab", "aaac", "aaad", "aaae"}
	if got := NewSuggestor().WithMaxResults(0).Suggest("aaaz", cands); len(got) != 5 {
		t.Errorf("uncapped = %v", got)
	}
	if got := NewSuggestor().Suggest("aaaz", cands); len(got) != 3 {
		t.Errorf("default cap = %v", got)
	}
}
