package rotini

import (
	"sort"
	"strings"
)

// KeySuggestor is the conventional registry key a [Suggestor] is bound under.
const KeySuggestor = "suggestor"

// defaultMinScore is the cutoff a [Suggestor] keeps candidates at or above: similar enough to
// be worth offering, strict enough to skip merely-adjacent words.
const defaultMinScore = 0.6

// Suggestor turns a possibly-mistyped token and a list of candidates into ranked "did you
// mean" suggestions. It is a pure ranking function: it discovers nothing, prints nothing, and
// holds no state beyond its configuration. Candidates come from whatever vocabulary the caller
// has — command names, enum members, map keys, any []string.
//
//	s := rotini.NewSuggestor().
//		WithAlgorithm(rotini.SuggestAlgorithmJaroWinkler).
//		WithMinScore(0.7).
//		WithMaxResults(5).
//		WithCaseFold()
//	hits := s.Suggest("isntall", []string{"install", "uninstall", "list"}) // [install]
//
// rotini itself suggests nothing. A program binds one under [KeySuggestor] and applies it to a
// [ParseError]'s Token and Candidates if it wants suggestions:
//
//	suggestor := rtx.MustGet[*rotini.Suggestor](rotini.KeySuggestor)
//	if best, ok := suggestor.Closest(parseErr.Token, parseErr.Candidates); ok {
//		fmt.Fprintf(rtx.Stderr, "Did you mean %q?\n", best)
//	}
//
// A configured Suggestor is safe for concurrent use; finish configuring it before sharing it
// across goroutines.
type Suggestor struct {
	algorithm  SuggestAlgorithm
	minScore   float64
	maxResults int
	caseFold   bool
	normalizer func(string) string
}

// Match is a scored suggestion: the original candidate Value and its similarity Score in
// [0,1], 1 being identical.
type Match struct {
	Value string
	Score float64
}

// NewSuggestor returns a [Suggestor] ranking by [SuggestAlgorithmLevenshtein] with a minimum
// score of 0.6 and at most 3 results. The With methods return the receiver, so they chain.
func NewSuggestor() *Suggestor {
	return &Suggestor{
		algorithm:  SuggestAlgorithmLevenshtein,
		minScore:   defaultMinScore,
		maxResults: 3,
	}
}

// WithAlgorithm selects the ranking algorithm. Default [SuggestAlgorithmLevenshtein]; an
// unrecognized algorithm is ignored.
func (s *Suggestor) WithAlgorithm(algorithm SuggestAlgorithm) *Suggestor {
	if algorithm.Valid() {
		s.algorithm = algorithm
	}
	return s
}

// WithMinScore sets the smallest similarity score a candidate may have and still be suggested.
// Default 0.6; a score outside [0,1] is ignored.
func (s *Suggestor) WithMinScore(score float64) *Suggestor {
	if score >= 0 && score <= 1 {
		s.minScore = score
	}
	return s
}

// WithMaxResults caps how many suggestions [Suggestor.Suggest] / [Suggestor.Matches] return,
// Default 3. A non-positive n is ignored.
func (s *Suggestor) WithMaxResults(n int) *Suggestor {
	if n > 0 {
		s.maxResults = n
	}
	return s
}

// WithCaseFold makes matching case-insensitive (input and candidates are lowered before scoring),
// Off by default.
func (s *Suggestor) WithCaseFold() *Suggestor {
	s.caseFold = true
	return s
}

// WithNormalizer sets a preprocessing function applied to the input and each candidate before
// scoring, running before case-folding. The original candidate strings are still what
// [Suggestor.Suggest] and [Suggestor.Matches] return. A nil function clears it.
func (s *Suggestor) WithNormalizer(normalizer func(string) string) *Suggestor {
	s.normalizer = normalizer
	return s
}

// normalize applies the configured normalizer (if any) then case-folding (if on).
func (s *Suggestor) normalize(text string) string {
	if s.normalizer != nil {
		text = s.normalizer(text)
	}
	if s.caseFold {
		text = strings.ToLower(text)
	}
	return text
}

// Score returns the configured algorithm's similarity of a and b in [0,1], after applying the
// Suggestor's normalization. A nil receiver returns 0.
func (s *Suggestor) Score(a, b string) float64 {
	if s == nil {
		return 0
	}
	return similarity(s.normalize(a), s.normalize(b), s.algorithm)
}

