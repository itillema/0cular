package eval

import (
	"maps"
	"math"
	"math/big"
	"slices"
	"strings"
	"testing"

	"github.com/google/cel-go/common/types"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"pgregory.net/rapid"

	"github.com/itillema/0cular/contract"
)

// budgetContract returns a contract with the given diff budgets.
func budgetContract(t *testing.T, budgets string) *contract.Contract {
	t.Helper()
	return mustContract(t, `apiVersion: 0cular/v1alpha1
kind: Contract
metadata:
  name: budgets
artifact:
  format: json
  maxBytes: 1MiB
diffBudgets:
`+budgets, "")
}

// budgetCheck checks candidate against baseline and returns the only
// budget check.
func budgetCheck(t *testing.T, budgets, candidate, baseline string) Check {
	t.Helper()
	r := mustCheck(t, mustEvaluator(t, budgetContract(t, budgets)), candidate, &baseline)
	for _, c := range r.Checks {
		if c.Kind == KindBudget {
			return c
		}
	}
	t.Fatal("no budget check")
	return Check{}
}

func TestMeasureBudgets(t *testing.T) {
	tests := []struct {
		name                string
		budget              string
		baseline, candidate string
		status              Status
		evidence            string
		breached            []string
	}{
		{
			name:     "doubled",
			budget:   "  - {name: count, measure: size(self.items), maxChangePct: 20}\n",
			baseline: `{"items": [` + strings.Repeat("1,", 119) + `1]}`, candidate: `{"items": [` + strings.Repeat("1,", 239) + `1]}`,
			status: StatusNeedsApproval, evidence: "120 → 240 (+100%); budget ±20%", breached: []string{"maxChangePct"},
		},
		{
			name:     "exactly at the limit",
			budget:   "  - {name: count, measure: self.n, maxChangePct: 20}\n",
			baseline: `{"n": 120}`, candidate: `{"n": 144}`,
			status: StatusPass, evidence: "120 → 144 (+20%); budget ±20%",
		},
		{
			name:     "a decimal limit, exactly",
			budget:   "  - {name: ratio, measure: self.n, maxChangePct: 0.1}\n",
			baseline: `{"n": 1000}`, candidate: `{"n": 1001}`,
			status: StatusPass, evidence: "1000 → 1001 (+0.1%); budget ±0.1%",
		},
		{
			name:     "a small baseline counts as 1",
			budget:   "  - {name: ratio, measure: self.n, maxChangePct: 40}\n",
			baseline: `{"n": 0}`, candidate: `{"n": 0.5}`,
			status: StatusNeedsApproval, evidence: "0 → 0.5 (+50%); budget ±40%", breached: []string{"maxChangePct"},
		},
		{
			name:     "a decrease",
			budget:   "  - {name: drop, measure: self.n, maxIncrease: 50, maxDecrease: 20}\n",
			baseline: `{"n": 100}`, candidate: `{"n": 70}`,
			status: StatusNeedsApproval, evidence: "100 → 70 (-30); budget +50, -20", breached: []string{"maxDecrease"},
		},
		{
			name:     "every limit",
			budget:   "  - {name: all, measure: self.n, maxChangePct: 5, maxIncrease: 2}\n",
			baseline: `{"n": 10}`, candidate: `{"n": 12.5}`,
			status: StatusNeedsApproval, evidence: "10 → 12.5 (+25%, +2.5); budget ±5%, +2", breached: []string{"maxChangePct", "maxIncrease"},
		},
		{
			name:     "not overridable",
			budget:   "  - {name: strict, measure: self.n, maxChangePct: 10, overridable: false}\n",
			baseline: `{"n": 10}`, candidate: `{"n": 20}`,
			status: StatusFail, evidence: "10 → 20 (+100%); budget ±10%", breached: []string{"maxChangePct"},
		},
		{
			name:     "the artifact's size",
			budget:   "  - {name: size-stable, measure: meta.bytes, maxChangePct: 70}\n",
			baseline: `{"n": 1}`, candidate: `{"n": 100000}`,
			status: StatusPass, evidence: "8 → 13 (+62.5%); budget ±70%",
		},
		{
			name:     "a tiny change",
			budget:   "  - {name: tiny, measure: self.n, maxChangePct: 1}\n",
			baseline: `{"n": 100000}`, candidate: `{"n": 100001}`,
			status: StatusPass, evidence: "100000 → 100001 (+0.001%); budget ±1%",
		},
		{
			name:     "a failing measure",
			budget:   "  - {name: count, measure: size(self.items), maxChangePct: 20}\n",
			baseline: `{}`, candidate: `{"items": []}`,
			status: StatusError, evidence: "on the baseline: the expression failed: no such key: items",
		},
		{
			name:     "an infinite measure",
			budget:   "  - {name: ratio, measure: self.n / 0.0, maxChangePct: 20}\n",
			baseline: `{"n": 1}`, candidate: `{"n": 1}`,
			status: StatusError, evidence: "on the candidate: the measure is +Inf, not a finite number",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := budgetCheck(t, tt.budget, tt.candidate, tt.baseline)
			if got.Status != tt.status || got.Evidence != tt.evidence {
				t.Errorf("budget = %s, %q; want %s, %q", got.Status, got.Evidence, tt.status, tt.evidence)
			}
			var breached []string
			if got.Budget != nil {
				breached = got.Budget.Breached
			}
			if diff := cmp.Diff(tt.breached, breached); diff != "" {
				t.Errorf("Breached mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestCollectionBudgets(t *testing.T) {
	const baseline = `{"policies": [{"id": "a", "limit": 1}, {"id": "b", "limit": 2}, {"id": "c", "limit": 3}]}`
	tests := []struct {
		name      string
		budget    string
		candidate string
		status    Status
		evidence  string
		values    *BudgetValues
	}{
		{
			name:      "within the budget",
			budget:    "  - {name: churn, collection: self.policies, key: item.id, maxRemoved: 1, maxAdded: 1, maxChangedPct: 50}\n",
			candidate: `{"policies": [{"id": "b", "limit": 2}, {"id": "c", "limit": 30}, {"id": "d", "limit": 4}]}`,
			status:    StatusPass,
			evidence:  "3 → 3 items: 1 removed, 1 added, 1 changed (33.3%); budget at most 1 removed, 1 added, 50% changed",
			values:    &BudgetValues{BaselineItems: 3, CandidateItems: 3, Removed: 1, Added: 1, Changed: 1},
		},
		{
			name:      "over the budget",
			budget:    "  - {name: churn, collection: self.policies, key: item.id, maxRemoved: 0, maxChangedPct: 10}\n",
			candidate: `{"policies": [{"id": "b", "limit": 2}, {"id": "c", "limit": 30}]}`,
			status:    StatusNeedsApproval,
			evidence:  "3 → 2 items: 1 removed, 0 added, 1 changed (33.3%); budget at most 0 removed, 10% changed",
			values:    &BudgetValues{BaselineItems: 3, CandidateItems: 2, Removed: 1, Changed: 1, Breached: []string{"maxRemoved", "maxChangedPct"}},
		},
		{
			name:      "keys match as CEL compares them",
			budget:    "  - {name: churn, collection: self.policies, key: item.limit, maxAdded: 0, maxRemoved: 0}\n",
			candidate: `{"policies": [{"id": "x", "limit": 1.0}, {"id": "b", "limit": 2}, {"id": "c", "limit": 3}]}`,
			status:    StatusPass,
			evidence:  "3 → 3 items: 0 removed, 0 added, 1 changed (33.3%); budget at most 0 removed, 0 added",
			values:    &BudgetValues{BaselineItems: 3, CandidateItems: 3, Changed: 1},
		},
		{
			name:      "items that CEL built",
			budget:    "  - {name: churn, collection: \"self.policies.map(p, {'id': p.id, 'limit': p.limit})\", key: item.id, maxRemoved: 1, maxAdded: 1, maxChangedPct: 50}\n",
			candidate: `{"policies": [{"id": "b", "limit": 2}, {"id": "c", "limit": 30}, {"id": "d", "limit": 4}]}`,
			status:    StatusPass,
			evidence:  "3 → 3 items: 1 removed, 1 added, 1 changed (33.3%); budget at most 1 removed, 1 added, 50% changed",
			values:    &BudgetValues{BaselineItems: 3, CandidateItems: 3, Removed: 1, Added: 1, Changed: 1},
		},
		{
			name:      "a list of numbers",
			budget:    "  - {name: limits, collection: \"self.policies.map(p, p.limit)\", key: item, maxRemoved: 1}\n",
			candidate: `{"policies": [{"id": "a", "limit": 1}, {"id": "b", "limit": 5}]}`,
			status:    StatusNeedsApproval,
			evidence:  "3 → 2 items: 2 removed, 1 added, 0 changed (0%); budget at most 1 removed",
			values:    &BudgetValues{BaselineItems: 3, CandidateItems: 2, Removed: 2, Added: 1, Breached: []string{"maxRemoved"}},
		},
		{
			name:      "a repeated key",
			budget:    "  - {name: churn, collection: self.policies, key: item.id, maxAdded: 5}\n",
			candidate: `{"policies": [{"id": "a"}, {"id": "a"}]}`,
			status:    StatusError,
			evidence:  `on the candidate: two items have the key "a"`,
		},
		{
			name:      "a key that fails",
			budget:    "  - {name: churn, collection: self.policies, key: item.id, maxAdded: 5}\n",
			candidate: `{"policies": [{"name": "a"}]}`,
			status:    StatusError,
			evidence:  "on the candidate: the expression failed: no such key: id",
		},
		{
			name:      "not a list",
			budget:    "  - {name: churn, collection: self.policies, key: item, maxAdded: 5}\n",
			candidate: `{"policies": {"a": 1}}`,
			status:    StatusError,
			evidence:  "on the candidate: the expression returned the wrong kind of value: it returned a map, not a list",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := budgetCheck(t, tt.budget, tt.candidate, baseline)
			if got.Status != tt.status || got.Evidence != tt.evidence {
				t.Errorf("budget = %s, %q; want %s, %q", got.Status, got.Evidence, tt.status, tt.evidence)
			}
			if diff := cmp.Diff(tt.values, got.Budget); diff != "" {
				t.Errorf("Budget mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestCollectionBudgetsShareDiffs checks that budgets with the same
// collection and key share one diff, whose cost the first of them reports,
// and that a budget with another key makes its own.
func TestCollectionBudgetsShareDiffs(t *testing.T) {
	e := mustEvaluator(t, budgetContract(t, `  - {name: removals, collection: self.policies, key: item.id, maxRemoved: 0}
  - {name: changes, collection: self.policies, key: item.id, maxChangedPct: 10}
  - {name: by-limit, collection: self.policies, key: item.limit, maxRemoved: 0}
`))
	baseline := `{"policies": [{"id": "a", "limit": 1}, {"id": "b", "limit": 2}]}`
	r := mustCheck(t, e, `{"policies": [{"id": "b", "limit": 3}]}`, &baseline)
	byName := map[string]Check{}
	for _, c := range r.Checks {
		byName[c.Name] = c
	}
	removals, changes, byLimit := byName["removals"], byName["changes"], byName["by-limit"]
	shared := &BudgetValues{BaselineItems: 2, CandidateItems: 1, Removed: 1, Changed: 1}
	if diff := cmp.Diff(shared, removals.Budget, cmpopts.IgnoreFields(BudgetValues{}, "Breached")); diff != "" {
		t.Errorf("removals mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(shared, changes.Budget, cmpopts.IgnoreFields(BudgetValues{}, "Breached")); diff != "" {
		t.Errorf("changes mismatch (-want +got):\n%s", diff)
	}
	if removals.Cost == 0 || changes.Cost != 0 || byLimit.Cost == 0 {
		t.Errorf("costs = %d, %d, %d; want the shared diff's cost once, then 0, then the other diff's", removals.Cost, changes.Cost, byLimit.Cost)
	}
	if byLimit.Budget == nil || byLimit.Budget.Removed != 2 || byLimit.Budget.Added != 1 {
		t.Errorf("by-limit = %+v, want its own diff: 2 removed, 1 added", byLimit.Budget)
	}

	// A diff that fails fails every budget that shares it.
	r = mustCheck(t, e, `{"policies": [{"id": "a", "limit": 1}, {"id": "a", "limit": 2}]}`, &baseline)
	for _, c := range r.Checks[len(r.Checks)-3 : len(r.Checks)-1] {
		if c.Status != StatusError || c.Evidence != `on the candidate: two items have the key "a"` {
			t.Errorf("%s = %s, %q; want the shared diff's error", c.Name, c.Status, c.Evidence)
		}
	}
}

func TestBudgetsSkipped(t *testing.T) {
	e := mustEvaluator(t, budgetContract(t, "  - {name: count, measure: size(self.items), maxChangePct: 20}\n"))
	badBaseline := `{"items": [}`
	for _, tt := range []struct {
		name                string
		candidate, baseline string
		noBaseline          bool
		want                string
	}{
		{name: "no baseline", candidate: `{"items": []}`, noBaseline: true, want: "there's no baseline to compare with"},
		{name: "the candidate doesn't decode", candidate: `{"items": [}`, baseline: `{"items": []}`, want: "the candidate didn't decode"},
		{name: "the baseline doesn't decode", candidate: `{"items": []}`, baseline: badBaseline, want: "the baseline didn't decode"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var baseline *string
			if !tt.noBaseline {
				baseline = &tt.baseline
			}
			r := mustCheck(t, e, tt.candidate, baseline)
			got := r.Checks[len(r.Checks)-1]
			if got.Kind != KindBudget || got.Status != StatusSkipped || got.Evidence != tt.want || got.Budget != nil {
				t.Errorf("budget check = %+v, want skipped: %q", got, tt.want)
			}
		})
	}
}

func TestNewReportsBudgetProblems(t *testing.T) {
	c := budgetContract(t, `  - {name: measure, measure: "'text'", maxChangePct: 1}
  - {name: collection, collection: "1", key: item.id, maxAdded: 1}
  - {name: key, collection: self.items, key: self.id, maxAdded: 1}
`)
	c.Path = "budgets.yaml"
	_, err := New(c)
	want := []string{
		"budgets.yaml:9:30: diffBudgets[0].measure: the expression returns a string, but it must return a number",
		"budgets.yaml:10:36: diffBudgets[1].collection: the expression returns a number (int), but it must return a list",
		"budgets.yaml:11:46: diffBudgets[2].key: at 1:1 in the expression: undeclared reference to 'self'; a key reads only item, the element it identifies",
	}
	if err == nil || err.Error() != strings.Join(want, "\n") {
		t.Errorf("New() error =\n%v\nwant:\n%s", err, strings.Join(want, "\n"))
	}
}

func TestSigned(t *testing.T) {
	for f, want := range map[float64]string{
		0: "0", 100: "+100", -30: "-30", 33.333: "+33.3", -3.35: "-3.4", 0.04: "+0.04", -0.001: "-0.001", 2.5: "+2.5",
	} {
		if got := signed(f); got != want {
			t.Errorf("signed(%v) = %q, want %q", f, got, want)
		}
	}
}

// TestWithinPct checks the change-percentage limit against integer
// arithmetic: a change of delta from base is within pct exactly when
// delta × 100 ≤ pct × max(|base|, 1). Floating-point division would put
// some changes right at a limit on the wrong side of it.
func TestWithinPct(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		base := rapid.Int64Range(-1_000_000, 1_000_000).Draw(t, "base")
		delta := rapid.Int64Range(0, 2_000_000).Draw(t, "delta")
		pct := rapid.Int64Range(0, 500).Draw(t, "pct")
		denom := max(abs(base), 1)
		want := delta*100 <= pct*denom
		for _, cand := range []int64{base + delta, base - delta} {
			if got := withinPct(new(big.Rat).Abs(big.NewRat(cand-base, 1)), rat(math.Abs(float64(base))), float64(pct)); got != want {
				t.Fatalf("withinPct(|%d - %d|, %d%%) = %v, want %v", cand, base, pct, got, want)
			}
			b := contract.DiffBudget{MaxChangePct: ptr(float64(pct))}
			if breached := measureBreaches(b, float64(base), float64(cand)); (len(breached) == 0) != want {
				t.Fatalf("measureBreaches(%d → %d, %d%%) = %v, want within: %v", base, cand, pct, breached, want)
			}
		}
	})
}

func abs(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}

// TestMeasureBreachesProperties checks properties of the budget math on
// arbitrary doubles: no change is within any budget; a change within a
// budget is within every larger one; and an increase and a decrease of the
// same size are within the same change-percentage limit.
func TestMeasureBreachesProperties(t *testing.T) {
	finite := rapid.Float64Range(-1e12, 1e12)
	rapid.Check(t, func(t *rapid.T) {
		base, cand := finite.Draw(t, "base"), finite.Draw(t, "cand")
		pct := rapid.Float64Range(0, 1e4).Draw(t, "pct")
		more := pct + rapid.Float64Range(0, 1e4).Draw(t, "more")
		limits := contract.DiffBudget{MaxChangePct: &pct, MaxIncrease: &pct, MaxDecrease: &pct}
		if breached := measureBreaches(limits, base, base); len(breached) != 0 {
			t.Fatalf("no change breaches %v", breached)
		}
		within := len(measureBreaches(contract.DiffBudget{MaxChangePct: &pct}, base, cand)) == 0
		if within && len(measureBreaches(contract.DiffBudget{MaxChangePct: &more}, base, cand)) != 0 {
			t.Fatalf("%v → %v is within %v%% but not %v%%", base, cand, pct, more)
		}
		mirrored := base - (cand - base)
		if math.IsInf(mirrored, 0) {
			return
		}
		if mirror := len(measureBreaches(contract.DiffBudget{MaxChangePct: &pct}, base, mirrored)) == 0; mirror != within {
			t.Fatalf("%v → %v and %v → %v disagree about %v%%", base, cand, base, mirrored, pct)
		}
	})
}

// TestDiffItems checks the collection diff against sets: removed and added
// count the keys only one side holds, changed the keys both hold with
// different items; swapping the sides swaps removed and added.
func TestDiffItems(t *testing.T) {
	keys := rapid.SampledFrom([]string{"a", "b", "c", "d", "e"})
	items := rapid.MapOfN(keys, rapid.IntRange(0, 2), 0, 5)
	rapid.Check(t, func(t *rapid.T) {
		base, cand := items.Draw(t, "base"), items.Draw(t, "cand")
		baseItems, candItems := collectionItems(t, "base", base), collectionItems(t, "cand", cand)
		removed, added, changed := diffItems(baseItems, candItems)
		var wantRemoved, wantAdded, wantChanged int
		for k, v := range base {
			if c, ok := cand[k]; !ok {
				wantRemoved++
			} else if c != v {
				wantChanged++
			}
		}
		for k := range cand {
			if _, ok := base[k]; !ok {
				wantAdded++
			}
		}
		if removed != wantRemoved || added != wantAdded || changed != wantChanged {
			t.Fatalf("diffItems() = %d removed, %d added, %d changed; want %d, %d, %d", removed, added, changed, wantRemoved, wantAdded, wantChanged)
		}
		if r, a, c := diffItems(candItems, baseItems); r != added || a != removed || c != changed {
			t.Fatalf("swapped diffItems() = %d, %d, %d; want %d, %d, %d", r, a, c, added, removed, changed)
		}
		if r, a, c := diffItems(baseItems, baseItems); r+a+c != 0 {
			t.Fatalf("diffItems(x, x) = %d, %d, %d; want no changes", r, a, c)
		}
	})
}

// collectionItems turns drawn items into a collection's items, each either
// a decoded value or a CEL value; diffItems must compare them the same.
func collectionItems(t *rapid.T, label string, m map[string]int) map[string]any {
	out := make(map[string]any, len(m))
	for _, k := range slices.Sorted(maps.Keys(m)) {
		if rapid.Bool().Draw(t, label+" "+k+" is decoded") {
			out[k] = float64(m[k])
		} else {
			out[k] = types.Int(m[k])
		}
	}
	return out
}

func TestEqualItems(t *testing.T) {
	decodedMap := map[string]any{"id": "a", "tags": []any{"x", 1.0, nil, true}}
	tests := []struct {
		name string
		a, b any
		want bool
	}{
		{"equal decoded maps", decodedMap, map[string]any{"id": "a", "tags": []any{"x", 1.0, nil, true}}, true},
		{"a nested difference", decodedMap, map[string]any{"id": "a", "tags": []any{"x", 2.0, nil, true}}, false},
		{"a missing key", decodedMap, map[string]any{"id": "a", "other": 1.0}, false},
		{"a decoded map and a CEL map", decodedMap, types.DefaultTypeAdapter.NativeToValue(decodedMap), true},
		{"a double and an equal int", 1.0, types.Int(1), true},
		{"a string and a number", "1", 1.0, false},
		{"null and null", nil, types.NullValue, true},
		{"lists holding CEL values", []any{types.Bytes("x")}, []any{types.Bytes("x")}, true},
		{"lists of different lengths", []any{1.0}, []any{1.0, 2.0}, false},
		{"a list and a map", []any{}, map[string]any{}, false},
	}
	for _, tt := range tests {
		if got := equalItems(tt.a, tt.b); got != tt.want {
			t.Errorf("%s: equalItems() = %v, want %v", tt.name, got, tt.want)
		}
		if got := equalItems(tt.b, tt.a); got != tt.want {
			t.Errorf("%s, swapped: equalItems() = %v, want %v", tt.name, got, tt.want)
		}
	}
}

// TestBudgetPercentagesDisplay checks that evidence rounds percentages
// without changing the outcome.
func TestBudgetPercentagesDisplay(t *testing.T) {
	for base, want := range map[float64]string{3: "+33.3%", 6: "+16.7%", 7: "+14.3%"} {
		got := measureEvidence(contract.DiffBudget{MaxChangePct: ptr(50.0)}, &BudgetValues{Baseline: base, Candidate: base + 1})
		if !strings.Contains(got, "("+want+")") {
			t.Errorf("measureEvidence(%v → %v) = %q, want it to show %s", base, base+1, got, want)
		}
	}
}
