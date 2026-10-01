package rotini

import (
	"errors"
	"slices"
	"testing"
)

// The Suggestor's job is one question — "did they mean X?" — and it has two ways to be wrong:
// staying silent on a real typo, and inventing a match for a word that was not one. The second
// is the expensive one, because a confident wrong guess is what makes people stop trusting the
// feature, so the corpus below measures both directions and the defaults are pinned against it.

// cliVocabulary is a realistic command-and-flag namespace: real names from this repo's examples
// and companion CLI, long enough to have genuine near-collisions in it.
var cliVocabulary = []string{
	"install", "uninstall", "generate", "validate", "initialize", "version", "list",
	"--verbose", "--version", "--output", "--shutdown-timeout", "--shutdown", "schedule",
	"serve", "shell", "queue", "status", "library", "service", "worker", "add", "remove",
	"done", "purge", "compact", "search", "show", "albums", "artists", "songs", "config",
}

// cliTypos covers the shapes people actually produce: a dropped character, a doubled one, a
// transposition, an adjacent-key slip, and a truncation.
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

// notTypos are words that are NOT near-misses of anything in the vocabulary. Every suggestion
// here is a confident wrong guess. Several are deliberately plausible-looking CLI verbs, which
// is exactly when a ranker is most tempted to invent something.
var notTypos = []string{
	"kubernetes", "frobnicate", "xyzzy", "docker", "terraform", "ansible", "helm",
	"migrate", "deploy", "rollback", "commit", "branch", "checkout", "prune",
	"aaaaaa", "zzz", "foobar", "widget", "sync", "watch", "logs", "exec",
}

// shortVocabulary is the second corpus, and the one the first one's absence nearly shipped a
// bug through. Most CLIs have several four-character commands; a threshold tuned only on longer
// words goes completely silent on them.
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

// shortNotTypos are short words that are NOT near-misses of shortVocabulary — the set most
// likely to produce a wrong guess, since at three or four characters everything is close to
// everything.
var shortNotTypos = []string{
	"exec", "sync", "watch", "tail", "make", "test", "bash", "grep", "sed", "awk", "cat", "cp", "ls",
}

// TestSuggestor_defaultsAreCorrectOnBothAxes is the measurement that chose the algorithm and the
// threshold, kept as a test so a change to either has to beat it.
//
// The nine selectable algorithms this type used to carry were a survey, not an answer, and the
// default was the wrong end of it: Levenshtein at 0.6 with case-folding off invented three
// matches from this corpus and could not suggest anything for "--VERBOSE". Optimal string
// alignment at 0.8 is perfect in both directions.
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

// TestSuggestor_shortCommandsAreNotABlindSpot is the test the first corpus could not have
// written, because it had no short words in it.
//
// The default was 0.75 only after a real binary was run: at 0.8 — which looked perfect on the
// long corpus — `hlep` suggested NOTHING, because a threshold of 0.8 allows one edit per five
// characters and `help` is four. Every four-character command in every CLI was silently
// unreachable. Reading the table would never have shown it.
func TestSuggestor_shortCommandsAreNotABlindSpot(t *testing.T) {
	s := NewSuggestor()

	var missed []string
	for typo, want := range shortTypos {
		if got, ok := s.Closest(typo, shortVocabulary); !ok || got != want {
			missed = append(missed, typo+"→"+got+" (want "+want+")")
		}
	}
	// "hel" → "help" is a 3-character input: one edit is a third of the word, and catching
	// it needs a threshold loose enough to start inventing matches. Staying silent there is
	// the correct end to fail on, so one miss is the documented budget.
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

// TestSuggestor_aSingleEditFloorIsWorseThanAThreshold records an alternative that was tried and
// rejected, so it is not re-proposed: "always allow one edit, whatever the length" catches the
// three-character cases but takes short false positives from 0 to 2 ("cp" → "up", "sed" → "set").
// A distance of 2 is far worse — 8 short and 4 long wrong guesses.
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

// TestSuggestor_thresholdIsNotACliff: the defaults must not be a lucky point. A user who nudges
// the minimum score in either direction should get gracefully more or less, not a collapse —
// which is the property that ruled Jaro–Winkler out, since it was clean at 0.8 and wrong seven
// times at 0.7.
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
	// And loosening it does not suddenly lose the easy ones.
	s := NewSuggestor().WithMinScore(0.7)
	for typo, want := range cliTypos {
		if got, ok := s.Closest(typo, cliVocabulary); !ok || got != want {
			t.Errorf("min score 0.70: %q → %q, want %q", typo, got, want)
		}
	}
}

