package codegen

import "testing"

// The suggestion helpers give spec authors "did you mean" hints at validate time. They are
// separate from the runtime's opt-in, end-user-facing Suggestor.

func TestLevenshtein(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"abc", "abc", 0},
		{"", "abc", 3},
		{"abc", "", 3},
		{"kitten", "sitting", 3}, // the canonical example
		{"flag", "flags", 1},     // insertion
		{"verbose", "verbse", 1}, // deletion
		{"color", "colour", 1},
	}
	for _, tc := range cases {
		if got := levenshtein(tc.a, tc.b); got != tc.want {
			t.Errorf("levenshtein(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
	// Edit distance is symmetric.
	if levenshtein("deploy", "delpoy") != levenshtein("delpoy", "deploy") {
		t.Error("levenshtein is not symmetric")
	}
}

func TestClosestName(t *testing.T) {
	candidates := []string{"verbose", "version", "deploy"}
	cases := []struct{ target, want string }{
		{"verbse", "verbose"},  // distance 1
		{"versoin", "version"}, // transposition, distance 2
		{"deploy", "deploy"},   // exact
		{"xyzzy", ""},          // nothing within distance 2
		{"", ""},               // no target
	}
	for _, tc := range cases {
		if got := closestName(tc.target, candidates); got != tc.want {
			t.Errorf("closestName(%q) = %q, want %q", tc.target, got, tc.want)
		}
	}
	if got := closestName("anything", nil); got != "" {
		t.Errorf("closestName with no candidates = %q, want empty", got)
	}
}

func TestDidYouMean(t *testing.T) {
	const msg = `unknown flag "verbse"`
	got := didYouMean(msg, "verbse", []string{"verbose", "version"})
	if want := msg + `; did you mean "verbose"?`; got != want {
		t.Errorf("didYouMean = %q, want %q", got, want)
	}
	// No near match: the message is returned untouched rather than guessing.
	if got := didYouMean(msg, "xyzzy", []string{"verbose"}); got != msg {
		t.Errorf("didYouMean with no near match = %q, want the message unchanged", got)
	}
}
