# ADR-0016: Approvals

- **Status:** Proposed
- **Date:** 2026-11-04

## Context

A breached diff budget makes the verdict NEEDS_APPROVAL, unless the budget sets `overridable: false`. BUILD_PLAN §3.4 says an approval accepts the breach:

- it binds to the candidate's digest, the contract's hash and the evaluated budgets (each breached budget's name and values), and any change invalidates it;
- it has `By`, `Reason` and `Expires`;
- it can't override BLOCK;
- the result records it, and from v0.3 so does publish metadata.

How the approval is obtained is the caller's job: a CI environment approval, chatops, or a person at a terminal. The approval is often given in one place and applied in another, later. So whether an approval still applies has to be decided only from the approval and the new result. The plan leaves these questions open:

- what "any change" covers, and whether an approval may cover more or fewer budgets than are breached;
- what an approval without an expiry means;
- how several approvals combine;
- how the result shows a breach that was approved, and an approval that didn't apply;
- where an approval keeps a measure's values when reports redact them (ADR-0015).

## Decision

1. **An approval accepts exactly one evaluated change.** `gate.Check` applies it only if all of these hold:
   - the verdict without approvals is NEEDS_APPROVAL;
   - it names who approved and why;
   - it has an expiry that hasn't passed;
   - its candidate digest and contract digest are the result's;
   - its budgets are the same set as the breached ones, in any order, each with the same values. For a measure budget those are the baseline's and the candidate's values; for a collection budget, the item counts and the counts of removed, added and changed items.

   An approval that covers a budget that isn't breached, misses one that is, or lists one twice doesn't apply.
2. **Every approval expires.** One without an expiry never applies, and one applies only before its expiry, not at it. `gate.Approve` refuses to make an approval that has no expiry or has already expired. `check --approve` approves only the run it's part of, and records the approval with an expiry one hour later.
3. **One approval that applies is enough.** `gate.Check` takes any number of them. When one applies, the verdict becomes PASS and each breached budget's status becomes `approved`.
4. **The result records every approval given,** in the order given, with whether it applied and, if not, why: for example, "it's for another candidate". Reports show who approved, until when and why, and why an approval didn't apply. They name the budgets an approval covers, but not their values; the checks show those, redacted where they must be.
5. **An approval keeps a redacted measure's values,** so that it binds to them (ADR-0015). Callers should store an approval as carefully as the artifact.
6. **`gate.Approve` approves only a NEEDS_APPROVAL result,** the one the approver saw. It copies the digests and the breached budgets' values from that result.
7. **The `Approval` type lives in `eval`,** because `eval.Result` records approvals and `gate` imports `eval`. Its JSON form, with the field names `by`, `reason`, `expires`, `candidateDigest`, `contractDigest` and `budgets`, is how an approval travels between processes. It's experimental until 1.0.

## Consequences

- **An approval can't be reused** for another candidate, for another version of the contract, or after the baseline moves enough to change what a budget measured.
- **An approval survives a baseline change that a budget doesn't see.** If the baseline moves but every breached budget still measures the same values, the approval still applies. The approver accepted that change, so this is intended.
- **A result can say why an approval was refused,** which helps when an approval given in chatops or a CI environment meets a candidate that has since changed.
- **Approvals given earlier have to be carried in.** In v0.1, only `check --approve` makes them on the command line; a library caller passes them to `gate.Check`. Loading a stored approval from a file, such as for `publish` in v0.3, is left to that task.
- **The JSON report gains an `approvals` array** and the check status `approved`. Nothing was released with schema version 1 before them, so it stays at 1.

## Alternatives considered

- **An approval without an expiry never expires:** the exact binding already limits it to one change, but a forgotten expiry would make it permanent. When in doubt, the gate blocks.
- **Bind to the baseline's digest instead of the budget values:** stricter, but a rebase that changes nothing a budget measures would need a fresh approval.
- **Bind to a digest of the budget values:** compact, and it would hide redacted values, but an approval could no longer show what it accepted.
- **Let approvals combine,** with each covering some budgets: more flexible, but it's harder to see who accepted what. One approver can approve several budgets at once.
- **Keep `needs-approval` as the status of an approved budget:** no new status, but a reader would see a budget needing approval under a PASS verdict.
- **Return a `gate.Result` that wraps `eval.Result`:** it keeps approvals out of `eval`, but §3.4 says `gate.Check` returns `eval.Result`, and every report takes one.

## References

- [BUILD_PLAN.md §3.3 and §3.4](../BUILD_PLAN.md)
- [ADR-0015: Redacting secrets in reports](0015-redacting-secrets-in-reports.md)
