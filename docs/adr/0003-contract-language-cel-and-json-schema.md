# ADR-0003: Contract language: CEL and JSON Schema

- **Status:** Accepted
- **Date:** 2026-10-10

## Context

A contract states what a consumer can safely load: the shape of the artifact, the absolute limits it must stay within (invariants), and how far it may move from the baseline (diff budgets). The gate evaluates these rules on untrusted data, in CI and in publish paths, and the loader evaluates the same rules inside production processes. So the rule language must:

- be expressive enough for collection checks: counts, uniqueness, non-null fields, references between items;
- be bounded in both time and memory, with no way for a rule or an artifact to hang or exhaust the host;
- never execute anything from the artifact;
- be checkable before use, so a broken rule fails `contract lint` instead of a publish;
- be familiar to the people who write contracts: platform, SRE and security engineers.

## Decision

1. **Shape: JSON Schema, Draft 2020-12**, validated with santhosh-tekuri/jsonschema/v6 after decoding (ADR-0005). A schema violation is a BLOCK.
2. **Rules: CEL**, evaluated with google/cel-go:
   - Invariants are CEL expressions that return a bool. False, an error, an exhausted cost limit or a timeout is a BLOCK.
   - Diff-budget measures are CEL expressions that return a number, evaluated on both the candidate and the baseline.
3. **The CEL environment:**
   - The variables are `self` (the candidate), `baseline` (`null` under `--no-baseline`) and `meta` (`bytes`, `sha256`, `format`, `key`).
   - It has standard CEL plus the cel-go Strings, Lists, Sets and Math extensions. Each extension is pinned to an explicit version option, such as `ext.Strings(ext.StringsVersion(N))`, so a cel-go upgrade can't change how a contract behaves. Raising a pinned version is a behavior change and goes in the release notes.
   - `cel.CrossTypeNumericComparisons(true)` lets a decoded number (a double) compare with an int literal, so `self.limit > 0` works.
   - One custom function, `zerocular.unique(list)`, checks uniqueness in linear time with a Go hash set. The namespace is the letter-first form of the name (ADR-0001). Task 1.2 checks whether `ext.Lists`' `distinct()` is also linear; if it is, `size(l.distinct()) == size(l)` may replace the custom function.
4. **Limits:**
   - A runtime cost limit (`cel.CostLimit`, default 1,000,000) for each evaluation.
   - A 2-second timeout per expression, enforced with `Program.ContextEval` and `cel.InterruptCheckFrequency`, because cost tracking alone doesn't bound wall time.
   - A contract can lower either limit, never raise it.
   - At lint time, `Env.EstimateCost` rejects an expression whose static estimate exceeds the ceiling. The sizes come from `artifact.maxBytes` and from the schema's `maxItems` and `maxLength`.
5. **Optional evaluators later.** Rego and CUE may come later as optional evaluators, behind the same evaluator interface and the same limits.

## Consequences

- Rules are bounded and side-effect free, and they type-check at lint time, so a contract mistake shows up in review rather than in a publish.
- CEL is already used in Kubernetes (ValidatingAdmissionPolicy and CRD validation rules), so many contract authors have seen it.
- CEL is still unfamiliar to some users. The invariant cookbook and specific lint messages carry that load.
- Numbers reach CEL as doubles (ADR-0005), so integer-literal arithmetic needs care. Lint warns on it: write `self.x + 1.0`, not `self.x + 1`.

## Alternatives considered

- **Rego (OPA):** expressive and widely used for policy. But it's a larger runtime to embed in the loader, its evaluation is harder to bound by cost, and its query style is less natural for value checks on one document.
- **CUE:** unifies schema and constraints elegantly. But it's heavier to embed, less familiar, and has no cost model.
- **JSONPath, JMESPath or JSONata:** queries without a type checker or a cost model.
- **Starlark, Lua or WebAssembly:** full programs. Bounding them, and reviewing what they do, is much harder than for an expression language with a cost model.

## References

- [CEL language definition](https://github.com/google/cel-spec/blob/master/doc/langdef.md)
- [cel-go](https://pkg.go.dev/github.com/google/cel-go/cel)
- [CEL in Kubernetes](https://kubernetes.io/docs/reference/using-api/cel/)
- [JSON Schema 2020-12](https://json-schema.org/draft/2020-12)
