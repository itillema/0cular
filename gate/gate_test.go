package gate_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/itillema/0cular/contract"
	"github.com/itillema/0cular/eval"
	"github.com/itillema/0cular/gate"
)

// limitsContract has an invariant, a measure budget and a collection
// budget.
const limitsContract = `apiVersion: 0cular/v1alpha1
kind: Contract
metadata: {name: limits}
artifact: {format: json, maxBytes: 4KiB}
report: {redact: [pin]}
invariants:
  - {name: limit-positive, expr: "self.limit > 0", message: the consumer divides by the limit}
diffBudgets:
  - {name: limit-stable, measure: self.limit, maxChangePct: 50}
  - {name: policy-churn, collection: self.policies, key: item.id, maxAdded: 1}
  - {name: pin-stable, measure: self.pin, maxChangePct: 10}
`

// The versions the tests check. over breaches limit-stable, and overBoth
// policy-churn too; zero breaks limit-positive.
const (
	baseline = `{"limit": 100, "pin": 1234, "policies": [{"id": "a"}]}`
	within   = `{"limit": 120, "pin": 1234, "policies": [{"id": "a"}]}`
	over     = `{"limit": 200, "pin": 1234, "policies": [{"id": "a"}]}`
	overBoth = `{"limit": 200, "pin": 1234, "policies": [{"id": "a"}, {"id": "b"}, {"id": "c"}]}`
	zero     = `{"limit": 0, "pin": 1234, "policies": [{"id": "a"}]}`
)

