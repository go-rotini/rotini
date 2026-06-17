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

// --- String metrics ---
//
// A roster of nearness algorithms plus [Similarity], which normalizes any of them
// to a [0,1] score (1 = identical). All are rune-based and usable standalone.

// SuggestAlgo selects the string-distance algorithm a [Suggestor] (and
// [Similarity]) ranks by — a small string enum so it reads clearly in code and
// config. rotini defines the set; it is not user-extensible.
type SuggestAlgo string

const (
	// SuggestAlgoLevenshtein — Levenshtein (Wagner–Fischer) edit distance:
	// insertions, deletions, substitutions, each cost one. The default.
	SuggestAlgoLevenshtein SuggestAlgo = "levenshtein"
	// SuggestAlgoDamerauLevenshtein — true (unrestricted) Damerau–Levenshtein:
	// Levenshtein plus transpositions of adjacent runes, allowing a substring to
	// be edited more than once.
	SuggestAlgoDamerauLevenshtein SuggestAlgo = "damerau-levenshtein"
	// SuggestAlgoOSA — Optimal String Alignment (restricted Damerau–Levenshtein):
	// adds adjacent transpositions, but no substring is edited more than once.
	SuggestAlgoOSA SuggestAlgo = "osa"
	// SuggestAlgoHamming — positional substitutions; defined only for equal-length
	// strings (unequal lengths score 0).
	SuggestAlgoHamming SuggestAlgo = "hamming"
	// SuggestAlgoLCS — longest common subsequence; the score is 2·LCS/(len a+len b).
	SuggestAlgoLCS SuggestAlgo = "lcs"
	// SuggestAlgoJaro — Jaro similarity, weighting matching runes and
	// transpositions; good for short strings.
	SuggestAlgoJaro SuggestAlgo = "jaro"
	// SuggestAlgoJaroWinkler — Jaro plus a shared-prefix bonus; the classic
	// "did you mean" metric for short, prefix-similar typos.
	SuggestAlgoJaroWinkler SuggestAlgo = "jaro-winkler"
	// SuggestAlgoSorensenDice — Sørensen–Dice coefficient over rune bigrams.
	SuggestAlgoSorensenDice SuggestAlgo = "sorensen-dice"
	// SuggestAlgoJaccard — Jaccard index over the set of rune bigrams.
	SuggestAlgoJaccard SuggestAlgo = "jaccard"
)

// valid reports whether a is a recognized SuggestAlgo. New algorithms add a case
// here, a dispatch arm in [Similarity], and (if a metric) their own function.
func (a SuggestAlgo) valid() bool {
	switch a {
	case SuggestAlgoLevenshtein, SuggestAlgoDamerauLevenshtein, SuggestAlgoOSA,
		SuggestAlgoHamming, SuggestAlgoLCS, SuggestAlgoJaro, SuggestAlgoJaroWinkler,
		SuggestAlgoSorensenDice, SuggestAlgoJaccard:
		return true
	default:
		return false
	}
}

// Similarity normalizes algo's metric for a and b to a score in [0,1], where 1
// means identical and 0 means maximally dissimilar — so edit-distance and
// similarity algorithms compare on one scale. An unrecognized algo falls back to
// Levenshtein. It does no case-folding or normalization; a [Suggestor] applies
// those before calling it.
func Similarity(a, b string, algo SuggestAlgo) float64 {
	switch algo {
	case SuggestAlgoDamerauLevenshtein:
		return editSimilarity(DamerauLevenshtein(a, b), a, b)
	case SuggestAlgoOSA:
		return editSimilarity(OSA(a, b), a, b)
	case SuggestAlgoHamming:
		d, ok := Hamming(a, b)
		if !ok {
			return 0
		}
		return editSimilarityLen(d, runeLen(a))
	case SuggestAlgoLCS:
		la, lb := runeLen(a), runeLen(b)
		if la+lb == 0 {
			return 1
		}
		return 2 * float64(LCS(a, b)) / float64(la+lb)
	case SuggestAlgoJaro:
		return Jaro(a, b)
	case SuggestAlgoJaroWinkler:
		return JaroWinkler(a, b)
	case SuggestAlgoSorensenDice:
		return SorensenDice(a, b)
	case SuggestAlgoJaccard:
		return Jaccard(a, b)
	default: // SuggestAlgoLevenshtein and any unknown algo
		return editSimilarity(Levenshtein(a, b), a, b)
	}
}

