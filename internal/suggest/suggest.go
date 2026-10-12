// Package suggest finds the name someone probably meant when they mistyped
// one, for "did you mean" hints.
package suggest

import "strings"

// maxDistance is the largest edit distance a suggestion may have.
const maxDistance = 2

// maxNameRunes bounds the names Closest compares, since Distance takes time
// proportional to the product of the lengths.
const maxNameRunes = 64

// Closest returns the candidate closest to name, ignoring case, if one is
// within two edits of it. Ties go to the earliest candidate.
func Closest(name string, candidates []string) (string, bool) {
	if len([]rune(name)) > maxNameRunes {
		return "", false
	}
	best, bestDistance := "", maxDistance+1
	for _, c := range candidates {
		if len([]rune(c)) > maxNameRunes {
			continue
		}
		if d := Distance(strings.ToLower(name), strings.ToLower(c)); d < bestDistance {
			best, bestDistance = c, d
		}
	}
	return best, best != ""
}

// Distance returns the Levenshtein distance between a and b: how many
// single-character insertions, deletions and substitutions turn one into
// the other.
func Distance(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(rb)]
}
