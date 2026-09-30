package rotini

import (
	"errors"
	"slices"
	"sort"
	"strings"
)

// Nearness: turning a token the parser rejected into the candidate the user probably meant.
//
// rotini itself suggests NOTHING in the programs built with it (its own companion tool opts in,
// the way any program can). A framework that guesses at a user's intent and prints the
// guess in its own voice is making an editorial decision that belongs to the program — so what
// rotini does is hand over the facts ([*ParseError] carries the Token that failed and the
// Candidates it failed against) plus a ranker, and the program decides whether to speak.
//
// # Why there is one algorithm and not a menu
//
// There used to be nine, selectable, defaulting to Levenshtein at a 0.6 minimum with
// case-folding off. That was a survey of string-distance metrics, not an answer, and the
// default was measurably the wrong end of it: three of the nine cannot work on this problem at
// all (Hamming is defined only for equal-length strings and scores 0/30 on real typos), and the
// out-of-the-box configuration both invented matches for unrelated words and failed to suggest
// anything for `--VERBOSE`.
//
// Measured over 30 realistic CLI typos — dropped, doubled and transposed characters, adjacent-key
// slips and truncations — against a 31-word vocabulary of real commands and flags, plus 22 words
// that are NOT typos of anything (where a suggestion is a confident WRONG guess):
//
//	optimal string alignment @ 0.75   30/30 correct   0/22 wrong
//	damerau-levenshtein      @ 0.75   30/30 correct   0/22 wrong   (~3x the cost)
//	jaro-winkler             @ 0.80   30/30 correct   3/22 wrong
//	jaro-winkler             @ 0.70   30/30 correct   7/22 wrong
//	levenshtein              @ 0.80   18/30 correct   0/22 wrong
//
// Jaro–Winkler's shared-prefix bonus is what makes it good at typos and what makes it
// hallucinate (`kubernetes` → `generate`, `docker` → `done`), and a confident wrong guess is
// worse than no guess: it is the failure that makes people stop trusting the feature.
//
// # Why 0.75 and not 0.8
//
// 0.8 looked clean on the corpus above and was silently useless for SHORT commands, which that
// corpus had none of. A threshold of 0.8 means a word tolerates one edit per five characters, so
// `help`, `list`, `show` and `init` tolerate NONE: "hlep" suggested nothing at all. Found by
// running a real binary, not by reading the table.
//
// Against a second corpus of 14 typos on short commands (`help` `list` `add` `get` `run` `show`
// `init` `push` `pull` `diff` `log` `set` `rm` `up` `down`) and 14 short words that are not
// typos of them:
//
//	@ 0.80   0/14 correct    ← the blind spot
//	@ 0.75  13/14 correct   0 wrong
//	@ 0.70  13/14 correct   0 wrong
//	@ 0.65  14/14 correct   1 wrong ("sed" → "set")
//
// 0.75 is the highest threshold that catches a single edit in a four-character word — exactly
// 1-1/4 — and it costs nothing on the long corpus. An absolute "always allow one edit" floor was
// tried instead and is worse: it takes short false positives from 0 to 2 ("cp" → "up").
//
// The remaining miss is a three-character command, where one edit is a third of the word and
// genuinely ambiguous. Catching those needs ~0.65, which starts inventing matches. **rotini
// stays silent there**, which is the correct end to fail on.

// defaultMinScore is the cutoff a [Suggestor] keeps candidates at or above. See the two tables
// above: 0.75 is clean on both recall and false positives for long AND short vocabularies.
const defaultMinScore = 0.75

