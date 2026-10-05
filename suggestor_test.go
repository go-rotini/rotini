package rotini

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

// The corpora below measure both failure modes of the Suggestor's defaults: silence on a real
// typo, and a suggestion for a word that is not one.

// cliVocabulary is a realistic command-and-flag namespace with genuine near-collisions.
var cliVocabulary = []string{
	"install", "uninstall", "generate", "validate", "initialize", "version", "list",
	"--verbose", "--version", "--output", "--shutdown-timeout", "--shutdown", "schedule",
	"serve", "shell", "queue", "status", "library", "service", "worker", "add", "remove",
	"done", "purge", "compact", "search", "show", "albums", "artists", "songs", "config",
}

// cliTypos covers dropped, doubled and transposed characters, adjacent-key slips and
// truncations.
var cliTypos = map[string]string{
	"instal": "install", "installl": "install", "isntall": "install",
	"gnerate": "generate", "genrate": "generate", "generaet": "generate",
	"validat": "validate", "vallidate": "validate",
	"initialze": "initialize", "inititalize": "initialize",
	"vesion": "version", "verison": "version",
	"--vebose": "--verbose", "--verbsoe": "--verbose", "--verbos": "--verbose",
	"--outupt": "--output", "--ouput": "--output",
	"scedule": "schedule", "shcedule": "schedule",
	"queu": "queue", "quee": "queue", "stauts": "status", "statsu": "status",
	"libary": "library", "serach": "search", "compcat": "compact",
	"albms": "albums", "artsits": "artists", "confg": "config", "remvoe": "remove",
}

// notTypos are words that are not near-misses of anything in the vocabulary, several of them
// plausible CLI verbs; any suggestion for one is a wrong guess.
var notTypos = []string{
	"kubernetes", "frobnicate", "xyzzy", "docker", "terraform", "ansible", "helm",
	"migrate", "deploy", "rollback", "commit", "branch", "checkout", "prune",
	"aaaaaa", "zzz", "foobar", "widget", "sync", "watch", "logs", "exec",
}

// shortVocabulary covers short commands, which a threshold tuned only on longer words misses.
var shortVocabulary = []string{
	"help", "list", "add", "get", "run", "show", "init",
	"push", "pull", "diff", "log", "set", "rm", "up", "down",
}

var shortTypos = map[string]string{
	"hlep": "help", "hepl": "help", "hel": "help",
	"lsit": "list", "lits": "list", "lst": "list",
	"sohw": "show", "shw": "show", "inti": "init", "int": "init",
	"psuh": "push", "dwon": "down", "dif": "diff",
}

// shortNotTypos are short words that are not near-misses of shortVocabulary.
var shortNotTypos = []string{
	"exec", "sync", "watch", "tail", "make", "test", "bash", "grep", "sed", "awk", "cat", "cp", "ls",
}

// TestSuggestor_defaultsAreCorrectOnBothAxes pins the default algorithm and threshold against
// the long corpus: every typo suggested correctly, no non-typo matched.
func TestSuggestor_defaultsAreCorrectOnBothAxes(t *testing.T) {
	s := NewSuggestor()

	var missed []string
	for typo, want := range cliTypos {
		got, ok := s.Closest(typo, cliVocabulary)
		if !ok || got != want {
			missed = append(missed, typo+"→"+got+" (want "+want+")")
		}
	}
	if len(missed) > 0 {
		slices.Sort(missed)
		t.Errorf("stayed silent or guessed wrong on %d/%d real typos: %v",
			len(missed), len(cliTypos), missed)
	}

	var invented []string
	for _, word := range notTypos {
		if got, ok := s.Closest(word, cliVocabulary); ok {
			invented = append(invented, word+"→"+got)
		}
	}
	if len(invented) > 0 {
		t.Errorf("invented %d match(es) for words that were not typos: %v\n"+
			"A confident wrong guess is worse than no guess — it is what makes users stop "+
			"trusting the suggestion.", len(invented), invented)
	}
}

