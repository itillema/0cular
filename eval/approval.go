package eval

import "time"

// Approval accepts the diff budgets that one change breaches, which makes
// a NEEDS_APPROVAL verdict a PASS (ADR-0016). It binds to the candidate's
// digest, the contract's digest and each breached budget's name and values:
// gate.Check applies it only if all of them are the same, nothing blocks,
// and it hasn't expired. gate.Approve makes one from a Result.
//
// An approval holds the values of the budgets it accepts, even those that
// reports redact (ADR-0015), so keep it as carefully as the artifact. Its
// JSON form is how it travels from the check that asks for it to the one
// that applies it. It's experimental, and may change before 0cular 1.0.
type Approval struct {
	// By names who approved, and Reason says why. An approval without
	// either never applies.
	By     string `json:"by"`
	Reason string `json:"reason"`
	// Expires is when the approval stops applying. An approval without
	// one never applies.
	Expires time.Time `json:"expires"`
	// CandidateDigest is the digest of the candidate that the approval
	// accepts, and ContractDigest the digest of the contract's file.
	CandidateDigest string `json:"candidateDigest"`
	ContractDigest  string `json:"contractDigest"`
	// Budgets lists every budget that the candidate breaches, with what it
	// measured.
	Budgets []ApprovedBudget `json:"budgets"`
}

// ApprovedBudget is a breached diff budget as an approval accepts it: its
// name and what it measured. A measure budget sets only the measure's
// values, and a collection budget only the counts.
type ApprovedBudget struct {
	// Name is the budget's name.
	Name string `json:"name"`
	// Baseline and Candidate are a measure budget's values.
	Baseline  float64 `json:"baseline,omitzero"`
	Candidate float64 `json:"candidate,omitzero"`
	// BaselineItems and CandidateItems are how many items a collection
	// budget's lists hold; Removed, Added and Changed count the items the
	// candidate removed, added and changed.
	BaselineItems  int `json:"baselineItems,omitzero"`
	CandidateItems int `json:"candidateItems,omitzero"`
	Removed        int `json:"removed,omitzero"`
	Added          int `json:"added,omitzero"`
	Changed        int `json:"changed,omitzero"`
}

// ApprovalOutcome is an approval given for a candidate, and whether it
// applied.
type ApprovalOutcome struct {
	Approval
	// Applied is set when the approval accepted the candidate's breached
	// budgets.
	Applied bool
	// Rejection says why the approval doesn't apply, such as "it's for
	// another candidate". It's empty when Applied is set.
	Rejection string
}
