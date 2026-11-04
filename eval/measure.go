package eval

import "context"

// Measurement is what Measure found.
type Measurement struct {
	// Contract is the contract's metadata.name, and ContractDigest the
	// digest of its file.
	Contract       string
	ContractDigest string
	// Baseline and Candidate are the versions measured.
	Baseline, Candidate Artifact
	// Checks holds the baseline's envelope check, then the candidate's,
	// then the diff budgets, as the contract lists them.
	Checks []Check
	// BaselineDoc and CandidateDoc are the decoded versions, or nil for a
	// version that didn't decode. Treat their values as read-only.
	BaselineDoc, CandidateDoc *Document
}

// Measure decodes baseline and candidate, and measures the contract's diff
// budgets from one to the other, as Check does. It skips the schema and
// the invariants, which judge the candidate alone: Measure compares two
// versions, as 0cular diff does, rather than gating one. Problems with
// either version are results, not errors: Measure returns an error only
// when ctx ends first.
func (e *Evaluator) Measure(ctx context.Context, baseline, candidate Input) (*Measurement, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m := &Measurement{
		Contract:       e.contract.Metadata.Name,
		ContractDigest: e.contract.Digest,
		Baseline:       artifactOf(baseline),
		Candidate:      artifactOf(candidate),
	}
	base, check, err := e.baselineEnvelope(ctx, baseline)
	if err != nil {
		return nil, err
	}
	m.Checks = append(m.Checks, check)
	cand, check, err := e.envelope(ctx, "candidate", candidate)
	if err != nil {
		return nil, err
	}
	m.Checks = append(m.Checks, check)
	budgets, err := e.budgetChecks(ctx, version{cand, candidate.Key}, &version{base, baseline.Key})
	if err != nil {
		return nil, err
	}
	m.Checks = append(m.Checks, budgets...)
	m.BaselineDoc, m.CandidateDoc = base, cand
	return m, nil
}
