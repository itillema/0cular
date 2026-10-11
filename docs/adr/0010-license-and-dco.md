# ADR-0010: License (Apache-2.0) and DCO

- **Status:** Accepted
- **Date:** 2026-10-10

## Context

0cular is open source and meant to be embedded: the loader library runs inside other companies' services, and the CLI runs in their CI and publish paths. The license has to be acceptable to those companies' legal reviews, and it has to cover patents, because a safety tool that teams wire into production is exactly what a cautious legal team reads closely.

The project has one maintainer and hopes for outside contributors. It needs a clear record that each contribution may be distributed under the project's license, without the overhead of collecting signed agreements.

## Decision

1. **License: Apache License 2.0** for all code and documentation in the repository, with the license text in `LICENSE`.
2. **Developer Certificate of Origin (DCO 1.1)** instead of a contributor license agreement:
   - Every commit is signed off with `git commit -s`, which adds a `Signed-off-by:` line matching the commit's author. The maintainer's commits are signed off too.
   - A workflow checks every non-merge commit in each pull request, using only git and no third-party app. Dependabot's pull requests are exempt, because it can't sign off.
   - History written before the repository went public is signed off before publication (`git rebase --signoff`).
3. **Third-party notices.** Release archives and images include the licenses of the dependencies they ship, since some (such as BSD-3-Clause) require the notice to travel with binaries (task 1.9).

## Consequences

- Apache-2.0's explicit patent grant and patent-retaliation clause make adoption easier for companies, and it's the norm in the cloud-native ecosystem 0cular belongs to.
- The DCO costs contributors one flag per commit. The check tells them how to fix a missing sign-off.
- There's no CLA. Relicensing later would need every contributor's consent, which is acceptable for a project that intends to stay Apache-2.0.

## Alternatives considered

- **MIT or BSD:** simpler, but with no explicit patent grant.
- **MPL-2.0:** file-level copyleft is less familiar in infrastructure tooling, and it adds questions for teams embedding the loader.
- **GPL or AGPL:** would block embedding the loader in proprietary services, which is the main way 0cular is used.
- **A contributor license agreement:** heavier for contributors, and it needs signing infrastructure that a solo-maintained project doesn't have.

## References

- [Apache License 2.0](https://www.apache.org/licenses/LICENSE-2.0)
- [Developer Certificate of Origin](https://developercertificate.org/)
