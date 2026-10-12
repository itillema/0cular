# ADR-0014: Counting the cost of CEL rules

- **Status:** Accepted
- **Date:** 2026-10-11

## Context

ADR-0003 bounds every CEL evaluation in two ways: a runtime cost limit (default 1,000,000), using cel-go's `cel.CostLimit`, and a 2-second timeout.

cel-go's runtime cost tracker keeps a stack of values that grows with every iteration of a comprehension, and searches the whole stack on most steps. So tracking cost makes comprehensions quadratic. Measured with cel-go v0.30.0:

| Elements in the list | With tracking | Without tracking |
| --- | --- | --- |
| 5,000 | 21 ms | 1 ms |
| 10,000 | 83 ms | 2 ms |
| 20,000 | 322 ms | 5 ms |

The rule was `self.features.all(f, f.weight >= 0.0)`. Over the roughly 55,000 features of a 5 MB artifact, it ran past the 2-second timeout and blocked the candidate. cel-go v0.32.0 has the same tracker.

Without a runtime cost limit, though, memory isn't bounded: a nested `map()` can build billions of elements within the timeout, inside a host application running the loader.

## Decision

1. **0cular counts the cost itself, instead of using cel-go's tracker.** A decorator charges each step of a program to a per-evaluation budget:
   - every function call, operator and comprehension step costs 1;
   - every list or map a step creates costs its size, and every string a tenth of its length;
   - a comprehension's accumulator is charged once, when the comprehension ends;
   - `zerocular.unique` costs the size of the list it checks, charged before it runs.
2. **The limit stays at 1,000,000 per evaluation.** It can be lowered, never raised. Going over it stops the evaluation, which blocks the candidate, as before.
3. **The counter enforces the timeout too.** It checks the evaluation's context every 100 steps, so the 2-second timeout and cancellation stop every evaluation, including the comprehensions the decorator wraps.
4. **Functions slower than linear,** such as `sort()` and `distinct()`, cost 1 per call. The timeout bounds them.
5. **`lists.range()` builds at most 1,000,000 elements,** the cost limit.
6. **Static estimates at lint time still come from cel-go's estimator,** as ADR-0003 says. Its units are close to the runtime count, but not the same.

## Consequences

- **Rules run in linear time.** The rule above, over 100,000 elements, costs 300,001 and takes about 40 ms. A full check of a 5 MiB artifact with a schema and 10 invariants takes about 310 ms on a laptop.
- **A quadratic rule still stops at the limit,** early, before it builds its result.
- **Reports show 0cular's count, not cel-go's.** A rule that ran close to the limit in cel-go's units may land on the other side of it.
- **0cular relies on cel-go's decorator interface** (`cel.CustomDecoratorV2`). A cel-go upgrade must keep the counter's tests passing.

## Alternatives considered

- **Keep cel-go's tracker:** exact cel-go units, but rules over long lists time out, so large artifacts couldn't use `all()`, `exists()`, `map()` or `filter()`.
- **Rely on the timeout alone:** simplest, but memory isn't bounded at runtime.
- **Patch or fork cel-go:** it would fix the stack, but 0cular would then maintain a fork of a security-sensitive dependency.

## References

- [cel-go v0.30.0 runtime cost tracker](https://github.com/google/cel-go/blob/v0.30.0/interpreter/runtimecost.go)
- [ADR-0003: Contract language: CEL and JSON Schema](0003-contract-language-cel-and-json-schema.md)