// editSimilarity maps an edit distance to a [0,1] similarity, normalizing by the
// longer of the two rune lengths.
func editSimilarity(dist int, a, b string) float64 {
	return editSimilarityLen(dist, max(runeLen(a), runeLen(b)))
}

func editSimilarityLen(dist, n int) float64 {
	if n == 0 {
		return 1
	}
	return 1 - float64(dist)/float64(n)
}

func runeLen(s string) int { return len([]rune(s)) }

// Levenshtein returns the Levenshtein (Wagner–Fischer) edit distance between a
// and b: the minimum number of single-rune insertions, deletions, and
// substitutions to turn one into the other.
func Levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	if len(ra) == 0 {
		return len(rb)
	}
	if len(rb) == 0 {
		return len(ra)
	}
	prev := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur := make([]int, len(rb)+1)
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(rb)]
}

// OSA returns the Optimal String Alignment distance (restricted Damerau–
// Levenshtein): Levenshtein plus transposition of two adjacent runes, with the
// restriction that no substring is edited more than once.
func OSA(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	la, lb := len(ra), len(rb)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}
	d := make([][]int, la+1)
	for i := range la + 1 {
		d[i] = make([]int, lb+1)
		d[i][0] = i
	}
	for j := range lb + 1 {
		d[0][j] = j
	}
	for i := 1; i <= la; i++ {
		for j := 1; j <= lb; j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			d[i][j] = min(d[i-1][j]+1, d[i][j-1]+1, d[i-1][j-1]+cost)
			if i > 1 && j > 1 && ra[i-1] == rb[j-2] && ra[i-2] == rb[j-1] {
				d[i][j] = min(d[i][j], d[i-2][j-2]+1)
			}
		}
	}
	return d[la][lb]
}

// DamerauLevenshtein returns the true (unrestricted) Damerau–Levenshtein
// distance: insertions, deletions, substitutions, and transpositions of adjacent
// runes, allowing a substring to be edited more than once (so it can beat OSA,
// e.g. "ca"→"abc" is 2, not 3).
func DamerauLevenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	la, lb := len(ra), len(rb)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}
	maxDist := la + lb
	h := make([][]int, la+2)
	for i := range h {
		h[i] = make([]int, lb+2)
	}
	h[0][0] = maxDist
	for i := range la + 1 {
		h[i+1][0] = maxDist
		h[i+1][1] = i
	}
	for j := range lb + 1 {
		h[0][j+1] = maxDist
		h[1][j+1] = j
	}
	da := map[rune]int{}
	for i := 1; i <= la; i++ {
		db := 0
		for j := 1; j <= lb; j++ {
			k := da[rb[j-1]]
			l := db
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
				db = j
			}
			h[i+1][j+1] = min(
				h[i][j]+cost,              // substitution / match
				h[i+1][j]+1,               // insertion
				h[i][j+1]+1,               // deletion
				h[k][l]+(i-k-1)+1+(j-l-1), // transposition
			)
		}
		da[ra[i-1]] = i
	}
	return h[la+1][lb+1]
}

// Hamming returns the Hamming distance — the number of positions at which a and
// b differ — and ok=false when the strings differ in rune length (Hamming is
// defined only for equal-length strings).
func Hamming(a, b string) (dist int, ok bool) {
	ra, rb := []rune(a), []rune(b)
	if len(ra) != len(rb) {
		return 0, false
	}
	for i := range ra {
		if ra[i] != rb[i] {
			dist++
		}
	}
	return dist, true
}