// TestSuggestor_shortCommandsAreNotABlindSpot pins that the default threshold catches typos of
// short commands (`hlep` → `help`), missing at most one, without false positives.
func TestSuggestor_shortCommandsAreNotABlindSpot(t *testing.T) {
	s := NewSuggestor()

	var missed []string
	for typo, want := range shortTypos {
		if got, ok := s.Closest(typo, shortVocabulary); !ok || got != want {
			missed = append(missed, typo+"→"+got+" (want "+want+")")
		}
	}
	// "hel" → "help" is the budgeted miss: catching a one-edit typo of a three-character input
	// needs a threshold loose enough to invent matches.
	if len(missed) > 1 {
		slices.Sort(missed)
		t.Errorf("missed %d/%d short-command typos (budget 1): %v",
			len(missed), len(shortTypos), missed)
	}

	var invented []string
	for _, word := range shortNotTypos {
		if got, ok := s.Closest(word, shortVocabulary); ok {
			invented = append(invented, word+"→"+got)
		}
	}
	if len(invented) > 0 {
		t.Errorf("invented %d short match(es): %v\n"+
			"Short vocabularies are where a loose threshold does the most damage — at three "+
			"characters everything is close to everything.", len(invented), invented)
	}
}

// TestSuggestor_aSingleEditFloorIsWorseThanAThreshold pins that a fixed one-edit floor, unlike
// the score threshold, produces false positives on short words ("cp" → "up").
func TestSuggestor_aSingleEditFloorIsWorseThanAThreshold(t *testing.T) {
	withinOneEdit := func(input string, candidates []string) (string, bool) {
		for _, c := range candidates {
			if c != input && optimalStringAlignment(fold(input), fold(c)) <= 1 {
				return c, true
			}
		}
		return "", false
	}
	var invented []string
	for _, word := range shortNotTypos {
		if got, ok := withinOneEdit(word, shortVocabulary); ok {
			invented = append(invented, word+"→"+got)
		}
	}
	if len(invented) == 0 {
		t.Error("the one-edit floor produced no false positives here — re-run the comparison, " +
			"the reason for preferring a score threshold may no longer hold")
	}
}

// TestSuggestor_thresholdIsNotACliff pins that minimum scores near the default (0.70–0.85)
// invent no matches, and that 0.70 still catches every long-corpus typo.
func TestSuggestor_thresholdIsNotACliff(t *testing.T) {
	for _, score := range []float64{0.7, 0.75, 0.8, 0.85} {
		s := NewSuggestor().WithMinScore(score)
		var invented int
		for _, word := range notTypos {
			if _, ok := s.Closest(word, cliVocabulary); ok {
				invented++
			}
		}
		for _, word := range shortNotTypos {
			if _, ok := s.Closest(word, shortVocabulary); ok {
				invented++
			}
		}
		if invented > 0 {
			t.Errorf("min score %.2f invented %d match(es) for non-typos", score, invented)
		}
	}
	s := NewSuggestor().WithMinScore(0.7)
	for typo, want := range cliTypos {
		if got, ok := s.Closest(typo, cliVocabulary); !ok || got != want {
			t.Errorf("min score 0.70: %q → %q, want %q", typo, got, want)
		}
	}
}

// TestSuggestor_caseIsNeverTheUsersProblem pins case-insensitive matching, including a
// case-only typo ("--VERBOSE"), which the exact-match check must not silence.
func TestSuggestor_caseIsNeverTheUsersProblem(t *testing.T) {
	s := NewSuggestor()
	for input, want := range map[string]string{
		"--VERBOSE": "--verbose", // case only
		"--Verbose": "--verbose",
		"INSTALL":   "install",
		"INSTAL":    "install", // case and a dropped character
		"Instal":    "install",
	} {
		got, ok := s.Closest(input, cliVocabulary)
		if !ok {
			t.Errorf("%q suggested nothing — case was treated as the user's mistake", input)
			continue
		}
		if got != want {
			t.Errorf("%q → %q, want %q", input, got, want)
		}
	}
}

// TestSuggestor_exactMatchIsNotATypo pins that a token matching a candidate byte-for-byte gets
// no suggestion.
func TestSuggestor_exactMatchIsNotATypo(t *testing.T) {
	s := NewSuggestor()
	for _, input := range []string{"install", "--verbose", "schedule"} {
		if got, ok := s.Closest(input, cliVocabulary); ok {
			t.Errorf("%q suggested %q — it was spelled correctly", input, got)
		}
	}
}

// ── For: the ParseError adapter ──────────────────────────────────────────────

// TestSuggestorFor_closesTheLoop pins that For ranks a ParseError's Token against its
// Candidates.
func TestSuggestorFor_closesTheLoop(t *testing.T) {
	err := &ParseError{
		Kind:       ParseKindUnknownCommand,
		Msg:        `unknown command "instal"`,
		Token:      "instal",
		Candidates: []string{"install", "uninstall", "list"},
	}
	got := NewSuggestor().For(err)
	if len(got) == 0 || got[0] != "install" {
		t.Errorf("For() = %v, want install first", got)
	}
}

