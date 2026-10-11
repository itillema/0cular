# doubled-feature-file

> **Synthetic data.** This scenario reconstructs the shape of a public incident using made-up data. None of it is real, and 0cular isn't affiliated with Cloudflare.

## The incident

On November 18, 2025, a database permission change made the query that builds Cloudflare's Bot Management feature file return duplicate rows. The file doubled in size and ended up with more than 200 features, where normal use was about 60.

Cloudflare's FL2 proxy preallocates memory for 200 features. On the oversized file it panicked (`called Result::unwrap() on an Err value`) and returned 5xx errors. The older FL proxy didn't error, but it gave all traffic a bot score of 0.

The file was regenerated every five minutes and published to the entire network, so the network flapped between good and bad files. Failures began at 11:20 UTC, core traffic was largely flowing again by about 14:30, and everything was normal by 17:06.

Postmortem: [Cloudflare outage on November 18, 2025](https://blog.cloudflare.com/18-november-2025-outage/)

## What's reconstructed

| File | Content |
| --- | --- |
| `baseline.json` | A feature file with 120 features |
| `candidate.json` | The same file with every row duplicated: 240 entries |
| `consumer/` | A stub that preallocates 200 slots and copies the features in without checking the count, like the proxy did |
| `schema.json` | The file's shape. Like the real pipeline's checks, it knows nothing about the consumer's limit |
| `contract.yaml` | The contract that would have caught it |
| `expected.yaml` | The expected outcome at each stage |

The counts are ours. 120 → 240 was chosen so that a single doubling crosses the 200 limit; Cloudflare didn't publish the bad file's exact count.

## What catches it

| Stage | Outcome |
| --- | --- |
| Check (v0.1) | BLOCK. The invariant `feature-cap` fails (240 features against a limit of 200), and the budget `feature-count-stable` is breached (+100% against 20%) |
| Keep (v0.2) | The loader rejects the candidate and keeps the last known good |
| Publish (v0.3) | BLOCK, before the file reaches any store |

Both files pass `schema.json`: a schema checks values, but it can't see the consumer's limit.

## Run the consumer

```bash
go run ./replay/doubled-feature-file/consumer replay/doubled-feature-file/baseline.json   # loads 120 features
go run ./replay/doubled-feature-file/consumer replay/doubled-feature-file/candidate.json  # panics: index out of range [200] with length 200
```
