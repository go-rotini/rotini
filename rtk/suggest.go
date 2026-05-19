package rtk

import "strings"

// levenshteinDistance returns the edit distance between a and b — the
// minimum number of single-character insertions, deletions, or substitutions
// needed to transform one into the other.
//
// The two-row rolling implementation is O(min(|a|,|b|)) memory.
func levenshteinDistance(a, b string) int {
	if a == "" {
		return len(b)
	}
	if b == "" {
		return len(a)
	}

	prev := make([]int, len(b)+1)
	curr := make([]int, len(b)+1)

	for j := 0; j <= len(b); j++ {
		prev[j] = j
	}

	for i := 1; i <= len(a); i++ {
		curr[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 0
			if a[i-1] != b[j-1] {
				cost = 1
			}
			curr[j] = min(
				prev[j]+1,
				curr[j-1]+1,
				prev[j-1]+cost,
			)
		}
		prev, curr = curr, prev
	}

	return prev[len(b)]
}

// suggestNearest returns the closest candidate to name by Levenshtein
// distance (case-insensitive), or "" if no candidate is within distance 2.
//
// The distance cap (3 — strictly less than) was chosen to match the
// rotiniold heuristic and keeps suggestions reasonable for typical
// flag/command name lengths.
func suggestNearest(name string, candidates []string) string {
	best := ""
	bestDist := 3

	lower := strings.ToLower(name)
	for _, c := range candidates {
		d := levenshteinDistance(lower, strings.ToLower(c))
		if d < bestDist {
			bestDist = d
			best = c
		}
	}

	return best
}