// TestSuggestor_caseIsNeverTheUsersProblem. Case-folding used to be opt-in, so a user typing
// --VERBOSE got nothing at all from a default Suggestor. There is no version of a CLI where
// that is the right answer, so there is no longer a setting for it.
//
// The case-ONLY typo is the sharp one, and it is what this test was written to catch: folding
// before the exact-match check made "--VERBOSE" look correctly spelled and silenced it. A wrong
// case is the mistake a user is least able to see in their own terminal.
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

// TestSuggestor_exactMatchIsNotATypo: a token that is in the vocabulary was typed correctly, so
// there is nothing to suggest. Without this the ranker answers "did you mean install?" to
// someone who typed install.
//
// "Exactly" is byte-for-byte, and the distinction is load-bearing in the other direction: see
// TestSuggestor_caseIsNeverTheUsersProblem. Folding first would have silenced the case typo,
// which is the one a user is least able to spot on their own.
func TestSuggestor_exactMatchIsNotATypo(t *testing.T) {
	s := NewSuggestor()
	for _, input := range []string{"install", "--verbose", "schedule"} {
		if got, ok := s.Closest(input, cliVocabulary); ok {
			t.Errorf("%q suggested %q — it was spelled correctly", input, got)
		}
	}
}

// ── For: the ParseError adapter ──────────────────────────────────────────────

// TestSuggestorFor_closesTheLoop covers the reason the method exists. rotini owns the error, so
// it knows both what the user typed and what would have been valid there; without For, every
// program writes the same errors.As-plus-two-nil-checks before it can ask.
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

// TestSuggestorFor_wrappedErrorsStillResolve: a funnel sees whatever the run recorded, which is
// rarely the bare *ParseError. errors.As is the whole reason this works through a wrapper.
func TestSuggestorFor_wrappedErrorsStillResolve(t *testing.T) {
	inner := &ParseError{Kind: ParseKindUnknownFlag, Token: "--vebose", Candidates: []string{"--verbose"}}
	wrapped := UsageError(errors.New("wrapping: " + inner.Error()))
	_ = wrapped // the interesting case is a real wrap, below

	if got := NewSuggestor().For(errors.Join(errors.New("context"), inner)); len(got) != 1 || got[0] != "--verbose" {
		t.Errorf("For(joined) = %v, want [--verbose]", got)
	}
}

// TestSuggestorFor_offeringNothingIsARealAnswer pins every way For declines, because a caller
// branches on the empty slice and each of these must produce one rather than a panic or a
// misleading hit.
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

// TestSuggestor_ranksAndCaps: several candidates can be genuinely close, and picking one
// arbitrarily is worse than offering both — "--vers" really is ambiguous between --verbose and
// --version, and that is the user's call.
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

// TestSuggestor_isDeterministic. Ties break by longer shared prefix, then lexicographically, so
// the same typo never produces different advice on different runs — which a map iteration or an
// unstable sort would otherwise cause, intermittently, in a help message.
func TestSuggestor_isDeterministic(t *testing.T) {
	candidates := []string{"bravo", "alpha", "brava", "bravx"}
	first := NewSuggestor().WithMinScore(0.5).WithMaxResults(0).Suggest("bravz", candidates)
	for range 20 {
		if got := NewSuggestor().WithMinScore(0.5).WithMaxResults(0).Suggest("bravz", candidates); !slices.Equal(got, first) {
			t.Fatalf("Suggest is unstable: %v then %v", first, got)
		}
	}
}

// TestSuggestor_skipsEmptyAndDuplicateCandidates: a generated vocabulary can carry both, and
// neither should reach the output.
func TestSuggestor_skipsEmptyAndDuplicateCandidates(t *testing.T) {
	got := NewSuggestor().Suggest("instal", []string{"", "install", "install", "INSTALL", ""})
	if len(got) != 1 || got[0] != "install" {
		t.Errorf("Suggest = %v, want exactly one install", got)
	}
}

// TestSuggestor_nilAndEmptyInputs: a nil Suggestor is a caller that forgot to construct one,
// and it must not panic inside a funnel that is already reporting a failure.
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

// TestSuggestor_withMinScoreRejectsNonsense: a score outside [0,1] is a caller bug, and
// silently adopting it would either suggest everything or nothing.
func TestSuggestor_withMinScoreRejectsNonsense(t *testing.T) {
	for _, bad := range []float64{-1, 1.5} {
		s := NewSuggestor().WithMinScore(bad)
		if s.minScore != defaultMinScore {
			t.Errorf("WithMinScore(%v) took effect: %v", bad, s.minScore)
		}
	}
}

// TestOptimalStringAlignment_transpositionCostsOne is why this metric and not Levenshtein: a
// swapped pair of letters is the commonest typo there is, and under Levenshtein it costs two
// edits — which at a 0.8 threshold on a short word is the difference between suggesting and
// staying silent.
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