// Matches ranks candidates by similarity to input, nearest first, keeping those at or above
// the configured minimum and at most the configured number of results. Ties prefer the
// candidate sharing the longer common prefix with input, then break lexicographically, so the
// result is deterministic. Empty candidates are skipped and duplicates collapse to the first
// occurrence. Unlike [Suggestor.Suggest], Matches does include an exact match.
func (s *Suggestor) Matches(input string, candidates []string) []Match {
	if s == nil {
		return nil
	}
	normalizedInput := s.normalize(input)
	if normalizedInput == "" {
		return nil
	}
	type scoredMatch struct {
		value     string
		score     float64
		prefixLen int
	}
	hits := make([]scoredMatch, 0, len(candidates))
	seen := make(map[string]bool, len(candidates))
	for _, candidate := range candidates {
		normalizedCandidate := s.normalize(candidate)
		if normalizedCandidate == "" || seen[normalizedCandidate] {
			continue
		}
		seen[normalizedCandidate] = true
		score := similarity(normalizedInput, normalizedCandidate, s.algorithm)
		if score >= s.minScore {
			hits = append(hits, scoredMatch{
				value:     candidate,
				score:     score,
				prefixLen: commonPrefixLength(normalizedInput, normalizedCandidate),
			})
		}
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
	result := make([]Match, len(hits))
	for i, hit := range hits {
		result[i] = Match{Value: hit.value, Score: hit.score}
	}
	return result
}

// Suggest returns just the candidate strings from [Suggestor.Matches], nearest first — except
// that an input exactly matching a candidate was not mistyped, so it yields nil.
func (s *Suggestor) Suggest(input string, candidates []string) []string {
	if s == nil {
		return nil
	}
	matches := s.Matches(input, candidates)
	// A (normalized) exact match scores exactly 1 and sorts first: the input was
	// typed correctly, so there is nothing to suggest.
	if len(matches) == 0 || matches[0].Score == 1 {
		return nil
	}
	result := make([]string, len(matches))
	for i, match := range matches {
		result[i] = match.Value
	}
	return result
}

// Closest returns the single best suggestion for input. Like [Suggestor.Suggest] it reports
// ok=false when input exactly matches a candidate or nothing clears the minimum score.
func (s *Suggestor) Closest(input string, candidates []string) (suggestion string, ok bool) {
	if hits := s.Suggest(input, candidates); len(hits) > 0 {
		return hits[0], true
	}
	return "", false
}

// commonPrefixLength is the length (in runes) of the longest common prefix of a and b.
func commonPrefixLength(a, b string) int {
	aRunes, bRunes := []rune(a), []rune(b)
	length := 0
	for length < len(aRunes) && length < len(bRunes) && aRunes[length] == bRunes[length] {
		length++
	}
	return length
}

// String metrics: rune-based nearness algorithms, each normalized to a [0,1] similarity score
// that a [Suggestor] ranks by.

// SuggestAlgorithm selects the string-distance algorithm a [Suggestor] ranks by. The package
// defines the set; it is not user-extensible.
type SuggestAlgorithm string

const (
	// SuggestAlgorithmLevenshtein — Levenshtein (Wagner–Fischer) edit distance:
	// insertions, deletions, substitutions, each cost one. The default.
	SuggestAlgorithmLevenshtein SuggestAlgorithm = "levenshtein"
	// SuggestAlgorithmDamerauLevenshtein — true (unrestricted) Damerau–Levenshtein:
	// Levenshtein plus transpositions of adjacent runes, allowing a substring to
	// be edited more than once.
	SuggestAlgorithmDamerauLevenshtein SuggestAlgorithm = "damerau-levenshtein"
	// SuggestAlgorithmOptimalStringAlignment — restricted Damerau–Levenshtein:
	// adds adjacent transpositions, but no substring is edited more than once.
	SuggestAlgorithmOptimalStringAlignment SuggestAlgorithm = "optimal-string-alignment"
	// SuggestAlgorithmHamming — positional substitutions; defined only for
	// equal-length strings (unequal lengths score 0).
	SuggestAlgorithmHamming SuggestAlgorithm = "hamming"
	// SuggestAlgorithmLongestCommonSubsequence — longest common subsequence; the
	// score is 2·LCS/(len a + len b).
	SuggestAlgorithmLongestCommonSubsequence SuggestAlgorithm = "longest-common-subsequence"
	// SuggestAlgorithmJaro — Jaro similarity, weighting matching runes and
	// transpositions; good for short strings.
	SuggestAlgorithmJaro SuggestAlgorithm = "jaro"
	// SuggestAlgorithmJaroWinkler — Jaro plus a shared-prefix bonus; the classic
	// "did you mean" metric for short, prefix-similar typos.
	SuggestAlgorithmJaroWinkler SuggestAlgorithm = "jaro-winkler"
	// SuggestAlgorithmSorensenDice — Sørensen–Dice coefficient over rune bigrams.
	SuggestAlgorithmSorensenDice SuggestAlgorithm = "sorensen-dice"
	// SuggestAlgorithmJaccard — Jaccard index over the set of rune bigrams.
	SuggestAlgorithmJaccard SuggestAlgorithm = "jaccard"
)

// Algorithms returns every supported [SuggestAlgorithm] in a stable order — for
// enumerating the choices (config validation, a flag's enum, a UI list).
func Algorithms() []SuggestAlgorithm {
	return []SuggestAlgorithm{
		SuggestAlgorithmLevenshtein,
		SuggestAlgorithmDamerauLevenshtein,
		SuggestAlgorithmOptimalStringAlignment,
		SuggestAlgorithmHamming,
		SuggestAlgorithmLongestCommonSubsequence,
		SuggestAlgorithmJaro,
		SuggestAlgorithmJaroWinkler,
		SuggestAlgorithmSorensenDice,
		SuggestAlgorithmJaccard,
	}
}

// Valid reports whether a is a recognized [SuggestAlgorithm].
func (a SuggestAlgorithm) Valid() bool {
	switch a {
	case SuggestAlgorithmLevenshtein, SuggestAlgorithmDamerauLevenshtein,
		SuggestAlgorithmOptimalStringAlignment, SuggestAlgorithmHamming,
		SuggestAlgorithmLongestCommonSubsequence, SuggestAlgorithmJaro,
		SuggestAlgorithmJaroWinkler, SuggestAlgorithmSorensenDice, SuggestAlgorithmJaccard:
		return true
	default:
		return false
	}
}

// similarity normalizes algorithm's metric for a and b to a score in [0,1], so edit-distance
// and similarity algorithms compare on one scale. An unrecognized algorithm falls back to
// Levenshtein. Case-folding and normalization are the caller's.
func similarity(a, b string, algorithm SuggestAlgorithm) float64 {
	switch algorithm {
	case SuggestAlgorithmDamerauLevenshtein:
		return editSimilarity(damerauLevenshtein(a, b), a, b)
	case SuggestAlgorithmOptimalStringAlignment:
		return editSimilarity(optimalStringAlignment(a, b), a, b)
	case SuggestAlgorithmHamming:
		distance, ok := hamming(a, b)
		if !ok {
			return 0
		}
		return editSimilarityLength(distance, runeLength(a))
	case SuggestAlgorithmLongestCommonSubsequence:
		lenA, lenB := runeLength(a), runeLength(b)
		if lenA+lenB == 0 {
			return 1
		}
		return 2 * float64(longestCommonSubsequence(a, b)) / float64(lenA+lenB)
	case SuggestAlgorithmJaro:
		return jaro(a, b)
	case SuggestAlgorithmJaroWinkler:
		return jaroWinkler(a, b)
	case SuggestAlgorithmSorensenDice:
		return sorensenDice(a, b)
	case SuggestAlgorithmJaccard:
		return jaccard(a, b)
	default: // SuggestAlgorithmLevenshtein and any unrecognized algorithm
		return editSimilarity(levenshtein(a, b), a, b)
	}
}

// editSimilarity maps an edit distance to a [0,1] similarity, normalizing by the
// longer of the two rune lengths.
func editSimilarity(distance int, a, b string) float64 {
	return editSimilarityLength(distance, max(runeLength(a), runeLength(b)))
}

func editSimilarityLength(distance, length int) float64 {
	if length == 0 {
		return 1
	}
	return 1 - float64(distance)/float64(length)
}

func runeLength(s string) int { return len([]rune(s)) }

// levenshtein returns the Levenshtein (Wagner–Fischer) edit distance between a
// and b: the minimum number of single-rune insertions, deletions, and
// substitutions to turn one into the other.
func levenshtein(a, b string) int {
	aRunes, bRunes := []rune(a), []rune(b)
	if len(aRunes) == 0 {
		return len(bRunes)
	}
	if len(bRunes) == 0 {
		return len(aRunes)
	}
	previousRow := make([]int, len(bRunes)+1)
	for j := range previousRow {
		previousRow[j] = j
	}
	for i := 1; i <= len(aRunes); i++ {
		currentRow := make([]int, len(bRunes)+1)
		currentRow[0] = i
		for j := 1; j <= len(bRunes); j++ {
			cost := 1
			if aRunes[i-1] == bRunes[j-1] {
				cost = 0
			}
			currentRow[j] = min(previousRow[j]+1, currentRow[j-1]+1, previousRow[j-1]+cost)
		}
		previousRow = currentRow
	}
	return previousRow[len(bRunes)]
}

// optimalStringAlignment returns the Optimal String Alignment distance (restricted
// Damerau–Levenshtein): Levenshtein plus transposition of two adjacent runes, with
// the restriction that no substring is edited more than once.
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

// damerauLevenshtein returns the true (unrestricted) Damerau–Levenshtein distance:
// insertions, deletions, substitutions, and transpositions of adjacent runes,
// allowing a substring to be edited more than once (so it can beat
// optimal string alignment, e.g. "ca"→"abc" is 2, not 3).
func damerauLevenshtein(a, b string) int {
	aRunes, bRunes := []rune(a), []rune(b)
	lenA, lenB := len(aRunes), len(bRunes)
	if lenA == 0 {
		return lenB
	}
	if lenB == 0 {
		return lenA
	}
	maxDist := lenA + lenB
	matrix := make([][]int, lenA+2)
	for i := range matrix {
		matrix[i] = make([]int, lenB+2)
	}
	matrix[0][0] = maxDist
	for i := range lenA + 1 {
		matrix[i+1][0] = maxDist
		matrix[i+1][1] = i
	}
	for j := range lenB + 1 {
		matrix[0][j+1] = maxDist
		matrix[1][j+1] = j
	}
	lastRow := map[rune]int{} // last row (1-indexed) at which each rune appeared in a
	for i := 1; i <= lenA; i++ {
		lastMatchCol := 0
		for j := 1; j <= lenB; j++ {
			matchRow := lastRow[bRunes[j-1]]
			matchCol := lastMatchCol
			cost := 1
			if aRunes[i-1] == bRunes[j-1] {
				cost = 0
				lastMatchCol = j
			}
			matrix[i+1][j+1] = min(
				matrix[i][j]+cost, // substitution / match
				matrix[i+1][j]+1,  // insertion
				matrix[i][j+1]+1,  // deletion
				matrix[matchRow][matchCol]+(i-matchRow-1)+1+(j-matchCol-1), // transposition
			)
		}
		lastRow[aRunes[i-1]] = i
	}
	return matrix[lenA+1][lenB+1]
}

// hamming returns the Hamming distance — the number of positions at which a and b
// differ — and ok=false when the strings differ in rune length (Hamming is defined
// only for equal-length strings).
func hamming(a, b string) (distance int, ok bool) {
	aRunes, bRunes := []rune(a), []rune(b)
	if len(aRunes) != len(bRunes) {
		return 0, false
	}
	for i := range aRunes {
		if aRunes[i] != bRunes[i] {
			distance++
		}
	}
	return distance, true
}

// longestCommonSubsequence returns the length of the longest common subsequence of
// a and b (runes in order, not necessarily contiguous).
func longestCommonSubsequence(a, b string) int {
	aRunes, bRunes := []rune(a), []rune(b)
	lenA, lenB := len(aRunes), len(bRunes)
	if lenA == 0 || lenB == 0 {
		return 0
	}
	previousRow := make([]int, lenB+1)
	for i := 1; i <= lenA; i++ {
		currentRow := make([]int, lenB+1)
		for j := 1; j <= lenB; j++ {
			if aRunes[i-1] == bRunes[j-1] {
				currentRow[j] = previousRow[j-1] + 1
			} else {
				currentRow[j] = max(previousRow[j], currentRow[j-1])
			}
		}
		previousRow = currentRow
	}
	return previousRow[lenB]
}

// jaro returns the Jaro similarity of a and b in [0,1] (1 = identical).
func jaro(a, b string) float64 {
	aRunes, bRunes := []rune(a), []rune(b)
	lenA, lenB := len(aRunes), len(bRunes)
	if lenA == 0 && lenB == 0 {
		return 1
	}
	if lenA == 0 || lenB == 0 {
		return 0
	}
	matchDistance := max(0, max(lenA, lenB)/2-1)
	aMatched := make([]bool, lenA)
	bMatched := make([]bool, lenB)
	matches := 0
	for i := range lenA {
		start := max(0, i-matchDistance)
		end := min(lenB, i+matchDistance+1)
		for j := start; j < end; j++ {
			if bMatched[j] || aRunes[i] != bRunes[j] {
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
	bIndex := 0
	for i := range lenA {
		if !aMatched[i] {
			continue
		}
		for !bMatched[bIndex] {
			bIndex++
		}
		if aRunes[i] != bRunes[bIndex] {
			transpositions++
		}
		bIndex++
	}
	transpositions /= 2
	matchCount := float64(matches)
	return (matchCount/float64(lenA) + matchCount/float64(lenB) + (matchCount-transpositions)/matchCount) / 3
}

// jaroWinkler returns the Jaro–Winkler similarity in [0,1]: Jaro plus a bonus for
// a shared prefix (up to 4 runes), so prefix-similar typos rank higher.
func jaroWinkler(a, b string) float64 {
	jaroScore := jaro(a, b)
	if jaroScore == 0 {
		return 0
	}
	aRunes, bRunes := []rune(a), []rune(b)
	prefixLength := 0
	for prefixLength < len(aRunes) && prefixLength < len(bRunes) && prefixLength < 4 && aRunes[prefixLength] == bRunes[prefixLength] {
		prefixLength++
	}
	const prefixScale = 0.1 // standard Winkler prefix scaling factor
	return jaroScore + float64(prefixLength)*prefixScale*(1-jaroScore)
}

// sorensenDice returns the Sørensen–Dice coefficient over rune bigrams in [0,1].
// It is weak on strings shorter than two runes (which have no bigrams).
func sorensenDice(a, b string) float64 {
	if a == b {
		return 1
	}
	aBigrams, bBigrams := bigrams(a), bigrams(b)
	if len(aBigrams) == 0 || len(bBigrams) == 0 {
		return 0
	}
	counts := make(map[string]int, len(aBigrams))
	for _, gram := range aBigrams {
		counts[gram]++
	}
	overlap := 0
	for _, gram := range bBigrams {
		if counts[gram] > 0 {
			counts[gram]--
			overlap++
		}
	}
	return 2 * float64(overlap) / float64(len(aBigrams)+len(bBigrams))
}

// jaccard returns the Jaccard index over the SET of rune bigrams in [0,1]. It is
// weak on strings shorter than two runes (which have no bigrams).
func jaccard(a, b string) float64 {
	if a == b {
		return 1
	}
	aSet, bSet := map[string]bool{}, map[string]bool{}
	for _, gram := range bigrams(a) {
		aSet[gram] = true
	}
	for _, gram := range bigrams(b) {
		bSet[gram] = true
	}
	intersection := 0
	for gram := range aSet {
		if bSet[gram] {
			intersection++
		}
	}
	union := len(aSet) + len(bSet) - intersection
	if union == 0 {
		return 0
	}
	return float64(intersection) / float64(union)
}

// bigrams returns the adjacent rune-pairs of s, in order (with repeats).
func bigrams(s string) []string {
	runes := []rune(s)
	if len(runes) < 2 {
		return nil
	}
	result := make([]string, 0, len(runes)-1)
	for i := 0; i+1 < len(runes); i++ {
		result = append(result, string(runes[i:i+2]))
	}
	return result
}
