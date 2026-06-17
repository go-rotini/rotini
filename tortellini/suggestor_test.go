package tortellini

import (
	"math"
	"reflect"
	"strings"
	"testing"
)

func TestSuggest(t *testing.T) {
	s := NewSuggestor() // Levenshtein, minScore 0.6, maxResults 3
	cases := []struct {
		name       string
		input      string
		candidates []string
		want       []string
	}{
		{"close typo", "generte", []string{"generate", "validate", "init"}, []string{"generate"}},
		{"nearest first", "validte", []string{"validate", "generate"}, []string{"validate"}},
		{"weak candidate below the cutoff is dropped", "ru", []string{"run", "r"}, []string{"run"}},
		{"prefix breaks score ties", "abc", []string{"axc", "abx"}, []string{"abx", "axc"}},
		{"score+prefix ties break lexicographically", "stat", []string{"star", "stab"}, []string{"stab", "star"}},
		{"exact match suggests nothing", "run", []string{"run", "ru"}, nil},
		{"nothing close enough", "completely-wrong", []string{"generate", "validate"}, nil},
		{"empty input suggests nothing", "", []string{"generate"}, nil},
		{"duplicates and empties are dropped", "geneate", []string{"generate", "generate", ""}, []string{"generate"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := s.Suggest(tc.input, tc.candidates); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Suggest(%q, %v) = %v, want %v", tc.input, tc.candidates, got, tc.want)
			}
		})
	}
}

func TestSuggest_tuning(t *testing.T) {
	// A lower min-score admits farther candidates; a results cap trims the list.
	// The With* tuners are chainable receiver methods.
	wide := NewSuggestor().WithAlgo(SuggestAlgoLevenshtein).WithMinScore(0.2).WithMaxResults(2)
	got := wide.Suggest("dep", []string{"deploy", "delete", "describe", "drain"})
	if len(got) != 2 {
		t.Fatalf("Suggest with WithMaxResults(2) = %v, want 2 hits", got)
	}
	// The default min-score (0.6) excludes a far candidate the wide one admitted.
	if got := NewSuggestor().Suggest("dep", []string{"describe"}); got != nil {
		t.Errorf("default min-score admitted %v, want nil", got)
	}
	// Out-of-range / non-positive tuner values are ignored, keeping the defaults.
	if s := NewSuggestor().WithMinScore(2).WithMaxResults(-1); s.minScore != defaultMinScore || s.maxResults != 3 {
		t.Errorf("invalid tuners changed defaults: %+v", s)
	}
}

func TestSuggest_algo(t *testing.T) {
	if def := NewSuggestor(); def.algo != SuggestAlgoLevenshtein {
		t.Errorf("default algo = %q, want %q", def.algo, SuggestAlgoLevenshtein)
	}
	// A recognized algorithm is selected.
	if s := NewSuggestor().WithAlgo(SuggestAlgoJaroWinkler); s.algo != SuggestAlgoJaroWinkler {
		t.Errorf("WithAlgo(JaroWinkler) algo = %q, want it set", s.algo)
	}
	// An unrecognized algorithm is ignored — the current (default) one stays.
	if s := NewSuggestor().WithAlgo("nonsense"); s.algo != SuggestAlgoLevenshtein {
		t.Errorf("WithAlgo(unknown) set algo to %q, want it ignored", s.algo)
	}
	// Jaro-Winkler favors a shared-prefix typo end-to-end.
	got := NewSuggestor().WithAlgo(SuggestAlgoJaroWinkler).Suggest("deploi", []string{"deploy", "delete"})
	if len(got) == 0 || got[0] != "deploy" {
		t.Errorf("JaroWinkler Suggest(deploi) = %v, want [deploy ...]", got)
	}
}

func TestMatches_scored(t *testing.T) {
	// Matches returns the original candidate + its score, deduped.
	ms := NewSuggestor().Matches("generte", []string{"generate", "validate", "generate"})
	if len(ms) != 1 || ms[0].Value != "generate" || !(ms[0].Score > 0.5 && ms[0].Score < 1) {
		t.Fatalf("Matches = %+v, want one {generate, 0.5<score<1}", ms)
	}
	// Matches INCLUDES an exact match (score 1); Suggest does not.
	if ms := NewSuggestor().Matches("run", []string{"run", "ran"}); len(ms) == 0 || ms[0].Value != "run" || !approx(ms[0].Score, 1) {
		t.Errorf("Matches with exact = %+v, want exact first at score 1", ms)
	}
	if got := NewSuggestor().Suggest("run", []string{"run", "ran"}); got != nil {
		t.Errorf("Suggest with exact = %v, want nil", got)
	}
}

func TestSuggest_caseFold(t *testing.T) {
	cf := NewSuggestor().WithCaseFold()
	if got := cf.Score("Deploy", "deploy"); !approx(got, 1) {
		t.Errorf("case-folded Score(Deploy,deploy) = %.3f, want 1", got)
	}
	if got := NewSuggestor().Score("Deploy", "deploy"); approx(got, 1) {
		t.Errorf("case-sensitive Score(Deploy,deploy) = %.3f, want < 1", got)
	}
	// Folding applies to candidates too; the ORIGINAL casing is returned.
	if got := cf.Suggest("DEPLOI", []string{"Deploy", "Delete"}); len(got) == 0 || got[0] != "Deploy" {
		t.Errorf("case-folded Suggest = %v, want [Deploy ...]", got)
	}
}