func mustContract(t testing.TB, text string) *contract.Contract {
	t.Helper()
	c, err := contract.Parse([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func input(data string) eval.Input {
	return eval.Input{Data: []byte(data), Key: "limits.json"}
}

func mustCheck(t testing.TB, c *contract.Contract, candidate, base string, opts ...gate.Option) *eval.Result {
	t.Helper()
	b := input(base)
	r, err := gate.Check(context.Background(), c, input(candidate), &b, opts...)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// mustApprove checks candidate and approves what it breaches, for an hour.
func mustApprove(t testing.TB, c *contract.Contract, candidate string) eval.Approval {
	t.Helper()
	a, err := gate.Approve(mustCheck(t, c, candidate, baseline), "alice", "the limit doubles on purpose", time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// orDefault returns s, or def if s is empty.
func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// statuses returns each check's name and status.
func statuses(r *eval.Result) []string {
	var s []string
	for _, c := range r.Checks {
		s = append(s, c.Name+" "+string(c.Status))
	}
	return s
}

func TestCheckWithoutApprovals(t *testing.T) {
	c := mustContract(t, limitsContract)
	for candidate, want := range map[string]eval.Verdict{within: eval.Pass, over: eval.NeedsApproval, zero: eval.Block} {
		r := mustCheck(t, c, candidate, baseline)
		if r.Verdict != want || r.Approvals != nil {
			t.Errorf("Check(%s) = %s with approvals %v, want %s and none", candidate, r.Verdict, r.Approvals, want)
		}
	}
}

func TestApprove(t *testing.T) {
	c := mustContract(t, limitsContract)
	expires := time.Now().Add(time.Hour)
	r := mustCheck(t, c, overBoth, baseline)
	a, err := gate.Approve(r, "alice", "the limit doubles on purpose", expires)
	if err != nil {
		t.Fatal(err)
	}
	want := eval.Approval{
		By: "alice", Reason: "the limit doubles on purpose", Expires: expires,
		CandidateDigest: r.Candidate.Digest, ContractDigest: c.Digest,
		Budgets: []eval.ApprovedBudget{
			{Name: "limit-stable", Baseline: 100, Candidate: 200},
			{Name: "policy-churn", BaselineItems: 1, CandidateItems: 3, Added: 2},
		},
	}
	if diff := cmp.Diff(want, a); diff != "" {
		t.Errorf("Approve() differs (-want +got):\n%s", diff)
	}
}

func TestCheckApplies(t *testing.T) {
	c := mustContract(t, limitsContract)
	a := mustApprove(t, c, overBoth)
	r := mustCheck(t, c, overBoth, baseline, gate.WithApproval(a))
	if r.Verdict != eval.Pass {
		t.Errorf("verdict = %s, want PASS", r.Verdict)
	}
	want := []string{
		"candidate pass", "baseline pass", "limit-positive pass",
		"limit-stable approved", "policy-churn approved", "pin-stable pass",
	}
	if diff := cmp.Diff(want, statuses(r)); diff != "" {
		t.Errorf("statuses differ (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]eval.ApprovalOutcome{{Approval: a, Applied: true}}, r.Approvals); diff != "" {
		t.Errorf("approvals differ (-want +got):\n%s", diff)
	}
}

// TestRejections checks that a change to anything an approval binds to
// stops it from applying, and says why.
func TestRejections(t *testing.T) {
	c := mustContract(t, limitsContract)
	// A comment changes the contract's digest, and nothing else.
	edited := mustContract(t, limitsContract+"# edited\n")
	past := time.Date(2001, 2, 3, 4, 5, 6, 0, time.UTC)
	// Each test approves a candidate, over unless it names another, then
	// checks a candidate, the approved one unless it names another, under
	// the contract against the baseline, unless it names others.
	tests := []struct {
		name                          string
		approved, candidate, baseline string
		contract                      *contract.Contract
		change                        func(a *eval.Approval)
		wantVerdict                   eval.Verdict
		want                          string
	}{
		{name: "blocked", candidate: zero, wantVerdict: eval.Block, want: "the candidate is blocked, which no approval can accept"},
		{name: "nothing to approve", candidate: within, wantVerdict: eval.Pass, want: "nothing needs approval"},
		{name: "no approver", change: func(a *eval.Approval) { a.By = "" }, want: "it doesn't say who approved"},
		{name: "blank approver", change: func(a *eval.Approval) { a.By = " \t\n" }, want: "it doesn't say who approved"},
		{name: "no reason", change: func(a *eval.Approval) { a.Reason = "" }, want: "it doesn't say why"},
		{name: "no expiry", change: func(a *eval.Approval) { a.Expires = time.Time{} }, want: "it has no expiry"},
		{name: "expired", change: func(a *eval.Approval) { a.Expires = past }, want: "it expired at 2001-02-03T04:05:06Z"},
		{name: "another candidate", candidate: strings.Replace(over, " ", "  ", 1), want: "it's for another candidate"},
		{name: "another contract", contract: edited, want: "it's for another version of the contract"},
		{name: "another baseline", baseline: strings.Replace(baseline, "100", "110", 1), want: "limit-stable measured other values than the approved ones"},
		{
			name: "another approved value", change: func(a *eval.Approval) { a.Budgets[0].Candidate = 199 },
			want: "limit-stable measured other values than the approved ones",
		},
		{name: "a budget missing", change: func(a *eval.Approval) { a.Budgets = nil }, want: "it doesn't cover limit-stable, which needs approval"},
		{
			name: "a budget too many", change: func(a *eval.Approval) { a.Budgets = append(a.Budgets, eval.ApprovedBudget{Name: "policy-churn"}) },
			want: "it covers policy-churn, which doesn't need approval",
		},
		{
			name: "a budget twice", change: func(a *eval.Approval) { a.Budgets = append(a.Budgets, a.Budgets[0]) },
			want: "it lists limit-stable twice",
		},
		{
			name: "another collection count", approved: overBoth, change: func(a *eval.Approval) { a.Budgets[1].CandidateItems, a.Budgets[1].Added = 4, 3 },
			want: "policy-churn measured other values than the approved ones",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			approved := orDefault(tt.approved, over)
			a := mustApprove(t, c, approved)
			if tt.change != nil {
				tt.change(&a)
			}
			checked := c
			if tt.contract != nil {
				checked = tt.contract
			}
			candidate, base := orDefault(tt.candidate, approved), orDefault(tt.baseline, baseline)
			unapproved := mustCheck(t, checked, candidate, base)
			r := mustCheck(t, checked, candidate, base, gate.WithApproval(a))
			wantVerdict := tt.wantVerdict
			if wantVerdict == "" {
				wantVerdict = eval.NeedsApproval
			}
			if r.Verdict != wantVerdict {
				t.Errorf("verdict = %s, want %s", r.Verdict, wantVerdict)
			}
			if diff := cmp.Diff(statuses(unapproved), statuses(r)); diff != "" {
				t.Errorf("an approval that doesn't apply changed the statuses (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff([]eval.ApprovalOutcome{{Approval: a, Rejection: tt.want}}, r.Approvals); diff != "" {
				t.Errorf("approvals differ (-want +got):\n%s", diff)
			}
		})
	}
}

// TestApprovalOrder checks that an approval's budgets can come in any
// order.
func TestApprovalOrder(t *testing.T) {
	c := mustContract(t, limitsContract)
	a := mustApprove(t, c, overBoth)
	a.Budgets[0], a.Budgets[1] = a.Budgets[1], a.Budgets[0]
	if r := mustCheck(t, c, overBoth, baseline, gate.WithApproval(a)); r.Verdict != eval.Pass {
		t.Errorf("verdict = %s, want PASS; approvals: %+v", r.Verdict, r.Approvals)
	}
}

// TestMultipleApprovals checks that one approval that applies is enough,
// and that the result records every approval in the order given.
func TestMultipleApprovals(t *testing.T) {
	c := mustContract(t, limitsContract)
	stale := mustApprove(t, c, over)
	stale.By = "bob"
	stale.Budgets[0].Candidate = 150
	fresh := mustApprove(t, c, over)
	r := mustCheck(t, c, over, baseline, gate.WithApproval(stale), gate.WithApproval(fresh))
	if r.Verdict != eval.Pass {
		t.Errorf("verdict = %s, want PASS", r.Verdict)
	}
	want := []eval.ApprovalOutcome{
		{Approval: stale, Rejection: "limit-stable measured other values than the approved ones"},
		{Approval: fresh, Applied: true},
	}
	if diff := cmp.Diff(want, r.Approvals); diff != "" {
		t.Errorf("approvals differ (-want +got):\n%s", diff)
	}
}

// TestWithApprovalCopies checks that changing an approval's budgets after
// passing it to WithApproval changes nothing.
func TestWithApprovalCopies(t *testing.T) {
	c := mustContract(t, limitsContract)
	a := mustApprove(t, c, over)
	opt := gate.WithApproval(a)
	a.Budgets[0].Candidate = 1
	if r := mustCheck(t, c, over, baseline, opt); r.Verdict != eval.Pass {
		t.Errorf("verdict = %s, want PASS; approvals: %+v", r.Verdict, r.Approvals)
	}
}

// TestApprovalJSON checks that an approval keeps applying after a trip
// through JSON, which is how it travels, and the names of its fields.
func TestApprovalJSON(t *testing.T) {
	c := mustContract(t, limitsContract)
	a := mustApprove(t, c, overBoth)
	a.Expires = time.Date(2101, 2, 3, 4, 5, 6, 789, time.FixedZone("CET", 3600))
	data, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	wantFields := map[string]any{
		"by": "alice", "reason": "the limit doubles on purpose", "expires": "2101-02-03T04:05:06.000000789+01:00",
		"candidateDigest": a.CandidateDigest, "contractDigest": c.Digest,
		"budgets": []any{
			map[string]any{"name": "limit-stable", "baseline": 100.0, "candidate": 200.0},
			map[string]any{"name": "policy-churn", "baselineItems": 1.0, "candidateItems": 3.0, "added": 2.0},
		},
	}
	if diff := cmp.Diff(wantFields, fields); diff != "" {
		t.Errorf("the approval's JSON differs (-want +got):\n%s", diff)
	}
	var back eval.Approval
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	if r := mustCheck(t, c, overBoth, baseline, gate.WithApproval(back)); r.Verdict != eval.Pass {
		t.Errorf("verdict = %s, want PASS; approvals: %+v", r.Verdict, r.Approvals)
	}
}

// TestApprovalRedacted checks that an approval binds to a measure's values
// even when reports redact them (ADR-0015).
func TestApprovalRedacted(t *testing.T) {
	c := mustContract(t, limitsContract)
	pinChanged := strings.Replace(within, "1234", "2000", 1)
	r := mustCheck(t, c, pinChanged, baseline)
	if r.Verdict != eval.NeedsApproval || r.Checks[5].Budget == nil || !r.Checks[5].Budget.Redacted {
		t.Fatalf("Check() = %s with %+v, want NEEDS_APPROVAL with pin-stable redacted", r.Verdict, r.Checks[5])
	}
	a, err := gate.Approve(r, "alice", "a new pin", time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if want := []eval.ApprovedBudget{{Name: "pin-stable", Baseline: 1234, Candidate: 2000}}; !cmp.Equal(want, a.Budgets) {
		t.Errorf("budgets = %+v, want %+v", a.Budgets, want)
	}
	if r := mustCheck(t, c, pinChanged, baseline, gate.WithApproval(a)); r.Verdict != eval.Pass {
		t.Errorf("verdict = %s, want PASS", r.Verdict)
	}
	other := strings.Replace(baseline, "1234", "1300", 1)
	if r := mustCheck(t, c, pinChanged, other, gate.WithApproval(a)); r.Verdict != eval.NeedsApproval {
		t.Errorf("against another baseline, verdict = %s, want NEEDS_APPROVAL", r.Verdict)
	}
}

func TestApproveErrors(t *testing.T) {
	c := mustContract(t, limitsContract)
	needs := mustCheck(t, c, over, baseline)
	later := time.Now().Add(time.Hour)
	tests := []struct {
		name        string
		r           *eval.Result
		by, reason  string
		expires     time.Time
		wantErr     error
		wantMessage string
	}{
		{name: "pass", r: mustCheck(t, c, within, baseline), by: "alice", reason: "ok", expires: later, wantErr: gate.ErrNotApprovable, wantMessage: "gate: only a NEEDS_APPROVAL result can be approved, and this one is PASS"},
		{name: "block", r: mustCheck(t, c, zero, baseline), by: "alice", reason: "ok", expires: later, wantErr: gate.ErrNotApprovable, wantMessage: "this one is BLOCK"},
		{name: "no approver", r: needs, reason: "ok", expires: later, wantErr: gate.ErrInvalidApproval, wantMessage: "gate: invalid approval: it doesn't say who approved"},
		{name: "no reason", r: needs, by: "alice", expires: later, wantErr: gate.ErrInvalidApproval, wantMessage: "it doesn't say why"},
		{name: "no expiry", r: needs, by: "alice", reason: "ok", wantErr: gate.ErrInvalidApproval, wantMessage: "it has no expiry"},
		{name: "expired", r: needs, by: "alice", reason: "ok", expires: time.Now().Add(-time.Second), wantErr: gate.ErrInvalidApproval, wantMessage: "it expired at "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, err := gate.Approve(tt.r, tt.by, tt.reason, tt.expires)
			if !errors.Is(err, tt.wantErr) || !strings.Contains(err.Error(), tt.wantMessage) {
				t.Errorf("Approve() error = %v, want %v containing %q", err, tt.wantErr, tt.wantMessage)
			}
			if !cmp.Equal(a, eval.Approval{}) {
				t.Errorf("Approve() = %+v with an error, want the zero Approval", a)
			}
		})
	}
}

func TestCheckContractError(t *testing.T) {
	c := mustContract(t, strings.Replace(limitsContract, "self.limit > 0", "selff.limit > 0", 1))
	_, err := gate.Check(t.Context(), c, input(within), nil)
	var invalid *contract.Error
	if !errors.As(err, &invalid) {
		t.Errorf("Check() error = %v, want a *contract.Error", err)
	}
}

func TestCheckCanceled(t *testing.T) {
	c := mustContract(t, limitsContract)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := gate.Check(ctx, c, input(within), nil); !errors.Is(err, context.Canceled) {
		t.Errorf("Check() with a canceled context error = %v, want context.Canceled", err)
	}
}
