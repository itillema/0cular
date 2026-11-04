// Package gate checks a candidate version of an artifact before it's
// published, and applies the approvals that accept its breached diff
// budgets.
//
// Check runs eval's checks: the verdict is BLOCK if any check fails,
// NEEDS_APPROVAL if a diff budget that an approval can accept is breached,
// and PASS otherwise. An eval.Approval turns NEEDS_APPROVAL into PASS. It
// binds to the candidate's digest, the contract's digest and each breached
// budget's name and values, so a change to any of them invalidates it, and
// it expires (ADR-0016). Nothing turns BLOCK into PASS.
//
// How an approval is obtained is the caller's job: a CI environment
// approval, chatops, or a person at a terminal. Approve makes one from the
// result the approver saw, and a later Check applies it if nothing it binds
// to has changed. The result records every approval given, and whether it
// applied.
//
// The package is experimental: its API may change before 0cular 1.0.
package gate

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/itillema/0cular/contract"
	"github.com/itillema/0cular/eval"
)

var (
	// ErrNotApprovable is the error Approve returns for a result whose
	// verdict isn't NEEDS_APPROVAL.
	ErrNotApprovable = errors.New("gate: only a NEEDS_APPROVAL result can be approved")
	// ErrInvalidApproval is the error Approve returns for an approval that
	// could never apply: one that doesn't say who approved or why, or that
	// has no expiry or has expired.
	ErrInvalidApproval = errors.New("gate: invalid approval")
)

// Option configures Check.
type Option func(*options)

type options struct {
	approvals []eval.Approval
}

// WithApproval gives Check an approval to apply. Check can take any number
// of them; one that applies is enough.
func WithApproval(a eval.Approval) Option {
	a.Budgets = slices.Clone(a.Budgets)
	return func(o *options) {
		o.approvals = append(o.approvals, a)
	}
}

// Check checks candidate against c, and against baseline when it isn't
// nil, as eval.Evaluator.Check does, then applies the approvals that opts
// give. When the verdict is NEEDS_APPROVAL and an approval applies, the
// verdict becomes PASS, and each breached budget's status
// eval.StatusApproved. The result's Approvals lists every approval given,
// whether it applied, and why not.
//
// c should come from contract.LoadFile. A schema or expression in it that
// doesn't compile is a *contract.Error. Problems with either version are
// results, not errors: otherwise, Check returns an error only when ctx ends
// first.
func Check(ctx context.Context, c *contract.Contract, candidate eval.Input, baseline *eval.Input, opts ...Option) (*eval.Result, error) {
	var o options
	for _, opt := range opts {
		opt(&o)
	}
	e, err := eval.New(c)
	if err != nil {
		return nil, err
	}
	r, err := e.Check(ctx, candidate, baseline)
	if err != nil {
		return nil, err
	}
	apply(r, o.approvals, time.Now())
	return r, nil
}

// Approve returns an approval by who, for why, that expires at expires, of
// the budgets that r breaches. r is the result the approver saw, and its
// verdict must be NEEDS_APPROVAL. The approval accepts exactly what r
// measured: Check applies it to that candidate under that contract, and
// only while the budgets measure the same values.
func Approve(r *eval.Result, by, reason string, expires time.Time) (eval.Approval, error) {
	if r.Verdict != eval.NeedsApproval {
		return eval.Approval{}, fmt.Errorf("%w, and this one is %s", ErrNotApprovable, r.Verdict)
	}
	a := eval.Approval{
		By:              by,
		Reason:          reason,
		Expires:         expires,
		CandidateDigest: r.Candidate.Digest,
		ContractDigest:  r.ContractDigest,
		Budgets:         breached(r),
	}
	if problem := unusable(a, time.Now()); problem != "" {
		return eval.Approval{}, fmt.Errorf("%w: %s", ErrInvalidApproval, problem)
	}
	return a, nil
}

// apply judges each approval against r at the time now, records it in r,
// and, if one applies, approves r's breached budgets.
func apply(r *eval.Result, approvals []eval.Approval, now time.Time) {
	want := breached(r)
	applied := false
	for _, a := range approvals {
		rejection := reject(r, want, a, now)
		r.Approvals = append(r.Approvals, eval.ApprovalOutcome{Approval: a, Applied: rejection == "", Rejection: rejection})
		applied = applied || rejection == ""
	}
	if !applied {
		return
	}
	for i := range r.Checks {
		if r.Checks[i].Status == eval.StatusNeedsApproval {
			r.Checks[i].Status = eval.StatusApproved
		}
	}
	r.Verdict = eval.Pass
}

// reject says why approval a doesn't apply to r at the time now, or
// returns "" if it applies. want lists the budgets r breaches.
func reject(r *eval.Result, want []eval.ApprovedBudget, a eval.Approval, now time.Time) string {
	switch r.Verdict {
	case eval.Block:
		return "the candidate is blocked, which no approval can accept"
	case eval.Pass:
		return "nothing needs approval"
	}
	if problem := unusable(a, now); problem != "" {
		return problem
	}
	switch {
	case a.CandidateDigest != r.Candidate.Digest:
		return "it's for another candidate"
	case a.ContractDigest != r.ContractDigest:
		return "it's for another version of the contract"
	}
	return budgetMismatch(want, a.Budgets)
}

// unusable says why an approval could never apply, whatever it's for, at
// the time now, or returns "" if nothing rules it out.
func unusable(a eval.Approval, now time.Time) string {
	switch {
	case strings.TrimSpace(a.By) == "":
		return "it doesn't say who approved"
	case strings.TrimSpace(a.Reason) == "":
		return "it doesn't say why"
	case a.Expires.IsZero():
		return "it has no expiry"
	case !now.Before(a.Expires):
		return "it expired at " + a.Expires.UTC().Format(time.RFC3339)
	}
	return ""
}

// budgetMismatch says how the budgets an approval accepts, got, differ
// from the budgets a result breaches, want, or returns "" if they're the
// same, in any order.
func budgetMismatch(want, got []eval.ApprovedBudget) string {
	approved := make(map[string]eval.ApprovedBudget, len(got))
	for _, b := range got {
		if _, dup := approved[b.Name]; dup {
			return "it lists " + b.Name + " twice"
		}
		approved[b.Name] = b
	}
	for _, w := range want {
		b, ok := approved[w.Name]
		switch {
		case !ok:
			return "it doesn't cover " + w.Name + ", which needs approval"
		case b != w:
			return w.Name + " measured other values than the approved ones"
		}
		delete(approved, w.Name)
	}
	for _, b := range got {
		if _, left := approved[b.Name]; left {
			return "it covers " + b.Name + ", which doesn't need approval"
		}
	}
	return ""
}

// breached returns the budgets that r breaches and an approval can accept,
// with what they measured.
func breached(r *eval.Result) []eval.ApprovedBudget {
	var budgets []eval.ApprovedBudget
	for _, c := range r.Checks {
		if c.Kind == eval.KindBudget && c.Status == eval.StatusNeedsApproval {
			budgets = append(budgets, approvedBudget(c))
		}
	}
	return budgets
}

// approvedBudget returns a budget check's name and values.
func approvedBudget(c eval.Check) eval.ApprovedBudget {
	b := eval.ApprovedBudget{Name: c.Name}
	if v := c.Budget; v != nil {
		b.Baseline, b.Candidate = v.Baseline, v.Candidate
		b.BaselineItems, b.CandidateItems = v.BaselineItems, v.CandidateItems
		b.Removed, b.Added, b.Changed = v.Removed, v.Added, v.Changed
	}
	return b
}
