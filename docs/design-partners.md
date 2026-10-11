# Design partners

0cular gives config and data changes the safety net that code already has: it checks a change before it's published, keeps the last known good copy when a service loads it, and rolls it out in rings with automatic revert.

It's pre-alpha, and it should be shaped by teams who live with the problem. I'm looking for one or two **design partners**: teams that reload configuration or data live and want to stop a bad change before it reaches every consumer at once.

## Is this you?

You publish something that services pick up at runtime without a deploy, for example:

- rate limits, quotas, routing tables or service discovery records;
- detection rules, WAF rule sets or blocklists;
- feature files, model files or thresholds;
- prompts, model IDs, agent tool manifests or guardrail rules.

And a bad version of it could take something down: a list grows past a limit nobody wrote down, a field arrives blank, a record ends up empty. Five of the largest outages since mid-2024 started that way, and the [replay suite](../replay/) reconstructs their failure shapes from the public postmortems, with synthetic data.

## What you'd get

- **Hands-on help.** I write the first contract for one of your artifacts with you, and wire the check into your CI or publish path.
- **Your failure shapes, caught.** If a past incident of yours fits, it becomes a replay scenario with synthetic data, so the fix is tested on every change, with your approval of what's described.
- **Priority.** The stores, formats and features you need come first: S3, Consul, etcd, JSON, YAML and whatever else your publish path uses.
- **A say in the contract format** while it's still `v1alpha1`, before anything is locked in.
- **A direct line to the maintainer**, and your blockers fixed first.

## What I'd ask

- **One artifact to pilot.** Start with the check in CI (v0.1): it needs no change to your production systems.
- **Regular feedback:** a 30-minute call about every two weeks, plus issues or email as things come up.
- **Honesty**, including "this doesn't fit how we work".
- **Optionally, a case study** later, only with your consent. Anonymized is fine.

## What it isn't

- **Not production-ready yet.** Contracts are `v1alpha1` and can change. The loader that runs inside your services comes in v0.2, and you decide if and when it goes there.
- **Not a paid service.** There's no SLA, just one maintainer who'll prioritize your needs.
- **Not a feature-flag or experimentation tool.** For per-user targeting, keep your flag tool. 0cular is for the config and data that flags don't cover.

## Roadmap

| Release | What you can use |
| --- | --- |
| v0.1 Check | The contract, the `check` command and a GitHub Action: PASS, NEEDS_APPROVAL or BLOCK before you publish |
| v0.2 Keep | A loader library and a sidecar agent that keep the last known good when a bad version arrives |
| v0.3 Publish | Gated publishing to S3, Consul or etcd that refuses stale writers, plus revert and tests against your real consumer |
| v0.4 Rings | Rollout in rings, with health checks and automatic revert |

The details are in the [build plan](BUILD_PLAN.md).

## Get in touch

Email [isaactillema@isaactillema.com](mailto:isaactillema@isaactillema.com) with a few lines:

- what you reload live, and roughly how many services read it;
- how a change gets published today;
- what went wrong last time, if anything did.

Anything you share stays private unless you agree otherwise.
