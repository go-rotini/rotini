package tortellini

import (
	"math"
	"testing"
)

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
