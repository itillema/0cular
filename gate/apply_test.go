package gate

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"pgregory.net/rapid"

	"github.com/itillema/0cular/eval"
)

// TestApplyExpiry checks that an approval applies until the moment it
// expires, and not at that moment.
func TestApplyExpiry(t *testing.T) {
	expires := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	for _, tt := range []struct {
		now  time.Time
		want string
	}{
		{expires.Add(-time.Nanosecond), ""},
		{expires, "it expired at 2026-11-04T12:00:00Z"},
		{expires.Add(time.Hour), "it expired at 2026-11-04T12:00:00Z"},
	} {
		r := &eval.Result{Verdict: eval.NeedsApproval, Checks: []eval.Check{
			{Kind: eval.KindBudget, Name: "b", Status: eval.StatusNeedsApproval, Budget: &eval.BudgetValues{Baseline: 1, Candidate: 2}},
		}}
		a := eval.Approval{By: "alice", Reason: "why", Expires: expires, Budgets: breached(r)}
		apply(r, []eval.Approval{a}, tt.now)
		if got := r.Approvals[0].Rejection; got != tt.want {
			t.Errorf("at %s, rejection = %q, want %q", tt.now, got, tt.want)
		}
	}
}

// TestApplyProperties checks, for random results and approvals, that an
// approval applies exactly when the result needs approval and nothing the
// approval binds to differs, whatever the order of its budgets; and that
// applying it changes only the verdict and the breached budgets.
func TestApplyProperties(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	rapid.Check(t, func(t *rapid.T) {
		r := &eval.Result{
			ContractDigest: "sha256:contract",
			Candidate:      eval.Artifact{Digest: "sha256:candidate"},
			Checks:         []eval.Check{{Kind: eval.KindEnvelope, Name: "candidate", Status: eval.StatusPass}},
		}
		for i := range rapid.IntRange(1, 4).Draw(t, "budgets") {
			check := eval.Check{
				Kind:   eval.KindBudget,
				Name:   fmt.Sprintf("b%d", i),
				Status: rapid.SampledFrom([]eval.Status{eval.StatusPass, eval.StatusNeedsApproval, eval.StatusFail}).Draw(t, "status"),
				Budget: &eval.BudgetValues{
					Baseline:  float64(rapid.IntRange(-2, 2).Draw(t, "baseline")),
					Candidate: float64(rapid.IntRange(-2, 2).Draw(t, "candidate")),
					Added:     rapid.IntRange(0, 2).Draw(t, "added"),
				},
			}
			r.Checks = append(r.Checks, check)
		}
		r.Verdict = verdictOf(r.Checks)

		// Start from the approval that matches r, and change some of what
		// it binds to.
		a := eval.Approval{By: "alice", Reason: "why", Expires: now.Add(time.Hour), CandidateDigest: "sha256:candidate", ContractDigest: "sha256:contract", Budgets: breached(r)}
		changed := false
		change := func(label string, f func()) {
			if rapid.Bool().Draw(t, label) {
				f()
				changed = true
			}
		}
		change("another candidate", func() { a.CandidateDigest = "sha256:other" })
		change("another contract", func() { a.ContractDigest = "sha256:other" })
		change("no approver", func() { a.By = rapid.SampledFrom([]string{"", " ", "\t\n"}).Draw(t, "by") })
		change("no reason", func() { a.Reason = "" })
		change("no expiry", func() { a.Expires = time.Time{} })
		change("expired", func() { a.Expires = now.Add(-time.Duration(rapid.IntRange(0, 10).Draw(t, "ago")) * time.Minute) })
		if len(a.Budgets) > 0 {
			change("a budget dropped", func() {
				i := rapid.IntRange(0, len(a.Budgets)-1).Draw(t, "dropped")
				a.Budgets = slices.Delete(a.Budgets, i, i+1)
			})
		}
		if len(a.Budgets) > 0 {
			change("a value changed", func() {
				a.Budgets[rapid.IntRange(0, len(a.Budgets)-1).Draw(t, "changed")].Candidate += 0.5
			})
		}
		change("a budget added", func() {
			names := []string{"unknown"}
			for _, c := range r.Checks {
				if c.Kind == eval.KindBudget && c.Status != eval.StatusNeedsApproval {
					names = append(names, c.Name)
				}
			}
			a.Budgets = append(a.Budgets, eval.ApprovedBudget{Name: rapid.SampledFrom(names).Draw(t, "added name")})
		})
		a.Budgets = rapid.Permutation(a.Budgets).Draw(t, "order")

		before := slices.Clone(r.Checks)
		verdict := r.Verdict
		apply(r, []eval.Approval{a}, now)

		wantApplied := verdict == eval.NeedsApproval && !changed
		got := r.Approvals[0]
		if got.Applied != wantApplied || (got.Rejection == "") != wantApplied {
			t.Fatalf("applied = %t with rejection %q, want applied = %t; verdict %s, changed %t, approval %+v", got.Applied, got.Rejection, wantApplied, verdict, changed, a)
		}
		wantVerdict := verdict
		if wantApplied {
			wantVerdict = eval.Pass
		}
		if r.Verdict != wantVerdict {
			t.Fatalf("verdict = %s, want %s", r.Verdict, wantVerdict)
		}
		for i, c := range r.Checks {
			want := before[i].Status
			if wantApplied && want == eval.StatusNeedsApproval {
				want = eval.StatusApproved
			}
			if c.Status != want {
				t.Fatalf("check %s status = %s, want %s", c.Name, c.Status, want)
			}
		}
	})
}

// verdictOf combines statuses as eval does.
func verdictOf(checks []eval.Check) eval.Verdict {
	v := eval.Pass
	for _, c := range checks {
		switch {
		case c.Status.Blocks():
			return eval.Block
		case c.Status == eval.StatusNeedsApproval:
			v = eval.NeedsApproval
		}
	}
	return v
}
