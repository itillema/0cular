# ADR-0015: Redacting secrets in reports

- **Status:** Proposed
- **Date:** 2026-10-12

## Context

Config files sometimes hold secrets: an API key, a database password, a signing token. Reports quote the artifact as evidence, so a check that fails on a secret field can show the secret. The GitHub Action (task 1.6) puts reports in Job Summaries and pull request comments, which can be public and stay in the history.

`eval` quotes artifact values in these places:
- the explanation of a failed rule, such as `self.auth.token = "…", not == "…"`;
- a schema violation whose message quotes the value, such as a string that fails a `pattern`;
- a decode error that quotes a number too large for a double;
- budget evidence: a measure's values, and collection keys in errors.

## Decision

1. **Reports hide the values of fields whose names look secret.** A field matches a pattern when its name contains the pattern, comparing the two in lower case and with only their letters and digits. So `apikey` matches `apiKey`, `api_key` and `X-API-KEY`.
2. **The built-in patterns are** `password`, `passwd`, `secret`, `token`, `credential`, `apikey`, `privatekey` and `authorization`. They always apply.
3. **A contract adds patterns in `report.redact`:**
   - at most 64 patterns, each at most 64 characters;
   - each needs a letter, since a pattern of digits alone would match array indexes;
   - no two may match the same names.

   Nothing removes a built-in pattern.
4. **Evidence shows `[redacted]` in place of:**
   - in a rule's explanation, any value read through a matching field (by name, by index such as `self["api_key"]`, or through a loop over one), and a map's entries for matching fields, at any depth. The result of `size()` is shown, since it's only a count;
   - a schema violation's message, for a value at a JSON Pointer through a matching field. The message then names only the keyword that failed;
   - the value a decode error quotes, at a pointer through a matching field;
   - collection keys in budget errors, when the collection or its key reads a matching field;
   - a measure's values, when the measure reads a matching field. `eval.BudgetValues` keeps them for approvals, marked `Redacted`, and reports leave them out.
5. **Reports escape the marker like any other text** (§3.12).

## Consequences

- A secret in a field whose name looks secret, or that the contract names, stays out of every report format.
- Redaction goes by field names, not values. A secret under an innocent name, such as `note: "the password is hunter2"`, still shows. No pattern list catches everything, and secrets management is a non-goal: secrets belong in a secrets manager, not in config files.
- Some values that aren't secret are hidden too, such as a field called `tokenizer`, or a value computed from a secret field. Evidence is less precise there, which is the safe side.
- Patterns are part of the contract, so they're reviewed with it, and a pull request can't remove one: the Action reads the contract from the base ref.

## Alternatives considered

- **Built-in patterns only:** simpler, but a team couldn't protect its own field names, such as `ssn` or `pin`.
- **Regular expressions as patterns:** more precise, but harder to review, and a mistake can quietly match nothing.
- **Redacting by value,** with secret scanners' patterns or entropy: it catches secrets under innocent names, but misses others and hides ordinary values. It could come later, alongside this.
- **Waiting until the Action posts reports (task 1.6):** v0.1's reports would have no protection until then.

## References

- [BUILD_PLAN.md §3.12, Security model](../BUILD_PLAN.md)
- [ADR-0003: Contract language: CEL and JSON Schema](0003-contract-language-cel-and-json-schema.md)
- [ADR-0005: Contract file format, `apiVersion` policy and decode model](0005-contract-format-and-apiversion-policy.md)
