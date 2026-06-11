package rotini

import (
	"reflect"
	"testing"
)

func TestSuggest(t *testing.T) {
	s := NewSuggestor()
	cases := []struct {
		name       string
		input      string
		candidates []string
		want       []string
	}{
		{"close typo", "generte", []string{"generate", "validate", "init"}, []string{"generate"}},
		{"nearest first", "validte", []string{"validate", "generate"}, []string{"validate"}},
		{"distance ties prefer the longer common prefix", "ru", []string{"r", "run"}, []string{"run", "r"}},
		{"remaining ties break lexicographically", "stat", []string{"star", "stab"}, []string{"stab", "star"}},
		{"exact match suggests nothing", "run", []string{"run", "ru"}, nil},
		{"nothing within distance", "completely-wrong", []string{"generate", "validate"}, nil},
		{"empty input suggests nothing", "", []string{"generate"}, nil},
		{"duplicates and empties are dropped", "geneate", []string{"generate", "generate", ""}, []string{"generate"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := s.Suggest(tc.input, tc.candidates)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Suggest(%q, %v) = %v, want %v", tc.input, tc.candidates, got, tc.want)
			}
		})
	}
}

func TestSuggest_options(t *testing.T) {
	// A wider distance admits farther candidates; a results cap trims the list.
	wide := NewSuggestor(WithMaxDistance(5), WithMaxResults(2))
	got := wide.Suggest("dep", []string{"deploy", "delete", "describe", "drain"})
	if len(got) != 2 {
		t.Fatalf("Suggest with MaxResults(2) = %v, want 2 hits", got)
	}

	// The default distance (2) excludes what the wide one admitted.
	if got := NewSuggestor().Suggest("dep", []string{"describe"}); got != nil {
		t.Errorf("default distance admitted %v, want nil", got)
	}

	// Non-positive option values are ignored, keeping the defaults.
	if s := NewSuggestor(WithMaxDistance(0), WithMaxResults(-1)); s.maxDistance != 2 || s.maxResults != 3 {
		t.Errorf("non-positive options changed defaults: %+v", s)
	}
}

func TestSuggest_nilReceiver(t *testing.T) {
	var s *Suggestor
	if got := s.Suggest("x", []string{"y"}); got != nil {
		t.Errorf("nil Suggestor.Suggest = %v, want nil", got)
	}
}