// Suggestor ranks a possibly-mistyped token against a list of candidates. It is a pure ranking
// function: it discovers nothing, prints nothing, and holds no state beyond its configuration.
// Candidates come from whatever vocabulary the caller has — command names, enum members, map
// keys, any []string.
//
// The common case is one call on a failed parse, which [Suggestor.For] does end to end:
//
//	var suggestor = rotini.NewSuggestor()
//
//	func funnel(_ context.Context, rtx *rotini.Context, out rotini.Outcome) {
//		for _, err := range out.Errors {
//			fmt.Fprintf(rtx.Stderr, "Error: %s\n", err)
//			if hits := suggestor.For(err); len(hits) > 0 {
//				fmt.Fprintf(rtx.Stderr, "Did you mean %q?\n", hits[0])
//			}
//		}
//	}
//
// Matching is case-insensitive, always: a user typing `--VERBOSE` meant `--verbose`, and having
// to discover a setting to be told so is not a choice worth offering.
//
// A configured Suggestor is safe for concurrent use; finish configuring it before sharing it
// across goroutines.
type Suggestor struct {
	minScore   float64
	maxResults int
}

// NewSuggestor returns a [Suggestor] with the defaults the measurements above chose: a minimum
// score of 0.75 and at most 3 results. The With methods return the receiver, so they chain.
//
// The zero value is not usable; start here.
func NewSuggestor() *Suggestor {
	return &Suggestor{minScore: defaultMinScore, maxResults: 3}
}

// WithMinScore sets the similarity a candidate must reach to be offered, in [0,1]. A value
// outside that range is ignored.
//
// Lower to suggest more freely, raise to suggest only on near-certainty. Both directions have a
// cost measured in the table above: below about 0.7 the ranker starts offering unrelated words,
// and above 0.75 it goes silent on four-character commands, which most CLIs have several of.
func (s *Suggestor) WithMinScore(score float64) *Suggestor {
	if score >= 0 && score <= 1 {
		s.minScore = score
	}
	return s
}

// WithMaxResults caps how many suggestions [Suggestor.Suggest] and [Suggestor.For] return
// (default 3). Zero or less means no cap.
//
// More than one is worth offering when two candidates are genuinely close — picking between
// `--verbose` and `--version` for `--vers` is the user's call, not the program's.
func (s *Suggestor) WithMaxResults(n int) *Suggestor {
	s.maxResults = n
	return s
}

// For returns the suggestions for the token a [*ParseError] rejected, nearest first, or nil.
//
// This is the whole point of the type, and the one thing a CLI framework can offer that a
// string-distance library cannot: rotini owns the error, so it knows both what the user typed
// and what would have been valid there. Without it every program writes the same plumbing —
// an errors.As, a Token check, a Candidates check — before it can ask the question.
//
// It returns nil for an error that is not a [*ParseError], one carrying no token or no
// vocabulary, and one whose token is not near anything. **Offering nothing is a real answer**,
// and the reason this returns a slice rather than a string: a caller branches on emptiness
// rather than on a sentinel.
//
// Nothing is printed. What to say, and whether to say it, stays with the program.
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
// An input that exactly matches a candidate was not mistyped, so it yields nil. "Exactly" means
// byte-for-byte: a case-only difference IS a typo — the parser rejected "--VERBOSE", and the
// useful thing to say about it is "--verbose".
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
	// An input that matches a candidate EXACTLY was typed correctly, so there is nothing to
	// suggest. The comparison is on the raw strings, not the folded ones: "--VERBOSE" folds
	// onto "--verbose" but is not the same token, and since the parser rejected it, "did you
	// mean --verbose?" is precisely the advice the user needs.
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

// fold normalizes a token for comparison. Case only: a CLI's vocabulary is ASCII-ish by
// convention, and anything cleverer (accent stripping, Unicode case folding) would be guessing
// about a vocabulary the program owns.
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
// of two ADJACENT runes, with the restriction that no substring is edited more than once.
//
// The transposition case is what earns it over plain Levenshtein, and it is the commonest typo
// there is: "isntall" is one edit from "install" here and two under Levenshtein, which is the
// difference between suggesting and staying silent. The unrestricted variant (true
// Damerau–Levenshtein) scored identically on every case measured and costs about three times as
// much, so the restriction is free.
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
