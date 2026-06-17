package tortellini

import "sort"

// KeySuggestor is the conventional registry key the generated main binds the
// [Suggestor] under (and handlers retrieve it by).
const KeySuggestor = "suggestor"

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
// The Suggestor is a pure ranking function over the candidates it is handed —
// it discovers nothing, prints nothing, and knows nothing about the command
// tree. Candidates come from whatever vocabulary the caller has: a ParseError's
// Candidates, sibling names off rtx.Chain(), enum members, or any []string.
type Suggestor struct {
	maxDistance int
	maxResults  int
}

// SuggestorOption configures a [Suggestor].
type SuggestorOption func(*Suggestor)

// WithMaxDistance sets the largest edit distance (Levenshtein) a candidate may
// have from the input and still be suggested. Default 2 — close typos only.
func WithMaxDistance(n int) SuggestorOption {
	return func(s *Suggestor) {
		if n > 0 {
			s.maxDistance = n
		}
	}
}

// WithMaxResults caps how many suggestions [Suggestor.Suggest] returns.
// Default 3.
func WithMaxResults(n int) SuggestorOption {
	return func(s *Suggestor) {
		if n > 0 {
			s.maxResults = n
		}
	}
}

// NewSuggestor returns a [Suggestor] ready to bind under a registry key
// (conventionally [KeySuggestor]).
func NewSuggestor(opts ...SuggestorOption) *Suggestor {
	s := &Suggestor{maxDistance: 2, maxResults: 3}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Suggest returns the candidates closest to input, nearest first, keeping only
// candidates within the configured edit distance and at most the configured
// number of results. Distance ties prefer the candidate sharing the longer
// common prefix with input (typing "ru" suggests "run" before the alias "r"),
// then break lexicographically, so the result is deterministic. An exact match
// means input wasn't mistyped: it returns nil, as it does for no input, no
// candidates within range, or a nil receiver.
func (s *Suggestor) Suggest(input string, candidates []string) []string {
	if s == nil || input == "" {
		return nil
	}
	type scored struct {
		name   string
		dist   int
		prefix int
	}
	var hits []scored
	seen := map[string]bool{}
	for _, c := range candidates {
		if c == "" || seen[c] {
			continue
		}
		seen[c] = true
		d := levenshtein(input, c)
		if d == 0 {
			return nil // exact match: nothing to suggest
		}
		if d <= s.maxDistance {
			hits = append(hits, scored{name: c, dist: d, prefix: commonPrefixLen(input, c)})
		}
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].dist != hits[j].dist {
			return hits[i].dist < hits[j].dist
		}
		if hits[i].prefix != hits[j].prefix {
			return hits[i].prefix > hits[j].prefix
		}
		return hits[i].name < hits[j].name
	})
	if len(hits) == 0 {
		return nil
	}
	if len(hits) > s.maxResults {
		hits = hits[:s.maxResults]
	}
	out := make([]string, len(hits))
	for i, h := range hits {
		out[i] = h.name
	}
	return out
}

// commonPrefixLen is the length of the longest common prefix of a and b.
func commonPrefixLen(a, b string) int {
	n := 0
	for n < len(a) && n < len(b) && a[n] == b[n] {
		n++
	}
	return n
}

// levenshtein is the edit distance between a and b (Wagner–Fischer).
func levenshtein(a, b string) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(b)]
}
