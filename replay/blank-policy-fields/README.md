# blank-policy-fields

> **Synthetic data.** This scenario reconstructs the shape of a public incident using made-up data. None of it is real, and 0cular isn't affiliated with Google.

## The incident

On June 12, 2025, a policy change was inserted into the regional Spanner tables that Google Cloud's Service Control reads its quota policies from. The data replicated globally within seconds, and it contained unintended blank fields.

A code path added on May 29 for additional quota policy checks read those fields. It had no appropriate error handling and wasn't protected by a feature flag, so the null pointer crashed the binary, and Service Control crash-looped in every region. The code had passed a region-by-region rollout, but the failing path only ran once a policy change reached it.

Most products were affected for about 3 hours (10:49 to 13:49 US Pacific), and some recovered later.

Postmortem: [Google Cloud Service Control incident report, June 12, 2025](https://status.cloud.google.com/incidents/ow5i3PPK96RduMcb1SsW)

## What's reconstructed

| File | Content |
| --- | --- |
| `baseline.yaml` | Eight quota policies, each with a limit and a period |
| `candidate.yaml` | The same policies plus a new one whose `limit:` is blank, which YAML reads as null |
| `consumer/` | A stub that derives each policy's per-second rate and dereferences the limit without checking for null |
| `schema.json` | The file's shape. Like a nullable column, it lets a limit be null. Its cap of 1,000 policies is ours, and lets `contract lint` prove the invariant stays within its cost limit |
| `contract.yaml` | The contract that would have caught it |
| `expected.yaml` | The expected outcome at each stage |

Google's report says only "unintended blank fields". Which field is blank here, and the policies themselves, are our choice.

## What catches it

| Stage | Outcome |
| --- | --- |
| Check (v0.1) | BLOCK. The invariant `every-policy-has-a-limit` fails on the policy whose limit is null |
| Keep (v0.2) | The loader rejects the candidate and keeps the last known good |
| Publish (v0.3) | BLOCK, before the data replicates anywhere |

Both files pass `schema.json`: the schema allows a null limit, and only the consumer's code assumes there's always one.

## Run the consumer

```bash
go run ./replay/blank-policy-fields/consumer replay/blank-policy-fields/baseline.yaml   # prints eight rates
go run ./replay/blank-policy-fields/consumer replay/blank-policy-fields/candidate.yaml  # panics: invalid memory address or nil pointer dereference
```