func TestSuggest_normalizer(t *testing.T) {
	// A normalizer that strips a leading "--" lets flag-style tokens match by name.
	s := NewSuggestor().WithNormalizer(func(in string) string { return strings.TrimPrefix(in, "--") })
	if got := s.Suggest("--colour", []string{"--color", "--verbose"}); len(got) == 0 || got[0] != "--color" {
		t.Errorf("normalized Suggest = %v, want [--color ...]", got)
	}
}

func TestSuggest_nilReceiver(t *testing.T) {
	var s *Suggestor
	if got := s.Suggest("x", []string{"y"}); got != nil {
		t.Errorf("nil Suggestor.Suggest = %v, want nil", got)
	}
	if got := s.Matches("x", []string{"y"}); got != nil {
		t.Errorf("nil Suggestor.Matches = %v, want nil", got)
	}
	if got := s.Score("x", "y"); got != 0 {
		t.Errorf("nil Suggestor.Score = %v, want 0", got)
	}
}
func TestEditDistances(t *testing.T) {
	cases := []struct {
		name string
		fn   func(a, b string) int
		a, b string
		want int
	}{
		{"levenshtein kitten/sitting", Levenshtein, "kitten", "sitting", 3},
		{"levenshtein flaw/lawn", Levenshtein, "flaw", "lawn", 2},
		{"levenshtein identical", Levenshtein, "abc", "abc", 0},
		{"levenshtein empty", Levenshtein, "", "abc", 3},
		{"levenshtein transpose ab/ba", Levenshtein, "ab", "ba", 2},
		{"osa transpose ab/ba", OSA, "ab", "ba", 1},
		{"osa ca/abc", OSA, "ca", "abc", 3},
		{"damerau transpose ab/ba", DamerauLevenshtein, "ab", "ba", 1},
		{"damerau ca/abc beats osa", DamerauLevenshtein, "ca", "abc", 2},
		{"damerau identical", DamerauLevenshtein, "abc", "abc", 0},
		{"lcs classic", LCS, "ABCBDAB", "BDCAB", 4},
		{"lcs none", LCS, "abc", "def", 0},
		{"lcs identical", LCS, "abc", "abc", 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.fn(tc.a, tc.b); got != tc.want {
				t.Errorf("%s(%q,%q) = %d, want %d", tc.name, tc.a, tc.b, got, tc.want)
			}
		})
	}
}

func TestHamming(t *testing.T) {
	if d, ok := Hamming("karolin", "kathrin"); !ok || d != 3 {
		t.Errorf("Hamming(karolin,kathrin) = (%d,%v), want (3,true)", d, ok)
	}
	if d, ok := Hamming("abc", "abd"); !ok || d != 1 {
		t.Errorf("Hamming(abc,abd) = (%d,%v), want (1,true)", d, ok)
	}
	if _, ok := Hamming("abc", "ab"); ok {
		t.Error("Hamming on unequal lengths returned ok=true, want false")
	}
}

func approx(got, want float64) bool { return math.Abs(got-want) < 1e-3 }

func TestSimilarityMetrics(t *testing.T) {
	cases := []struct {
		name string
		fn   func(a, b string) float64
		a, b string
		want float64
	}{
		{"jaro MARTHA/MARHTA", Jaro, "MARTHA", "MARHTA", 0.9444},
		{"jaro DIXON/DICKSONX", Jaro, "DIXON", "DICKSONX", 0.7667},
		{"jaro identical", Jaro, "abc", "abc", 1},
		{"jaro empty both", Jaro, "", "", 1},
		{"jaro one empty", Jaro, "abc", "", 0},
		{"jaro-winkler MARTHA/MARHTA", JaroWinkler, "MARTHA", "MARHTA", 0.9611},
		{"jaro-winkler DIXON/DICKSONX", JaroWinkler, "DIXON", "DICKSONX", 0.8133},
		{"dice night/nacht", SorensenDice, "night", "nacht", 0.25},
		{"dice identical", SorensenDice, "abc", "abc", 1},
		{"jaccard night/nacht", Jaccard, "night", "nacht", 0.1429},
		{"jaccard identical", Jaccard, "abc", "abc", 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.fn(tc.a, tc.b); !approx(got, tc.want) {
				t.Errorf("%s(%q,%q) = %.4f, want ~%.4f", tc.name, tc.a, tc.b, got, tc.want)
			}
		})
	}
}

func TestSimilarity_normalizesAllAlgosToUnitInterval(t *testing.T) {
	algos := []SuggestAlgo{
		SuggestAlgoLevenshtein, SuggestAlgoDamerauLevenshtein, SuggestAlgoOSA,
		SuggestAlgoHamming, SuggestAlgoLCS, SuggestAlgoJaro, SuggestAlgoJaroWinkler,
		SuggestAlgoSorensenDice, SuggestAlgoJaccard,
	}
	for _, algo := range algos {
		// identical → 1
		if got := Similarity("deploy", "deploy", algo); !approx(got, 1) {
			t.Errorf("Similarity(identical, %s) = %.4f, want 1", algo, got)
		}
		// a close typo scores high; an unrelated word scores low — across the board.
		near := Similarity("deploy", "deplyo", algo) // adjacent transposition
		far := Similarity("deploy", "xyzzy", algo)
		if near < far {
			t.Errorf("%s: near typo (%.3f) scored below unrelated (%.3f)", algo, near, far)
		}
		// stays in [0,1]
		for _, v := range []float64{near, far} {
			if v < 0 || v > 1 {
				t.Errorf("%s: score %.3f out of [0,1]", algo, v)
			}
		}
	}
}
