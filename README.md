# 0cular

0cular gives config and data changes the safety net that code already has. It checks a change before it's published, keeps the last known good copy when a service loads it, and rolls it out in rings with automatic revert.

> **Status: pre-alpha.** Nothing is ready to install yet. The [build plan](docs/BUILD_PLAN.md) shows what's planned and when. If your team reloads config or data live, you can help shape it as a [design partner](docs/design-partners.md).

0cular is spelled with a zero and said "zero-cular". Where a name can't start with a digit, such as metric names, environment variables and the CEL namespace, it's spelled `zerocular` ([ADR-0001](docs/adr/0001-name-owner-and-module-path.md)).

## Why

Code ships through staged, health-checked rollouts. Config and data usually don't: they move through fast global channels, so one bad change can break every consumer of that kind at once. Five of the largest outages since mid-2024 started this way:

| Date | Incident |
| --- | --- |
| Dec 5, 2025 | Cloudflare: a global config change hit a years-old proxy bug |
| Nov 18, 2025 | Cloudflare: a query returned duplicate rows, and a feature file grew past the proxy's 200-feature limit |
| Oct 19–20, 2025 | AWS: DNS automation applied a stale plan, then deleted it, emptying the DynamoDB endpoint record |
| Jun 12, 2025 | Google Cloud: a policy record with blank fields crash-looped Service Control in every region |
| Jul 19, 2024 | CrowdStrike: a template type defined 21 input fields; the sensor's code supplied 20 |

The sources and the analysis are in the [build plan](docs/BUILD_PLAN.md#1-problem-and-evidence).

## What it will do

| Component | What it does | Planned for |
| --- | --- | --- |
| Contract | Decode caps, JSON Schema, CEL invariants, diff budgets, consumer tests, rings and a failure policy, in one reviewed file | v0.1 |
| Gate and GitHub Action | Checks a candidate against its contract and a baseline: PASS, NEEDS_APPROVAL or BLOCK | v0.1 |
| Loader and sidecar agent | Applies a new version only if it passes, and keeps the last known good when one doesn't | v0.2 |
| Publish and stores | Write-once versions and a compare-and-swap pointer, with revert and a break-glass pin | v0.3 |
| Consumer tests | Run the real consumer on the candidate, sandboxed, before publishing | v0.3 |
| Rings and promoter | Roll a version out ring by ring, watch health, and revert automatically | v0.4 |

The [architecture overview](docs/architecture.md) explains how the parts fit together.

## The replay suite

Every claim above gets a test you can run. The `replay/` directory holds one scenario per outage shape: synthetic data shaped like the real incident, a tiny consumer that fails the same way, and the expected outcome at each stage, from the gate to the rings.

## Non-goals

- Per-user targeting or experimentation: use a feature-flag tool.
- Secrets management.
- A general CD system, or a replacement for Argo Rollouts or Flagger for pods.

## Contributing

Contributions are welcome; see [CONTRIBUTING.md](CONTRIBUTING.md). Everyone taking part follows the [Code of Conduct](CODE_OF_CONDUCT.md). To report a vulnerability, see [SECURITY.md](SECURITY.md).

## License

[Apache License 2.0](LICENSE)
