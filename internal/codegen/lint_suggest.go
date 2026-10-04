package codegen

import "fmt"

// Typo suggestions for lint messages, used at codegen time only. The runtime's opt-in
// Suggestor is separate.

// didYouMean appends "; did you mean %q?" to msg when a candidate is within edit distance 2
// of name, and returns msg unchanged otherwise.
func didYouMean(msg, name string, candidates []string) string {
	if s := closestName(name, candidates); s != "" {
		return msg + fmt.Sprintf("; did you mean %q?", s)
	}
	return msg
}

// closestName returns the candidate within edit distance 2 of target (the nearest typo
// fix), or "" when none is close enough.
func closestName(target string, candidates []string) string {
	best, bestDist := "", 3
	for _, c := range candidates {
		if d := levenshtein(target, c); d < bestDist {
			best, bestDist = c, d
		}
	}
	return best
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
