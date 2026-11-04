package suggest

import (
	"strings"
	"testing"

	"pgregory.net/rapid"
)

func TestClosest(t *testing.T) {
	candidates := []string{"self", "baseline", "meta"}
	tests := []struct {
		name string
		want string
		ok   bool
	}{
		{"selff", "self", true},
		{"Self", "self", true},
		{"sel", "self", true},
		{"baselin", "baseline", true},
		{"mta", "meta", true},
		{"items", "", false},
		{"", "", false},
		{strings.Repeat("s", 65), "", false},
	}
	for _, tt := range tests {
		if got, ok := Closest(tt.name, candidates); got != tt.want || ok != tt.ok {
			t.Errorf("Closest(%q) = %q, %v; want %q, %v", tt.name, got, ok, tt.want, tt.ok)
		}
	}
	if got, _ := Closest("ab", []string{"abc", "abd"}); got != "abc" {
		t.Errorf("Closest(ab) = %q; want the first of two equally close candidates, abc", got)
	}
}

func TestDistance(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"", "abc", 3},
		{"kitten", "sitting", 3},
		{"date_time", "date-time", 1},
		{"héllo", "hello", 1},
	}
	for _, tt := range tests {
		if got := Distance(tt.a, tt.b); got != tt.want {
			t.Errorf("Distance(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}

// TestDistanceIsAMetric checks the properties of an edit distance: it's
// zero only between equal strings, symmetric, at most the longer length,
// and obeys the triangle inequality.
func TestDistanceIsAMetric(t *testing.T) {
	word := rapid.StringMatching(`[a-c]{0,8}`)
	rapid.Check(t, func(t *rapid.T) {
		a, b, c := word.Draw(t, "a"), word.Draw(t, "b"), word.Draw(t, "c")
		ab := Distance(a, b)
		if (ab == 0) != (a == b) {
			t.Fatalf("Distance(%q, %q) = %d", a, b, ab)
		}
		if ba := Distance(b, a); ba != ab {
			t.Fatalf("Distance isn't symmetric: %d and %d", ab, ba)
		}
		if ab > max(len(a), len(b)) {
			t.Fatalf("Distance(%q, %q) = %d, more than the longer length", a, b, ab)
		}
		if ac, cb := Distance(a, c), Distance(c, b); ab > ac+cb {
			t.Fatalf("Distance breaks the triangle inequality: %d > %d + %d", ab, ac, cb)
		}
	})
}
