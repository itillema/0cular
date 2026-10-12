# ADR-0013: Decode and schema validation details

- **Status:** Accepted
- **Date:** 2026-10-11

## Context

ADR-0005 set the decode model: an artifact must mean the same thing to the gate as to its consumers, whatever its format. YAML resolves under the YAML 1.2 core schema, duplicate keys and non-finite numbers are errors, and caps bound the work. It left one question to task 1.2: whether an integer that a double can't hold exactly is rejected or rounded.

Building the decoders in task 1.2 turned up more places where parsers read the same bytes differently:

- **YAML 1.1 behavior that the node tree hides.** go.yaml.in/yaml/v3 follows YAML 1.1 in places:
  - It reads U+0085, U+2028 and U+2029 as line breaks, even inside quoted strings. YAML 1.2 parsers read them as ordinary characters.
  - It drops the non-specific tag `!` without a trace. Under YAML 1.2, `! 12` is the string "12"; without the tag, it's the number 12.
  - It treats `<<` as a merge key. Merge keys are a YAML 1.1 feature: PyYAML, Ruby's Psych and go-yaml merge them, while YAML 1.2 parsers read a plain string key.
- **Lone surrogates.** encoding/json turns a lone surrogate escape such as `\ud800` into U+FFFD, while other parsers keep it.
- **Integers beyond 2^53.** JavaScript, and any consumer that reads every number as a double, can't tell apart integers beyond 2^53 − 1. I-JSON (RFC 7493) limits interoperable integers to that range for this reason.
- **Nesting.** go.yaml.in/yaml/v3 and encoding/json both stop at 10,000 levels.

Validating artifacts against a contract's JSON Schema raised questions the plan didn't answer:

- **Formats.** Draft 2020-12 treats `format` as an annotation unless a validator is told to assert it, so by default `"format": "ipv4"` checks nothing.
- **References.** A schema can refer to other files and URLs, which would be read from wherever the check runs. The Action loads a contract from the pull request's base ref, so that a pull request can't weaken its own checks. A schema spread over several files would need every one of them taken from the base ref too.

## Decision

1. **An integer literal beyond ±(2^53 − 1) is a decode error.** The message tells the producer to quote the value if it's an identifier. A float literal such as `1e20` is allowed, since it's approximate by nature.
2. **JSON artifacts follow I-JSON (RFC 7493) where parsers disagree:** no duplicate member names, integers within ±(2^53 − 1), and no lone surrogate escapes. A UTF-8 byte order mark is an error, as most JSON parsers treat it.
3. **YAML artifacts may not use what parsers read differently:**
   - the raw characters U+0085, U+2028 and U+2029 (an escape in a double-quoted string is fine);
   - tags of any kind, including the non-specific `!`, which the decoder finds in the source at the node's position;
   - merge keys (`<<`);
   - an alias inside the value its anchor names;
   - map keys that aren't strings, including aliases.

   Anchors and aliases are otherwise allowed, within the caps: an alias shares its anchor's value, and counts its anchor's full size against `maxNodes` and its full depth against `maxDepth`. A byte order mark is allowed, as YAML specifies.
4. **Depth counts nested maps and arrays:** `{"a": [1]}` nests 2 deep. A contract's `maxDepth` is at most 10,000, the deepest nesting both parsers read.
5. **An empty artifact is a decode error.** A YAML `---` alone, or JSON `null`, decodes to null.
6. **The decoded value** is built from nil, bool, float64, string, `[]any` and `map[string]any`, and −0 becomes 0.
7. **A contract's JSON Schema:**
   - It is one self-contained file. A reference to another file or a URL is an error that says to bundle the schemas, so the schema reviewers see is the whole schema.
   - Its JSON is read as strictly as an artifact's, so a duplicate key can't hide a constraint from reviewers. It may be at most 4 MiB.
   - It defaults to Draft 2020-12, and its formats are asserted: a value marked `ipv4` or `date-time` must be one. A format the validator doesn't know is ignored; `contract lint` will flag it.
   - A failure reports the most specific violations, at most 20, in document order with JSON Pointers, and the total. A message that quotes the artifact is cut to 200 characters.

## Consequences

- Every integer a rule sees is exact. The gate reads each artifact the way a YAML 1.2 or I-JSON consumer does, or it blocks the artifact.
- Some artifacts are blocked until they're rewritten, and each message says how:
  - unquoted 64-bit IDs, until the IDs become strings;
  - an integral number from 2^53 up to 10^21 written without an exponent, which is how Go's encoding/json writes such a float64 (write it as `1e20`, or as a string);
  - YAML with tags, merge keys or raw line separators.
- A schema that uses `format` as documentation can block values its author didn't mean to check; remove the format, or replace it with a `pattern`.
- Teams with schemas split across files must bundle them first.
- Decoding a 5 MiB JSON artifact takes about 65 ms on a laptop, and validating it against a typical schema about 85 ms, leaving most of Phase 1's 500 ms budget to the rules. YAML is slower to decode, about 400 ms for 5 MiB, mostly in the YAML parser.

## Alternatives considered

- **Rounding integers beyond 2^53:** nothing would be blocked, but a rule near the boundary could see a different value than the consumer does. For example, two distinct IDs could compare equal.
- **A contract setting (`largeIntegers: reject | round`):** it would let teams with numeric 64-bit IDs opt in to rounding, at the cost of another field in the contract format. It can still be added if design partners need it.
- **Honoring YAML's standard tags (`!!str`, `!!int`):** correct for YAML 1.2 parsers, but not every consumer agrees on them, and the decoder can't see `!` in the node tree anyway.
- **Treating `<<` as a plain string key, as YAML 1.2 does:** YAML 1.1 consumers would merge the map instead.
- **Formats as annotations only, the Draft 2020-12 default:** a check the author wrote would silently do nothing.
- **Following references to other files:** convenient for split schemas, but the Action would have to collect them all from the base ref, and reviewers would have to follow them to see the whole schema.

## References

- [RFC 7493: The I-JSON Message Format](https://www.rfc-editor.org/rfc/rfc7493)
- [RFC 8259, section 8.2 (unpaired surrogates)](https://www.rfc-editor.org/rfc/rfc8259#section-8.2)
- [YAML 1.2.2, changes from YAML 1.1](https://yaml.org/spec/1.2.2/ext/changes/)
- [YAML 1.2.2, core schema](https://yaml.org/spec/1.2.2/#103-core-schema)
- [JSON Schema 2020-12 Validation, section 7.2 (format vocabularies)](https://json-schema.org/draft/2020-12/json-schema-validation#section-7.2)
