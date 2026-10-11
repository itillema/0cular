# ADR-0011: Branches and promotion

- **Status:** Proposed
- **Date:** 2026-10-11

## Context

0cular will be released and deployed through four environments: development, QA, UAT (staging) and production. Each environment needs a branch that always shows what's in it, and changes must move through the environments in order, so that nothing reaches production without passing QA and UAT. The build plan didn't define a branch model.

## Decision

1. **Four permanent branches,** one per environment. They're never deleted.

   | Branch | Environment |
   | --- | --- |
   | `main` | Production; the default branch |
   | `uat` | UAT and staging |
   | `qa` | QA and testing |
   | `dev` | Development and dev testing |

2. **Pull requests follow one flow:**

   | Into | Accepted from |
   | --- | --- |
   | `main` | `uat` only |
   | `uat` | `qa`, or a `hotfix/*` branch |
   | `qa` | `dev`, or a `dev/*` branch |
   | `dev` | Any branch, including from forks |

   Feature work branches off `dev`, merges into `dev`, and is then promoted `dev` → `qa` → `uat` → `main`. Promotions into `main`, `uat` and `qa` must come from branches in this repository, not from forks.

3. **Enforcement:**
   - The `branch-flow` workflow fails a pull request whose source branch isn't accepted by its target. It runs again when a pull request's base branch changes. It's a required check on `main`, `uat` and `qa`, because rulesets can require checks but can't restrict which branch a pull request comes from.
   - CI runs on every pull request and on every push to the four branches, and `ci-ok` is required on `main`.
   - Rulesets block deleting or force-pushing the four branches. GitHub's automatic deletion of merged head branches stays on for feature branches, and the rulesets keep it from removing a permanent one.
   - Promotions are merged with a merge commit, never rebased or squashed, so the four branches keep a shared history and later promotions merge cleanly.

4. **Dependabot** opens its pull requests against `dev`, so dependency updates are promoted like any other change.

5. **Hotfixes:** a `hotfix/*` branch merges into `uat`, and `uat` into `main`. The same hotfix branch is then merged into `dev` too (any branch may merge into `dev`), so development doesn't fall behind production.

## Consequences

- The tip of each permanent branch is what's in that environment. Every change reaches production through QA and UAT, or through the hotfix path.
- Every promotion is a pull request, so each one runs CI and leaves a record.
- Merge commits accumulate downstream: `main` has promotion merges that `dev` doesn't. That's expected, and harmless while changes flow one way and hotfixes are merged back into `dev`.
- **Releases are an open question for task 1.9.** release-please opens its release pull request against `main`, which this flow doesn't accept. One option is to run release-please against `uat`, so its release pull request is promoted like everything else. Another is to tag releases on `main` after a promotion. Task 1.9 decides, and updates this ADR.

## Alternatives considered

- **Trunk-based development** (`main` only, with environments selected by tags): simpler, but no branch shows what's in each environment.
- **GitHub Flow with deployment environments:** promotion lives in deployment workflows instead of branches. It means fewer merges, but each environment's state is less visible.
- **Rulesets alone:** they block deletion and require checks, but can't restrict a pull request's source branch. That's why the `branch-flow` check exists.
