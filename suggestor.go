package rotini

import (
	"errors"
	"slices"
	"sort"
	"strings"
)

// defaultMinScore is the cutoff a [Suggestor] keeps candidates at or above. 0.75 is the highest
// threshold that tolerates one edit in a four-character word (`hlep` → `help`); lower values
// start suggesting unrelated words, and three-character commands are deliberately left
// unmatched.
const defaultMinScore = 0.75

// Suggestor ranks a possibly-mistyped token against a list of candidates by optimal string
// alignment distance. It prints nothing and holds no state beyond its configuration. rotini
// never suggests on its own; a program opts in by calling a Suggestor, typically from its
// reporter with [Suggestor.For]:
//
//	var suggestor = rotini.NewSuggestor()
//
//	func reporter(_ context.Context, rtx *rotini.Context, out rotini.Outcome) {
//		for _, err := range out.Errors {
//			fmt.Fprintf(rtx.Stderr, "Error: %s\n", err)
//			if hits := suggestor.For(err); len(hits) > 0 {
//				fmt.Fprintf(rtx.Stderr, "Did you mean %q?\n", hits[0])
//			}
//		}
//	}
//
// Matching is always case-insensitive. A configured Suggestor is safe for concurrent use;
// finish configuring it before sharing it across goroutines.
type Suggestor struct {
	minScore   float64
	maxResults int
}

// NewSuggestor returns a [Suggestor] with a minimum score of 0.75 and at most 3 results. The
// With methods return the receiver, so they chain. The zero value is not usable.
func NewSuggestor() *Suggestor {
	return &Suggestor{minScore: defaultMinScore, maxResults: 3}
}

// WithMinScore sets the similarity a candidate must reach to be offered, in [0,1]. A value
// outside that range is ignored. Below about 0.7 the ranker starts offering unrelated words;
// above 0.75 it no longer matches one-edit typos of four-character commands.
func (s *Suggestor) WithMinScore(score float64) *Suggestor {
	if score >= 0 && score <= 1 {
		s.minScore = score
	}
	return s
}

// WithMaxResults caps how many suggestions [Suggestor.Suggest] and [Suggestor.For] return
// (default 3). Zero or less means no cap.
func (s *Suggestor) WithMaxResults(n int) *Suggestor {
	s.maxResults = n
	return s
}

// For returns the suggestions for the token a [*ParseError] rejected, ranked against its
// Candidates, nearest first. It returns nil for an error that is not a [*ParseError], one
// carrying no Token or no Candidates, and one whose token is near nothing.
func (s *Suggestor) For(err error) []string {
	if s == nil {
		return nil
	}
	var pe *ParseError
	if !errors.As(err, &pe) || pe.Token == "" || len(pe.Candidates) == 0 {
		return nil
	}
	return s.Suggest(pe.Token, pe.Candidates)
}

// Suggest ranks candidates by nearness to input, nearest first, keeping those at or above the
// minimum score and at most the configured number of results.
//
// An input that matches a candidate byte-for-byte yields nil; a case-only difference
// ("--VERBOSE" for "--verbose") is treated as a typo.
//
// Ties prefer the candidate sharing the longer common prefix with input, then break
// lexicographically, so the result is deterministic. Empty candidates are skipped and
// duplicates collapse to the first occurrence.
func (s *Suggestor) Suggest(input string, candidates []string) []string {
	if s == nil {
		return nil
	}
	normalizedInput := fold(input)
	if normalizedInput == "" {
		return nil
	}
	// Compare raw strings, not folded ones: "--VERBOSE" folds onto "--verbose" but was
	// rejected, so it still deserves a suggestion.
	if slices.Contains(candidates, input) {
		return nil
	}

	type hit struct {
		value     string
		score     float64
		prefixLen int
	}
	hits := make([]hit, 0, len(candidates))
	seen := make(map[string]bool, len(candidates))
	for _, candidate := range candidates {
		normalized := fold(candidate)
		if normalized == "" || seen[normalized] {
			continue
		}
		seen[normalized] = true
		score := nearness(normalizedInput, normalized)
		if score >= s.minScore {
			hits = append(hits, hit{
				value:     candidate,
				score:     score,
				prefixLen: commonPrefixLength(normalizedInput, normalized),
			})
		}
	}
	if len(hits) == 0 {
		return nil
	}

	sort.Slice(hits, func(i, j int) bool {
		if hits[i].score != hits[j].score {
			return hits[i].score > hits[j].score
		}
		if hits[i].prefixLen != hits[j].prefixLen {
			return hits[i].prefixLen > hits[j].prefixLen
		}
		return hits[i].value < hits[j].value
	})
	if s.maxResults > 0 && len(hits) > s.maxResults {
		hits = hits[:s.maxResults]
	}

	out := make([]string, len(hits))
	for i, h := range hits {
		out[i] = h.value
	}
	return out
}

// Closest returns the single best suggestion for input, or ok=false when input exactly matches
// a candidate or nothing clears the minimum score.
func (s *Suggestor) Closest(input string, candidates []string) (suggestion string, ok bool) {
	if hits := s.Suggest(input, candidates); len(hits) > 0 {
		return hits[0], true
	}
	return "", false
}

// fold normalizes a token for comparison by lowercasing it; no other normalization is applied.
func fold(text string) string { return strings.ToLower(text) }

// commonPrefixLength is the length (in runes) of the longest common prefix of a and b.
func commonPrefixLength(a, b string) int {
	aRunes, bRunes := []rune(a), []rune(b)
	length := 0
	for length < len(aRunes) && length < len(bRunes) && aRunes[length] == bRunes[length] {
		length++
	}
	return length
}

// nearness scores a and b in [0,1], 1 being identical, by optimal string alignment distance
// normalized against the longer of the two.
func nearness(a, b string) float64 {
	length := max(runeLength(a), runeLength(b))
	if length == 0 {
		return 1
	}
	return 1 - float64(optimalStringAlignment(a, b))/float64(length)
}

// runeLength is the length of s in runes.
func runeLength(s string) int { return len([]rune(s)) }

// optimalStringAlignment returns the Optimal String Alignment distance (restricted
// Damerau–Levenshtein): single-rune insertions, deletions and substitutions, plus transposition
// of two adjacent runes, with no substring edited more than once. Counting a transposition as
// one edit ("isntall" → "install") is what distinguishes it from Levenshtein.
func optimalStringAlignment(a, b string) int {
	aRunes, bRunes := []rune(a), []rune(b)
	lenA, lenB := len(aRunes), len(bRunes)
	if lenA == 0 {
		return lenB
	}
	if lenB == 0 {
		return lenA
	}
	matrix := make([][]int, lenA+1)
	for i := range lenA + 1 {
		matrix[i] = make([]int, lenB+1)
		matrix[i][0] = i
	}
	for j := range lenB + 1 {
		matrix[0][j] = j
	}
	for i := 1; i <= lenA; i++ {
		for j := 1; j <= lenB; j++ {
			cost := 1
			if aRunes[i-1] == bRunes[j-1] {
				cost = 0
			}
			matrix[i][j] = min(matrix[i-1][j]+1, matrix[i][j-1]+1, matrix[i-1][j-1]+cost)
			if i > 1 && j > 1 && aRunes[i-1] == bRunes[j-2] && aRunes[i-2] == bRunes[j-1] {
				matrix[i][j] = min(matrix[i][j], matrix[i-2][j-2]+1)
			}
		}
	}
	return matrix[lenA][lenB]
}