// LCS returns the length of the longest common subsequence of a and b (runes in
// order, not necessarily contiguous).
func LCS(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	la, lb := len(ra), len(rb)
	if la == 0 || lb == 0 {
		return 0
	}
	prev := make([]int, lb+1)
	for i := 1; i <= la; i++ {
		cur := make([]int, lb+1)
		for j := 1; j <= lb; j++ {
			if ra[i-1] == rb[j-1] {
				cur[j] = prev[j-1] + 1
			} else {
				cur[j] = max(prev[j], cur[j-1])
			}
		}
		prev = cur
	}
	return prev[lb]
}

// Jaro returns the Jaro similarity of a and b in [0,1] (1 = identical).
func Jaro(a, b string) float64 {
	ra, rb := []rune(a), []rune(b)
	la, lb := len(ra), len(rb)
	if la == 0 && lb == 0 {
		return 1
	}
	if la == 0 || lb == 0 {
		return 0
	}
	matchDist := max(0, max(la, lb)/2-1)
	aMatched := make([]bool, la)
	bMatched := make([]bool, lb)
	matches := 0
	for i := range la {
		start := max(0, i-matchDist)
		end := min(lb, i+matchDist+1)
		for j := start; j < end; j++ {
			if bMatched[j] || ra[i] != rb[j] {
				continue
			}
			aMatched[i], bMatched[j] = true, true
			matches++
			break
		}
	}
	if matches == 0 {
		return 0
	}
	transpositions := 0.0
	k := 0
	for i := range la {
		if !aMatched[i] {
			continue
		}
		for !bMatched[k] {
			k++
		}
		if ra[i] != rb[k] {
			transpositions++
		}
		k++
	}
	transpositions /= 2
	m := float64(matches)
	return (m/float64(la) + m/float64(lb) + (m-transpositions)/m) / 3
}

// JaroWinkler returns the Jaro–Winkler similarity in [0,1]: Jaro plus a bonus
// for a shared prefix (up to 4 runes), so prefix-similar typos rank higher.
func JaroWinkler(a, b string) float64 {
	j := Jaro(a, b)
	if j == 0 {
		return 0
	}
	ra, rb := []rune(a), []rune(b)
	prefix := 0
	for prefix < len(ra) && prefix < len(rb) && prefix < 4 && ra[prefix] == rb[prefix] {
		prefix++
	}
	const scale = 0.1 // standard Winkler prefix scaling factor
	return j + float64(prefix)*scale*(1-j)
}

// SorensenDice returns the Sørensen–Dice coefficient over rune bigrams in [0,1].
// It is weak on strings shorter than two runes (no bigrams).
func SorensenDice(a, b string) float64 {
	if a == b {
		return 1
	}
	ba, bb := bigrams(a), bigrams(b)
	if len(ba) == 0 || len(bb) == 0 {
		return 0
	}
	counts := map[string]int{}
	for _, g := range ba {
		counts[g]++
	}
	overlap := 0
	for _, g := range bb {
		if counts[g] > 0 {
			counts[g]--
			overlap++
		}
	}
	return 2 * float64(overlap) / float64(len(ba)+len(bb))
}

// Jaccard returns the Jaccard index over the SET of rune bigrams in [0,1].
func Jaccard(a, b string) float64 {
	if a == b {
		return 1
	}
	sa, sb := map[string]bool{}, map[string]bool{}
	for _, g := range bigrams(a) {
		sa[g] = true
	}
	for _, g := range bigrams(b) {
		sb[g] = true
	}
	inter := 0
	for g := range sa {
		if sb[g] {
			inter++
		}
	}
	union := len(sa) + len(sb) - inter
	if union == 0 {
		return 0
	}
	return float64(inter) / float64(union)
}

// bigrams returns the adjacent rune-pairs of s, in order (with repeats).
func bigrams(s string) []string {
	r := []rune(s)
	if len(r) < 2 {
		return nil
	}
	out := make([]string, 0, len(r)-1)
	for i := 0; i+1 < len(r); i++ {
		out = append(out, string(r[i:i+2]))
	}
	return out
}