// TestSuggestorFor_wrappedErrorsStillResolve pins that For finds a *ParseError inside a joined
// or wrapped error.
func TestSuggestorFor_wrappedErrorsStillResolve(t *testing.T) {
	inner := &ParseError{Kind: ParseKindUnknownFlag, Token: "--vebose", Candidates: []string{"--verbose"}}
	wrapped := UsageError(errors.New("wrapping: " + inner.Error()))
	_ = wrapped // the interesting case is a real wrap, below

	if got := NewSuggestor().For(errors.Join(errors.New("context"), inner)); len(got) != 1 || got[0] != "--verbose" {
		t.Errorf("For(joined) = %v, want [--verbose]", got)
	}
}

// TestSuggestorFor_offeringNothingIsARealAnswer pins every way For declines with an empty
// result.
func TestSuggestorFor_offeringNothingIsARealAnswer(t *testing.T) {
	cases := map[string]error{
		"nil error":            nil,
		"not a ParseError":     errors.New("something else"),
		"no token":             &ParseError{Kind: ParseKindUnknownFlag, Candidates: []string{"--verbose"}},
		"no vocabulary":        &ParseError{Kind: ParseKindUnknownFlag, Token: "--vebose"},
		"nothing near enough":  &ParseError{Kind: ParseKindUnknownCommand, Token: "kubernetes", Candidates: cliVocabulary},
		"token was spelled ok": &ParseError{Kind: ParseKindUnknownCommand, Token: "install", Candidates: cliVocabulary},
	}
	for name, err := range cases {
		t.Run(name, func(t *testing.T) {
			if got := NewSuggestor().For(err); len(got) != 0 {
				t.Errorf("For() = %v, want nothing", got)
			}
		})
	}
}

// ── ranking mechanics ────────────────────────────────────────────────────────

// TestSuggestor_ranksAndCaps pins that several close candidates are offered and that
// WithMaxResults caps them.
func TestSuggestor_ranksAndCaps(t *testing.T) {
	candidates := []string{"--version", "--verbose", "--verify", "--quiet"}

	all := NewSuggestor().WithMinScore(0.5).WithMaxResults(0).Suggest("--vers", candidates)
	if len(all) < 2 {
		t.Fatalf("Suggest = %v, want several close candidates offered", all)
	}
	if capped := NewSuggestor().WithMinScore(0.5).WithMaxResults(2).Suggest("--vers", candidates); len(capped) != 2 {
		t.Errorf("WithMaxResults(2) returned %d", len(capped))
	}
}

// TestSuggestor_isDeterministic pins the tie-break (longer shared prefix, then lexicographic)
// so the same input always yields the same order.
func TestSuggestor_isDeterministic(t *testing.T) {
	candidates := []string{"bravo", "alpha", "brava", "bravx"}
	first := NewSuggestor().WithMinScore(0.5).WithMaxResults(0).Suggest("bravz", candidates)
	for range 20 {
		if got := NewSuggestor().WithMinScore(0.5).WithMaxResults(0).Suggest("bravz", candidates); !slices.Equal(got, first) {
			t.Fatalf("Suggest is unstable: %v then %v", first, got)
		}
	}
}

// TestSuggestor_skipsEmptyAndDuplicateCandidates pins that empty and case-folded duplicate
// candidates never reach the output.
func TestSuggestor_skipsEmptyAndDuplicateCandidates(t *testing.T) {
	got := NewSuggestor().Suggest("instal", []string{"", "install", "install", "INSTALL", ""})
	if len(got) != 1 || got[0] != "install" {
		t.Errorf("Suggest = %v, want exactly one install", got)
	}
}

// TestSuggestor_nilAndEmptyInputs pins that a nil Suggestor, an empty input and no candidates
// all return nothing without panicking.
func TestSuggestor_nilAndEmptyInputs(t *testing.T) {
	var nilSuggestor *Suggestor
	if got := nilSuggestor.Suggest("instal", cliVocabulary); got != nil {
		t.Errorf("nil Suggestor returned %v", got)
	}
	if _, ok := nilSuggestor.Closest("instal", cliVocabulary); ok {
		t.Error("nil Suggestor reported a hit")
	}
	if got := nilSuggestor.For(&ParseError{Token: "instal", Candidates: cliVocabulary}); got != nil {
		t.Errorf("nil Suggestor.For returned %v", got)
	}
	if got := NewSuggestor().Suggest("", cliVocabulary); got != nil {
		t.Errorf("empty input returned %v", got)
	}
	if got := NewSuggestor().Suggest("instal", nil); got != nil {
		t.Errorf("no candidates returned %v", got)
	}
}

