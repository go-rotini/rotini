package tortellini

import (
	"sort"
	"strings"
)

// KeySuggestor is the conventional registry key the generated main binds the
// [Suggestor] under (and handlers retrieve it by).
const KeySuggestor = "suggestor"

// defaultMinScore is the cutoff a [Suggestor] keeps candidates at or above —
// "comfortably similar" by the configured algorithm, strict enough to avoid
// suggesting merely-adjacent words.
const defaultMinScore = 0.6

// Suggestor ranks "did you mean" candidates for a mistyped token. It is a
// *service*, not framework behavior: rotini never suggests anything on its own —
// parse failures carry the offending token and its vocabulary as data (a
// rotini.ParseError's Token / Candidates), and a handler that wants suggestions
// binds a Suggestor and composes the two:
//
//	// main.go
//	cmd.Program.Bind(tortellini.KeySuggestor, tortellini.NewSuggestor()).Execute()
//
//	// a handler, after a failed Parse
//	var ue *rotini.ParseError
//	if errors.As(err, &ue) && ue.Token != "" {
//		if s, ok := rotini.Get[*tortellini.Suggestor](rtx, tortellini.KeySuggestor); ok {
//			if hits := s.Suggest(ue.Token, ue.Candidates); len(hits) > 0 {
//				fmt.Fprintf(rtx.Stderr, "Did you mean %q?\n", hits[0])
//			}
//		}
//	}
//
// It is configured fluently and ranks by a pluggable algorithm normalized to a
// similarity score (see [SuggestAlgo] / [Similarity]):
//
//	tortellini.NewSuggestor().
//		WithAlgo(tortellini.SuggestAlgoJaroWinkler).
//		WithMinScore(0.7).
//		WithMaxResults(5).
//		WithCaseFold()
//
// The Suggestor is a pure ranking function over the candidates it is handed —
// it discovers nothing, prints nothing, and knows nothing about the command
// tree. Candidates come from whatever vocabulary the caller has: a ParseError's
// Candidates, sibling names off rtx.Chain(), enum members, or any []string.
type Suggestor struct {
	algo       SuggestAlgo
	minScore   float64
	maxResults int
	caseFold   bool
	normalizer func(string) string
}

// Match is a scored suggestion returned by [Suggestor.Matches]: the ORIGINAL
// candidate Value (as passed in) and its similarity Score in [0,1] (1 = identical).
type Match struct {
	Value string
	Score float64
}

// NewSuggestor returns a [Suggestor] ready to bind under a registry key
// (conventionally [KeySuggestor]), ranking by [SuggestAlgoLevenshtein] with a
// minimum score of 0.6 and at most 3 results. Tune any of it fluently:
// NewSuggestor().WithAlgo(…).WithMinScore(…).WithMaxResults(…).WithCaseFold().
func NewSuggestor() *Suggestor {
	return &Suggestor{
		algo:       SuggestAlgoLevenshtein,
		minScore:   defaultMinScore,
		maxResults: 3,
	}
}

// WithAlgo selects the ranking algorithm (see [SuggestAlgo]) and returns the
// receiver to chain. An unrecognized algorithm is ignored. Default
// [SuggestAlgoLevenshtein].
func (s *Suggestor) WithAlgo(algo SuggestAlgo) *Suggestor {
	if algo.valid() {
		s.algo = algo
	}
	return s
}

// WithMinScore sets the smallest similarity score (in [0,1], 1 = identical) a
// candidate may have and still be suggested, and returns the receiver to chain.
// Default 0.6. A score outside [0,1] is ignored.
func (s *Suggestor) WithMinScore(score float64) *Suggestor {
	if score >= 0 && score <= 1 {
		s.minScore = score
	}
	return s
}

// WithMaxResults caps how many suggestions [Suggestor.Suggest] / [Suggestor.Matches]
// return, and returns the receiver to chain. Default 3. A non-positive n is ignored.
func (s *Suggestor) WithMaxResults(n int) *Suggestor {
	if n > 0 {
		s.maxResults = n
	}
	return s
}

