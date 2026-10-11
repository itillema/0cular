# ADR-0005: Contract file format, apiVersion policy and decode model

- **Status:** Accepted
- **Date:** 2026-10-10

## Context

Contracts are code: they're reviewed, owned and versioned with the configuration they protect, so their format must be easy to read in a pull request and strict enough that a typo can't silently weaken a check.

Artifacts are untrusted data. If the gate and a consumer decode the same bytes differently, a bad artifact can pass the gate and still break the consumer. Measured with go.yaml.in/yaml/v3 v3.0.5 and Go's `encoding/json`, the libraries in ADR-0002:

- `encoding/json` accepts duplicate object keys and keeps the last value (`{"a":1,"a":2}` decodes as `a: 2`); other parsers keep the first. `encoding/json/v2`, which rejects duplicates, is generally available only from Go 1.27, and 0cular supports Go 1.26.
- go.yaml.in/yaml/v3 rejects duplicate mapping keys. But when decoding into untyped values, it applies some YAML 1.1 rules:
  - `0777` becomes the number 511 (an octal literal);
  - timestamps and dates become `time.Time`;
  - `.nan` and `.inf` become NaN and infinity.

  YAML 1.1 booleans such as `yes` stay strings.
- Integers beyond ±2^53 can't be represented exactly as doubles.

## Decision

### Contract files

1. **Contract files are YAML**, with `apiVersion: 0cular/v1alpha1`, `kind: Contract` and `metadata.name`, followed by `artifact`, `schema`, `invariants`, `diffBudgets`, and later `consumerTests` (v0.3), `rollout` (v0.4) and `onFailure`.
2. **Loading is strict:** an unknown field is an error, and so is a duplicate key. A JSON Schema for contract files is published, so editors can validate them (task 1.1).

### apiVersion policy

3. **`v1alphaN`** may change incompatibly between minor releases. Every change is listed in the release notes, and `contract lint` explains how to migrate.
4. **`v1betaN`** changes need a deprecation period of at least two minor releases, during which both versions are accepted.
5. **`v1`** doesn't change incompatibly. A breaking change needs a new major `apiVersion`, and the previous one keeps working for at least one major release of 0cular.

### Decode model

These rules apply to every artifact format: JSON and YAML in v0.1, and TOML and CSV later.

6. **Size first.** The size is checked against `artifact.maxBytes` before parsing.
7. **Text.** Text formats must be valid UTF-8.
8. **Duplicate keys are a decode error** in every format. For JSON, the decoder detects them in a token pass, because `encoding/json` accepts them.
9. **Map keys must be strings**; any other key is a decode error.
10. **Numbers decode as CEL doubles.** Integers beyond ±2^53 lose precision, and lint warns about integer-literal arithmetic in CEL. Whether out-of-range integers should be rejected instead of rounded is decided in task 1.2.
11. **Non-finite numbers are a decode error.** NaN and infinity can't come from JSON, and accepting them from YAML would make comparisons unreliable.
12. **YAML scalars resolve under the YAML 1.2 core schema**, not YAML 1.1:
    - only `true` and `false` are booleans;
    - octal needs the `0o` prefix;
    - timestamps and dates stay strings, in RFC 3339 form where applicable.

    The decoder works from the parsed YAML node tree and resolves scalars itself, rather than relying on go.yaml.in/yaml/v3's untyped decoding.
13. **Caps** against hostile inputs:
    - nesting depth, default 64;
    - nodes after YAML alias expansion, default 1,000,000;
    - YAML aliases, 100 per document.

    Exceeding any cap is a decode error.
14. **CSV (later)** needs a header row and decodes to a list of maps keyed by it, with string values.

Any decode error is a BLOCK at the gate, and a rejection in the loader, which keeps its last known good.

## Consequences

- The same artifact means the same thing to every rule, whatever its format. Duplicate keys and YAML 1.1 surprises can't make the gate see a different document than a consumer does.
- Contract typos fail loudly at lint time instead of disabling a check.
- The YAML decoder needs more code than calling `yaml.Unmarshal`, and that code needs fuzzing (task 1.2).
- Strictness can reject artifacts that a lenient consumer would accept, such as a JSON file with a duplicate key. That's deliberate: when in doubt, the gate blocks.

## Alternatives considered

- **JSON contract files:** strict and simple, but harder to review, with no comments.
- **CUE or TOML contracts:** CUE is less familiar to reviewers, and TOML's nested tables read poorly for lists of rules.
- **Each format's own semantics:** simpler to implement, but it lets the gate and consumers disagree about what an artifact says.
- **Relying on `encoding/json/v2` for duplicate detection:** it isn't available on Go 1.26, the oldest supported release. It can replace the token pass once 1.26 is out of support.

## References

- [YAML 1.2.2, core schema](https://yaml.org/spec/1.2.2/#103-core-schema)
- [Go 1.27 release notes: encoding/json/v2](https://go.dev/doc/go1.27)
- [RFC 8259, section 4 (object names should be unique)](https://www.rfc-editor.org/rfc/rfc8259#section-4)
