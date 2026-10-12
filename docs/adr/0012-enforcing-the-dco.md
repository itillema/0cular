# ADR-0012: Enforcing the DCO

- **Status:** Accepted
- **Date:** 2026-10-11

## Context

ADR-0010 requires a `Signed-off-by` line on every commit, the maintainer's included, and a workflow checks every commit in each pull request. But the check wasn't required, so a failing run didn't block a merge. Three of the maintainer's commits reached all four permanent branches without a sign-off:

- `8891fb6953104471409232eb4b1152c29db48877` (docs: mark Phase 0 complete)
- `05ba5402c06debc2de1c2fc1bf73503875461eb5` (ci: enforce the branch flow and record it in ADR-0011)
- `34dbb4ceb1c1a75fc5e75d3207e16cf61944f8ee` (accepted adr 11)

Signing them off by rewriting history would mean force-pushing `main`, `uat`, `qa` and `dev`, which ADR-0011 forbids.

## Decision

1. **The `dco` check is required on `dev`.** Every change enters through `dev` (ADR-0011), so an unsigned commit is stopped there, and promotions carry only commits that have already passed. Dependabot's pull requests still pass: the check's job is skipped for them, and a job skipped by a condition reports success.
2. **A missing sign-off on a permanent branch is fixed with a remediation commit, never by rewriting the branch.** The commit's author adds an empty, signed-off commit (`git commit --allow-empty -s`) with one line for each commit:

   ```text
   I, Name <email>, hereby add my Signed-off-by to this commit: <full SHA>
   ```

   This is the form the DCO GitHub app recognizes. A pull request that hasn't merged yet is still fixed with `git rebase --signoff`, as `CONTRIBUTING.md` says.
3. **The three commits above get one remediation commit.**

## Consequences

- An unsigned commit can't merge into `dev`, so it can't reach `qa`, `uat` or `main` either.
- `dev` changes only through pull requests, because the check runs only on pull requests and must pass before `dev` changes.
- History keeps the three unsigned commits. The remediation commit is the public record of their sign-off.

## Alternatives considered

- **Rewriting history with `git rebase --signoff` and force-pushing:** the history would be clean, but it breaks ADR-0011's rule against force-pushing the permanent branches, and every existing clone would have to be reset.
- **Accepting the unsigned commits and enforcing only from now on:** simplest, but part of the history would break ADR-0010's rule with no record of a sign-off.
- **Requiring the check on all four branches:** no extra protection, since promotions contain only commits that already passed on `dev`.

## References

- [Developer Certificate of Origin 1.1](https://developercertificate.org/)
- [DCO GitHub app: remediation commits](https://github.com/dcoapp/app)
- [GitHub Docs: troubleshooting required status checks](https://docs.github.com/en/pull-requests/collaborating-with-pull-requests/collaborating-on-repositories-with-code-quality-features/troubleshooting-required-status-checks) (a job skipped by a condition reports success)