// WithCaseFold makes matching case-insensitive (input and candidates are lowered
// before scoring), and returns the receiver to chain. Off by default.
func (s *Suggestor) WithCaseFold() *Suggestor {
	s.caseFold = true
	return s
}

// WithNormalizer sets a preprocessing function applied to BOTH the input and each
// candidate before scoring (e.g. trim, strip accents), and returns the receiver
// to chain. It runs before case-folding. The original candidate strings are still
// what [Suggestor.Suggest] / [Suggestor.Matches] return. A nil fn clears it.
func (s *Suggestor) WithNormalizer(fn func(string) string) *Suggestor {
	s.normalizer = fn
	return s
}

// normalize applies the configured normalizer (if any) then case-folding (if on).
func (s *Suggestor) normalize(str string) string {
	if s.normalizer != nil {
		str = s.normalizer(str)
	}
	if s.caseFold {
		str = strings.ToLower(str)
	}
	return str
}

// Score returns the configured algorithm's similarity of a and b in [0,1] (1 =
// identical), after applying the Suggestor's normalization. It scores a single
// pair; [Suggestor.Suggest] / [Suggestor.Matches] rank a candidate set. A nil
// receiver returns 0.
func (s *Suggestor) Score(a, b string) float64 {
	if s == nil {
		return 0
	}
	return Similarity(s.normalize(a), s.normalize(b), s.algo)
}

// Matches ranks candidates by similarity to input, nearest first, keeping those
// scoring at least the configured minimum (default 0.6) and at most the configured
// number of results. Each [Match] carries the ORIGINAL candidate string and its
// score. Distance ties (equal score) prefer the candidate sharing the longer
// common prefix with input, then break lexicographically, so the result is
// deterministic. Empty/duplicate candidates and an empty input yield no matches; a
// nil receiver yields nil. Unlike [Suggestor.Suggest], Matches DOES include an
// exact match (score 1).
func (s *Suggestor) Matches(input string, candidates []string) []Match {
	if s == nil {
		return nil
	}
	ni := s.normalize(input)
	if ni == "" {
		return nil
	}
	type scored struct {
		value  string
		score  float64
		prefix int
	}
	var hits []scored
	seen := map[string]bool{}
	for _, c := range candidates {
		nc := s.normalize(c)
		if nc == "" || seen[nc] {
			continue
		}
		seen[nc] = true
		if score := Similarity(ni, nc, s.algo); score >= s.minScore {
			hits = append(hits, scored{value: c, score: score, prefix: commonPrefixLen(ni, nc)})
		}
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].score != hits[j].score {
			return hits[i].score > hits[j].score
		}
		if hits[i].prefix != hits[j].prefix {
			return hits[i].prefix > hits[j].prefix
		}
		return hits[i].value < hits[j].value
	})
	if len(hits) > s.maxResults {
		hits = hits[:s.maxResults]
	}
	out := make([]Match, len(hits))
	for i, h := range hits {
		out[i] = Match{Value: h.value, Score: h.score}
	}
	return out
}

// Suggest is the "did you mean" convenience over [Suggestor.Matches]: it returns
// just the candidate strings, nearest first. But when input EXACTLY matches a
// candidate (it wasn't mistyped) it returns nil — there is nothing to suggest. A
// nil receiver or empty input also yields nil.
func (s *Suggestor) Suggest(input string, candidates []string) []string {
	if s == nil {
		return nil
	}
	ni := s.normalize(input)
	if ni == "" {
		return nil
	}
	for _, c := range candidates {
		if s.normalize(c) == ni { // exact match: input wasn't mistyped
			return nil
		}
	}
	matches := s.Matches(input, candidates)
	if len(matches) == 0 {
		return nil
	}
	out := make([]string, len(matches))
	for i, m := range matches {
		out[i] = m.Value
	}
	return out
}

// commonPrefixLen is the length (in runes) of the longest common prefix of a and b.
func commonPrefixLen(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	n := 0
	for n < len(ra) && n < len(rb) && ra[n] == rb[n] {
		n++
	}
	return n
}