// TestSuggestor_withMinScoreRejectsNonsense pins that a score outside [0,1] is ignored.
func TestSuggestor_withMinScoreRejectsNonsense(t *testing.T) {
	for _, bad := range []float64{-1, 1.5} {
		s := NewSuggestor().WithMinScore(bad)
		if s.minScore != defaultMinScore {
			t.Errorf("WithMinScore(%v) took effect: %v", bad, s.minScore)
		}
	}
}

// TestOptimalStringAlignment_transpositionCostsOne pins that an adjacent transposition is one
// edit, plus the empty and identical cases.
func TestOptimalStringAlignment_transpositionCostsOne(t *testing.T) {
	if got := optimalStringAlignment("isntall", "install"); got != 1 {
		t.Errorf("optimalStringAlignment(isntall, install) = %d, want 1", got)
	}
	if got := optimalStringAlignment("", "abc"); got != 3 {
		t.Errorf("distance from empty = %d, want 3", got)
	}
	if got := optimalStringAlignment("abc", "abc"); got != 0 {
		t.Errorf("distance to itself = %d, want 0", got)
	}
}

// FuzzSuggest checks the ranking's contract for any token, candidate list and setting:
//
//   - nothing panics, and the same input gives the same output in the same order
//   - at most maxResults results (when maxResults caps them), each one of the candidates,
//     each unique after case folding, each scoring at least the minimum, best first
//   - a token exactly equal to a candidate was not mistyped, so it yields nothing
//   - otherwise a candidate equal to the token apart from case ranks first
//
// The candidates arrive as one string split on commas, which lets the corpus carry lists.
func FuzzSuggest(f *testing.F) {
	for _, seed := range []struct {
		token, candidates string
		max               int
		min               float64
	}{
		{"stauts", "status,start,stats", 3, 0.7},
		{"satus", "status,setup", 0, 0.7},
		{"--verbos", "--verbose,--version,--output", 2, 0.5},
		{"VERSION", "version,verify", 3, 0.7},
		{"version", "version,verify", 3, 0.7},
		{"", "a,b", 3, 0.7},
		{"x", "", 3, 0.7},
		{"ünïcödé", "unicode,ünïcödé2,ünicöde", 3, 0.3},
		{"日本", "日本語,中国", 1, 0},
		{"aa", "aa,AA,aA", 5, 1},
		{"abc", ",,abc ,ABC,acb,bac", 0, 0},
	} {
		f.Add(seed.token, seed.candidates, seed.max, seed.min)
	}
	f.Fuzz(func(t *testing.T, token, list string, maxResults int, minScore float64) {
		candidates := strings.Split(list, ",")
		s := NewSuggestor().WithMaxResults(maxResults).WithMinScore(minScore)
		got := s.Suggest(token, candidates)

		if again := s.Suggest(token, candidates); !slices.Equal(got, again) {
			t.Fatalf("not deterministic: %q then %q", got, again)
		}
		if slices.Contains(candidates, token) && got != nil {
			t.Fatalf("token %q is a candidate, so nothing was mistyped; got %q", token, got)
		}
		if s.maxResults > 0 && len(got) > s.maxResults {
			t.Fatalf("%d results, want at most %d", len(got), s.maxResults)
		}
		seen := map[string]bool{}
		prev := 2.0
		for _, r := range got {
			if !slices.Contains(candidates, r) {
				t.Fatalf("result %q is not a candidate", r)
			}
			if seen[fold(r)] {
				t.Fatalf("result %q repeats another after case folding", r)
			}
			seen[fold(r)] = true
			score := nearness(fold(token), fold(r))
			if score < s.minScore {
				t.Fatalf("result %q scores %v, below the minimum %v", r, score, s.minScore)
			}
			if score > prev {
				t.Fatalf("results are not best first: %q (%v) follows a lower score (%v)", r, score, prev)
			}
			prev = score
		}
		if fold(token) != "" && !slices.Contains(candidates, token) {
			for _, c := range candidates {
				if fold(c) == fold(token) {
					if len(got) == 0 || fold(got[0]) != fold(token) {
						t.Fatalf("%q matches the token %q apart from case but does not rank first: %q", c, token, got)
					}
					break
				}
			}
		}
	})
}
