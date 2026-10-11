# Architecture decision records

This directory records the decisions that shape 0cular: what was decided, why, and what follows from it.

## Process

- Copy [template.md](template.md) to `NNNN-short-title.md`, using the next free number.
- A new ADR starts as **Proposed**. Once the maintainer accepts it, its status becomes **Accepted**, with the date.
- Don't rewrite an accepted ADR. To change a decision, write a new ADR that supersedes it, and mark the old one **Superseded by ADR-NNNN**.
- ADRs 0001–0010 are reserved for the decisions planned in [BUILD_PLAN.md](../BUILD_PLAN.md) §10. Unplanned ADRs start at 0011.

## Index

| ADR | Decision | Status |
| --- | --- | --- |
| [0001](0001-name-owner-and-module-path.md) | Name, owner and module path | Accepted |
| [0002](0002-go-and-the-v0-stack.md) | Go and the v0 stack | Accepted |
| [0003](0003-contract-language-cel-and-json-schema.md) | Contract language: CEL and JSON Schema | Accepted |
| [0004](0004-store-interface-and-versioning.md) | Store interface and versioning semantics | Accepted |
| [0005](0005-contract-format-and-apiversion-policy.md) | Contract file format, `apiVersion` policy and decode model | Accepted |
| 0006 | S3-compatible test server | Planned (task 3.1) |
| 0007 | Rollout manifest and ring hashing | Planned (task 4.1) |
| 0008 | Health evaluation | Planned (task 4.3) |
| 0009 | Re-validating a cached LKG when the contract changes | Planned (task 2.1) |
| [0010](0010-license-and-dco.md) | License (Apache-2.0) and DCO | Accepted |
