# BUILD_PLAN.md — 0cular

This is the phased plan for building 0cular: the what, why, how and when.

How the plan is kept:
- The status board and the task checkboxes stay current.
- A change to the plan, the stack or a safety default needs the maintainer's approval first. It's then recorded as an ADR in `docs/adr/` (unplanned ADRs start at 0011) and reflected here.
- Every target number here is a starting point to calibrate. None is lowered silently.

## Status board

| Field | Value |
| --- | --- |
| Current phase | Phase 0 — Foundations: complete |
| Last updated | 2026-10-11 |
| Next task | Phase 1, task 1.1 `contract`, once the maintainer starts Phase 1 |
| Blocked on | The maintainer's go-ahead for Phase 1 |
| Latest release | None |
| Planned releases | v0.1 Check, v0.2 Keep, v0.3 Publish, v0.4 Rings |
| Design partners | None yet (target: 1 by the end of Phase 1) |

## Contents

1. Problem and evidence
2. Context: landscape, research and positioning
3. Solution design
4. Tech stack in detail
5. Phases and milestones
6. Testing and evaluation
7. Release and open-source operations
8. Risk register
9. Rewards and success metrics
10. Decisions and open questions
11. Timeline
12. Appendices
13. Sources

---

## 1. Problem and evidence

Code ships through staged, health-checked rollouts. Config and data usually don't. They move through fast global channels because speed is the point: threat signatures, bot models, quota rules and routing plans must take effect quickly. Large cloud services push hundreds of configuration changes a day (ctest, OSDI 2020). When one of those changes breaks a consumer, it can break every consumer of that kind at once.

### 1.1 Five incidents, one pattern

| Date | What changed | What broke | Impact | Control that would likely have caught it |
| --- | --- | --- | --- | --- |
| Dec 5, 2025 | Cloudflare turned off an internal WAF test tool through its global config system while mitigating a React Server Components CVE | The change reached the fleet in seconds and hit a years-old bug in the FL1 proxy | About 25 min; about 28% of Cloudflare's HTTP traffic | Consumer-in-the-loop test; staged rings |
| Nov 18, 2025 | A database permission change made a query return duplicate rows, doubling the size of the Bot Management feature file | The FL2 proxy preallocated memory for 200 features (normal use: about 60) and panicked on the file with more than 200, returning 5xx errors; the older FL proxy didn't error but gave all traffic a bot score of 0. The file was regenerated every five minutes and published to the entire network | Failures began at 11:20 UTC; core traffic was largely flowing again by about 14:30; everything was normal by 17:06 UTC | Invariant on the cap; diff budget on item count; fail-stale loader |
| Oct 19–20, 2025 | A race in DynamoDB's DNS automation let a stale plan overwrite a newer one, and cleanup then deleted it | The regional endpoint was left with an empty DNS record that automation couldn't repair | DynamoDB impaired about 3 h; broader us-east-1 impact about 14.5 h | Invariant that the record is never empty; a publish that asserts its parent (stale-writer protection) |
| Jun 12, 2025 | A quota policy with unintended blank fields was written to Service Control's regional tables | It replicated globally in seconds. A May 29 code path without error handling or a feature flag crash-looped on the null value | About 3 h for most products | Non-null invariant; fail-stale loader with crash fallback |
| Jul 19, 2024 | CrowdStrike Channel File 291 (Rapid Response Content) | The Template Type defined 21 input fields; the sensor code supplied 20, so the Content Interpreter read out of bounds and crashed | About 8.5M Windows devices (Microsoft's estimate) | Consumer-in-the-loop test; staged rings; crash fallback |

The "control" column is our judgment from the postmortems, not the companies' claims. None of these changes went through a code pipeline, so the checks have to sit in each publish path, not only in CI. Dates and times are as each postmortem gives them: UTC, except AWS's, which are US Pacific.

### 1.2 Why it persists

- **Speed is a feature.** Cloudflare says Quicksilver, its configuration system, reaches 90% of its servers within seconds, and that "the speed is a feature". It named that ability to deploy configuration globally within seconds as "the core commonality" of its two late-2025 outages.
- **Config canaries take effort.** Before its Code Orange program, Cloudflare could stage config rollouts, but doing so took per-team effort and wasn't applied consistently.
- **Value checks miss consumer limits.** Validators that check values alone miss changes that break the code using them (ctest).
- **Consumers trust their inputs.** In its Code Orange plan, Cloudflare wrote that instead of panicking on the Nov 18 feature file, "there should have been a sane set of validated defaults".

**Problem statement.** Teams that reload config or data live have no open, store-agnostic way to:
- check a candidate against the consumer's real limits before publishing it;
- stage it to a slice of consumers;
- revert automatically;
- keep consumers on the last known good when something slips through.

---

## 2. Context: landscape, research and positioning

### 2.1 What exists

| Approach | Examples | Covers | Where it stops |
| --- | --- | --- | --- |
| Schema and policy checks | JSON Schema, CUE, Pkl, KCL, OPA/Conftest, Kyverno | Shape and value rules in CI or admission | Blind to consumer limits and to change size |
| Config testing in context | ctest / openctest (OSDI 2020), IDoCT | Runs code-level tests against the changed config | Research prototype; per-language effort |
| Workload progressive delivery | Argo Rollouts, Flagger, Spinnaker with Kayenta | Pod rollouts with metric analysis. Flagger also canaries changes to ConfigMaps and Secrets that a Deployment references, by rolling pods | Data that services reload live, without a redeploy |
| Environment promotion | Kargo | Code and config promoted stage by stage, with verification | Not fleet-percentage rollout of live data |
| Feature-flag rollouts | Unleash, Flipt, OpenFeature's flagd, GO Feature Flag | Percentage rollouts of flags | Not arbitrary files, rule sets or records |
| In-house config canaries | Cloudflare Snapstone, Meta Configerator, CrowdStrike staged rings | Health-mediated config rollout | Not available outside those companies |

### 2.2 What 0cular takes from prior work

| Source | Idea used |
| --- | --- |
| Cloudflare Code Orange: Fail Small (plan Dec 2025; completed spring 2026) | Health-mediated deployment for config; "fail stale" to the last known good where possible, else fail open or closed |
| Google Cloud's Service Control report (Jun 2025) | Incremental propagation of globally replicated data; feature-flag new code paths; fail open; randomized backoff |
| CrowdStrike RCA (Aug 2024) | Field-count validation; bounds checks; staged deployment rings; customer control |
| ctest (OSDI 2020) | Test config changes against the code that uses them |
| Kayenta (Spinnaker) | Statistical canary judgment (Mann–Whitney U) on metrics |
| Configerator (Meta, SOSP 2015) | Config as code: review, validation and canarying as one pipeline |

### 2.3 Positioning

0cular is the open, store-agnostic safety layer for live-reloaded config and data. It sits:
- **in the publisher's path**, as a gate and publish step;
- **in the consumer**, as a loader library or sidecar agent;
- **beside monitoring**, as a promoter.

It complements Argo Rollouts and Flagger (pods), flag tools (per-user flags) and policy engines (value rules).

### 2.4 Who it's for

| Persona | Live-reloaded data |
| --- | --- |
| Platform or SRE engineer at a mid-size company | Routing tables, rate limits, quota policies |
| Security engineering | Detection rules, WAF rule sets, blocklists |
| ML platform | Model files, feature definitions, thresholds |
| AI platform | Prompts, model IDs, agent tool manifests, guardrail rules |
| GitOps teams | Config committed to repos and synced to services |

---

## 3. Solution design

### 3.1 Architecture

```text
 publisher (job, automation, human)
      │ candidate
      ▼
 ┌────────────────────────────┐
 │ gate                       │◄── contracts/*.yaml (reviewed like code)
 └─────────────┬──────────────┘
               │ PASS (or approved): write the blob, then CAS the pointer
               ▼
 ┌────────────────────────────┐   CAS: promote / revert   ┌──────────────┐
 │ store: blobs + pointer     │◄──────────────────────────┤ promoter     │
 │ file, S3, Consul, etcd     │                           └──────▲───────┘
 └─────────────┬──────────────┘                                  │ health by config_version
               │ poll or watch (read-only Source)                │
               ▼                                          ┌──────┴───────┐
 ┌────────────────────────────┐          metrics          │ Prometheus   │
 │ loader (library) or agent  │──────────────────────────►│              │
 │ validate → swap → LKG      │                           └──────────────┘
 └────────────────────────────┘
```

### 3.2 Contract spec (`0cular/v1alpha1`)

| Field | Type | Meaning |
| --- | --- | --- |
| `apiVersion`, `kind`, `metadata.name` | string | `0cular/v1alpha1`, `Contract`, unique artifact name |
| `artifact.format` | enum | `json` and `yaml` in v0.1; `toml`, `csv` and `bytes` (size and hash only) later |
| `artifact.maxBytes` | size | Hard cap; checked before parsing |
| `artifact.maxDepth`, `artifact.maxNodes` | int | Decode caps (§3.3); defaults 64 and 1,000,000 |
| `artifact.csv` | object | `delimiter`, `maxRows` (CSV only, later) |
| `schema.jsonSchema` | path | Draft 2020-12 schema; applied after decoding |
| `invariants[]` | list | `name`, `expr` (CEL → bool), `message`; always BLOCK on false or error. Absolute limits go here |
| `diffBudgets[]` | list | Measure and collection budgets on change against the baseline; NEEDS_APPROVAL on breach unless `overridable: false` |
| `consumerTests[]` (v0.3) | list | `name`, `run` (argv), `container`, `sandbox`, `env`, `timeout`, `expectExit`, optional `replay` |
| `rollout` (v0.4) | object | `rings[]` (`name`, `upTo` bucket, `minInstances`, `instances`), `bake`, `health` (§3.9) |
| `onFailure` | object | `noLastKnownGood`: `closed` (default) or `open`; `trustLastKnownGood`: default `false` (ADR-0009). `parseError`, `invariantViolation` and `fetchError` accept only `stale`; lint rejects `open` and `closed` there |

**CEL environment:**
- `self` (the candidate), `baseline` (or `null` under `--no-baseline`) and `meta` (`bytes`, `sha256`, `format`, `key`).
- Standard CEL plus the cel-go string, list, set and math extensions, each pinned to a version (for example `ext.Strings(ext.StringsVersion(N))`) so cel-go upgrades don't change behavior. `cel.CrossTypeNumericComparisons(true)` lets `self.limit > 0` compare a double with an int.
- `zerocular.unique(list)`: a linear-time uniqueness check, written in Go with a hash set (Appendix A shows why). The `zerocular` namespace is the letter-first form of the name (ADR-0001).
- **Limits:** a runtime cost limit (default 1,000,000) and a 2 s timeout per expression, both configurable downward only. The timeout uses `prg.ContextEval(ctx, vars)` with `cel.InterruptCheckFrequency(n)`, since cost tracking alone doesn't bound time. `contract lint` also rejects an expression whose static estimate (`env.EstimateCost`, with sizes from `artifact.maxBytes` and the schema's `maxItems` and `maxLength`) exceeds the ceiling.

**Measure budgets** evaluate `measure` on the candidate and on the baseline:

```yaml
diffBudgets:
  - name: feature-count-stable
    measure: size(self.features)       # CEL; evaluated on candidate and baseline
    maxChangePct: 20                   # |cand − base| / max(|base|, 1) × 100
  - name: size-stable
    measure: meta.bytes
    maxChangePct: 50
```

Supported limits: `maxChangePct`, `maxIncrease`, `maxDecrease`. Budgets bound only change against the baseline. Absolute limits are invariants, and lint rejects `minValue` and `maxValue` in a budget:

```yaml
invariants:
  - name: endpoints-never-empty
    expr: size(self.endpoints) >= 1
    message: an empty endpoint list takes the region offline
```

**Collection budgets** compare keyed lists:

```yaml
  - name: policy-churn
    collection: self.policies          # list on candidate and baseline
    key: item.id                       # identity per element
    maxRemoved: 5
    maxAdded: 50
    maxChangedPct: 10
```

**Consumer tests (v0.3)** run the real consumer against the candidate:

```yaml
consumerTests:
  - name: proxy-loads-candidate
    container: ghcr.io/example/proxy:2.3   # default sandbox: --network none --read-only
    run: ["proxy", "--check-config", "{{.CandidatePath}}"]
    timeout: 60s
    expectExit: 0
```

### 3.3 Evaluation semantics

**Canonical decode model**, the same for every format (full rules in `docs/contract-spec.md`):
- Numbers are CEL `double` (integers above 2^53 lose precision). Lint warns on int-literal arithmetic: write `self.x + 1.0`, not `self.x + 1`.
- Datetimes stay RFC 3339 strings, with no YAML timestamp conversion. Map keys must be strings; any other key is a decode error.
- CSV (later) needs a header row and decodes to a list of maps keyed by it, with string values.
- Caps on nesting depth (default 64), nodes after YAML alias expansion (default 1,000,000) and YAML aliases (100 per document) stop billion-laughs inputs.

**Steps:**
1. **Envelope.** Size ≤ `maxBytes`; decodes under the model above; text formats must be valid UTF-8. Failure → BLOCK.
2. **Schema.** Any violation → BLOCK, listing up to 20 violations with JSON pointers.
3. **Invariants.** False, an error, an exhausted cost limit or a timeout → BLOCK.
4. **Diff budgets.** Skipped with a note under `--no-baseline`. A breach → NEEDS_APPROVAL, or BLOCK if `overridable: false`.
5. **Consumer tests (v0.3).** Any failure or timeout → BLOCK.

**Verdict:** BLOCK if any step blocks; else NEEDS_APPROVAL if any budget is breached without a valid approval (§3.4); else PASS. An evaluator error is always BLOCK (fail-safe).

Each check result carries `name`, `kind`, `status`, `message`, `evidence` and `durationMs`, where evidence shows the computed values. For example: `size(self.features) = 240; limit 200`, or `feature-count-stable: 120 → 240 (+100%); budget 20%`.

Results are ordered by step, then by contract order. JSON output carries `schemaVersion`.

### 3.4 Gate

- **CLI:**

  ```text
  0cular check --contract C --candidate PATH (--baseline PATH|REF | --no-baseline)
               [--approve --approver NAME --reason TEXT] [--format text|md|json]
  ```

  One of `--baseline` and `--no-baseline` is required; there's no silent default. From v0.3 a `REF` can name a store (`s3://config-bucket/bot-features`): the version its pointer names.
- **Library:**

  ```go
  gate.Check(ctx, contract, candidate, baseline,
      gate.WithApproval(gate.Approval{By, Reason, Expires, CandidateDigest, ContractHash, Budgets}))
  ```

  It returns `eval.Result`.
- **Approvals** cover NEEDS_APPROVAL only; they can't override BLOCK. An approval binds to the `CandidateDigest`, the `ContractHash` and the evaluated `Budgets` (each breached budget's name and values); any change invalidates it. Approvals are recorded in the result and, from v0.3, in publish metadata. How an approval is obtained is the caller's job: a CI environment approval, chatops, or a human at a terminal.

### 3.5 Versions, pointer and stores

Versions are content-addressed (`sha256:<hex>`). For each artifact, a store holds write-once **blobs**, each with a write-once metadata object (version, parent, contract hash, gate result digest, approvals, publisher, time), and one small mutable **pointer**.

**Pointer format** (task 2.2, so file and HTTP sources use it before the stores ship): JSON with `formatVersion`, `artifact`, `generation` (for compare-and-swap), `version`, `parent`, `updatedAt` and `updatedBy`. Sources read `pointer.json` and `blobs/sha256-<hex>` from a directory or under a base URL. In v0.4, `formatVersion: 2` adds the rollout manifest (§3.9); a loader that doesn't know a format version keeps its LKG, so old loaders never bypass rings.

**Interfaces.** Loaders and agents depend only on the read-only `Source`; publishers and the promoter use `Store`:

```go
type Source interface {
    Pointer(ctx context.Context, artifact string) (Pointer, error)
    Blob(ctx context.Context, artifact string, v Version) ([]byte, error)
}

type Store interface {
    Source
    PutBlob(ctx context.Context, artifact string, data []byte, meta Meta) (Version, error) // write-once (If-None-Match: *)
    CompareAndSwapPointer(ctx context.Context, artifact string, next Pointer, expectedGeneration uint64) error
    History(ctx context.Context, artifact string, limit int) ([]Meta, error)
}
// Optional: type Watcher interface { Watch(ctx, artifact) (<-chan Event, error) }
```

**Publish asserts its parent (v0.3).** `publish` reads the pointer (generation G, version P), gates the candidate against P, writes the blob, and swaps the pointer only if it's still at G. Otherwise it fails (exit 1) without retrying. Automation that builds a candidate from a known version passes it as `--parent <version>`, and publish fails if the pointer has moved past it. That stops the DynamoDB shape, where a delayed writer applied an old plan over a newer one.

**`store verify`** probes that the backend enforces conditional writes: a write with a wrong ETag (or index, or revision) must get 412 or its equivalent, and a repeated write-once write must fail. `publish` runs the probe once per process and refuses a store that fails it.

| Adapter | Blobs (write-once) | Pointer compare-and-swap | Change notification | Size limit |
| --- | --- | --- | --- | --- |
| `file` | Content-addressed files | Atomic rename + lock file | Polling (fsnotify optional) | Disk |
| `s3` (S3-compatible) | `If-None-Match: *` | `If-Match` with the pointer's ETag | Polling | 5 GB per PUT |
| `consul` | `?cas=0` | `?cas=<ModifyIndex>` | Blocking queries | 512 KB per value |
| `etcd` | Transaction on `create_revision = 0` | Transaction comparing `mod_revision` | Watch | 1.5 MiB per request (default) |
| `azblob` (later) | `If-None-Match: *` | `If-Match` with the ETag | Polling | — |

Adapters refuse blobs over their limit, so keep `maxBytes` below it.

### 3.6 Loader (Go library, v0.2)

**Slots:** `applied` is the last version swapped in. `good`, the LKG, is a version that stayed applied for `goodAfter` (default 10m) with no crash or hook failure.

**Lifecycle:**
1. **Start.** Load both slots and verify their SHA-256. Re-validate the LKG against the current contract (ADR-0009): if it fails, there's no LKG unless the contract sets `onFailure.trustLastKnownGood: true`, and health reports `lkg_invalid`. A pending marker means the process died during or soon after a swap, the crash-loop shape of the CrowdStrike and Google incidents: serve `good`, and skip the pending version until a newer one is assigned. Otherwise serve `applied`, re-arming the marker if it isn't `good` yet. Then fetch the assigned version (the pointer's; from v0.4, the ring's) within `fetchTimeout`.
2. **Apply.** Fetch → verify digest → decode → contract checks → `Validate` hook (can reject) → pending marker → atomic swap into `applied` → `OnChange` hooks. After `goodAfter` the version becomes `good` and the marker is removed; a clean `Close()` removes it too.
3. **Steady state.** Poll with jitter, or watch. Fetch errors back off exponentially with jitter, capped at `pollEvery`.
4. **Failure.** With an LKG, every failure keeps the current version and reports `stale`. A version whose hash doesn't match is never applied. A recovered panic in a hook rejects that version: in `Validate` the swap doesn't happen, and in `OnChange` the loader swaps back.
5. **No LKG.** Under `noLastKnownGood: closed` (default), `Start` returns an error and the app decides. Under `open`, a provided default value is used and the loader reports `degraded`.

**Usage:**

```go
type BotFeatures struct {
    Features []Feature `json:"features"`
}

c, err := contract.LoadFile("contracts/bot-features.yaml")
if err != nil { return err }

l, err := loader.Start(ctx, loader.Options[BotFeatures]{
    Source:   httpsource.New("https://config.internal/bot-features/"), // any store.Source
    Contract: c,
    CacheDir: "/var/cache/0cular",
    Validate: checkFeatures, // func(context.Context, BotFeatures) error; before the swap, can reject
    OnChange: rebuildIndex,  // func(old, cur BotFeatures, v loader.Version); after the swap
})
if err != nil { return err }   // no valid config and the policy is closed
defer l.Close()

cfg, version := l.Current()     // always a validated snapshot
```

**On disk** (`<CacheDir>/<artifact>/`): `blobs/<sha256>.bin`, `state.json` (both slots and the skipped versions), `pending.json` and, from v0.3, `PIN`. Writes go temp file → fsync → rename → fsync of the directory, through a build-tagged helper that skips the directory fsync on Windows. Retention keeps the last 3 blobs, and never prunes `good`, the manifest's `lastKnownGood` or a pinned version.

**Metrics:**

| Metric | Type | Labels |
| --- | --- | --- |
| `zerocular_config_info` | gauge (=1) | artifact, config_version, ring, instance_id |
| `zerocular_loads_total` | counter | artifact, result (`applied`, `rejected`, `unchanged`, `fetch_error`) |
| `zerocular_check_failures_total` | counter | artifact, check |
| `zerocular_stale` | gauge (0/1) | artifact |
| `zerocular_last_success_timestamp_seconds` | gauge | artifact |

Metric names use the letter-first `zerocular_` prefix, because a Prometheus metric name can't start with a digit (ADR-0001). `config_version` is the version being served, and only that one. `instance_id` lets the promoter build ring lists (§3.9); it adds no series, since each instance already has its own.

**Health.** `Health()` returns a state plus the current version's age:
- `ok`: serving the assigned version.
- `stale`: serving an older version after a failed fetch or check, or after a crash fallback.
- `degraded`: no LKG; serving the `open` default.
- `lkg_invalid`: the cached LKG failed re-validation. Until a valid version arrives, the loader serves the `open` default, or the old LKG under `trustLastKnownGood: true`.
- `failed`: nothing valid to serve. Only the agent reports it; `loader.Start` returns an error instead.

Every state except `failed` counts as ready. Use `Health()` for readiness only, never for a liveness probe: a restart loses the in-memory state and amplifies an outage.

**Version join for health queries.** App metrics don't need a `config_version` label: join on the info metric in PromQL. The join labels are configurable (`health.joinLabels`, default `[namespace, pod]`):

```text
sum by (namespace, pod) (rate(http_requests_total{code=~"5.."}[5m]))
  * on (namespace, pod) group_left (config_version)
  zerocular_config_info{artifact="bot-features"}
```

### 3.7 Agent mode (v0.2): fail-stale for any language

```text
0cular agent --source https://config.internal/bot-features/ --contract contracts/bot-features.yaml \
             --out /shared/config/bot-features.json [--signal HUP --pid-file /shared/run/app.pid] [--metrics :9464]
```

The agent is a sidecar that embeds the loader. It writes the current validated version atomically to `--out`, and optionally signals the app. Apps in any language just read a file that is always valid. Its `/readyz` endpoint, on the metrics port, passes once the first valid file exists. From v0.3, `--source` also takes store URLs. Native Python and TypeScript loaders come later, checked by a shared conformance suite.

On Kubernetes:
- Mount the shared directory, not a single file: a rename isn't visible through a `subPath` mount.
- Run the agent as a native sidecar (an init container with `restartPolicy: Always`) with a `startupProbe` on `/readyz`, so the app starts only after the first valid file exists.
- Set `shareProcessNamespace: true` only if the agent signals the app.

### 3.8 Consumer-in-the-loop tests (v0.3)

```text
0cular test-consumer --contract C --candidate PATH [--baseline PATH]
```

- Runs each `consumerTests` entry. `{{.CandidatePath}}` and `{{.BaselinePath}}` are substituted.
- **Sandbox:** by default, a container run with `--network none --read-only`, with the candidate mounted read-only. A raw argv on the host needs an explicit `sandbox: none`.
- Optional `replay`: a JSONL file of HTTP requests sent to the consumer running as a server, with `maxErrorRate`. A helper container in the consumer's network namespace sends them, so the consumer still has no outside network.
- Defaults: 60 s timeout, `expectExit: 0`.
- This is the step that catches field-count mismatches and latent consumer bugs, which no schema can see.

### 3.9 Revert, pin and rings

**Revert and pin (v0.3):**
- `0cular revert <artifact> [--to <version>]` moves the pointer back to its parent (or the given version) through the same compare-and-swap. From v0.4 it sets every ring to the fleet LKG and pauses.
- **Break-glass pin.** `ZEROCULAR_PIN_<NAME>=lkg|<version>`, or a `PIN` file in the cache directory, makes the loader ignore the pointer and serve that version from its cache (`lkg` is the `good` slot). Use it when the store or pointer is unavailable or wrong. `<NAME>` is the artifact name uppercased, with non-alphanumerics turned into `_`: `ZEROCULAR_PIN_BOT_FEATURES`. Environment names can't start with a digit, so the prefix uses the letter-first form; the loader, agent and CLI warn about any variable that starts with the digit spelling, `0CULAR_` (ADR-0001).

**Manifest (v0.4).** The rollout manifest lives in the pointer, as `formatVersion: 2`:

```json
{
  "formatVersion": 2,
  "artifact": "bot-features",
  "generation": 57,
  "version": "sha256:bb…",
  "parent": "sha256:aa…",
  "lastKnownGood": "sha256:aa…",
  "rings": [
    { "name": "canary", "upTo": 100,   "version": "sha256:bb…", "instances": ["edge-7"] },
    { "name": "early",  "upTo": 1000,  "version": "sha256:aa…" },
    { "name": "all",    "upTo": 10000, "version": "sha256:aa…" }
  ],
  "rejected": ["sha256:cc…"],
  "paused": false,
  "updatedAt": "2026-10-09T15:04:05Z",
  "updatedBy": "promoter"
}
```

**Ring assignment:**
- `bucket` = the first 8 bytes of SHA-256(instanceID) as a big-endian uint64, mod 10000. The instance ID comes from config: env, hostname or pod name.
- An instance listed in a ring's `instances` takes that ring; others take the first ring with `bucket < upTo`.
- **Small fleets:** when fewer than `minInstances` instances fall in a ring, the promoter adds the instances with the next-lowest buckets (from the `instance_id` label) to that ring's `instances`.

**Health** (ADR-0008):
- **Queries** (`perInstance`) are PromQL templates that return one value per instance, aggregated `by` the join labels; the promoter adds the version join (§3.6). `{{.Window}}` is the rate window.
- **Liveness:** `count by (config_version) (zerocular_config_info{artifact="…", ring="…"})` against the ring's expected size (its instance count before the change), plus `up`. A crashed consumer stops reporting.
- **Adoption:** bake time starts when about 90% of the ring's expected instances report the candidate, not when the manifest changes. Evaluation starts one rate window later.
- **Control:** the instances in rings still on the fleet LKG. The final ring has no untouched control, so its own pre-rollout window serves as the control, and any `slo` limits apply.

**Promoter loop**, as a long-running process or a CI step:

```text
for the ring holding the candidate:
  wait for adoption (≥ 90% of expected instances on the candidate); timeout → FAIL
  wait one rate window
  every interval until the bake time has passed:
    Prometheus unreachable, or no data for candidate and control → PAUSE (never promote on missing data)
    candidate reports nothing, falls below adoption or has up == 0, while control reports → FAIL
    for each health query q (higher = worse unless declared otherwise):
      c = median over candidate instances
      b = median over the control (final ring: its own pre-rollout window)
      FAIL if c − b > maxAbsDegradation[q], or (b > 0 and c / b > maxRatio[q]), or c > slo[q]
    any FAIL → abort the bake early
  at the end of the bake, if both groups have ≥ 10 instances:
    FAIL if one-sided Mann–Whitney U p < 0.01 in the worse direction and c − b ≥ minEffect[q]
    (ErrSamplesEqual from go-moremath counts as a pass)
  all pass → move the candidate to the next ring
  any FAIL → revert: every ring → fleet LKG; add the candidate to "rejected"; paused = true
after the final ring passes its bake: lastKnownGood = candidate
```

`minEffect[q]` is the smallest difference between the medians, in the query's units, that counts as a regression. If go-moremath's license doesn't fit, v0.4 ships the thresholds alone and defers the U test.

### 3.10 Postmortem replay suite

The scenarios use synthetic data with shapes inspired by public postmortems. They aren't real data and aren't affiliated with the companies. Each lives in `replay/<scenario>/` and contains:
- `README.md`: a link to the public postmortem, what's reconstructed, and a clear statement that the data is synthetic;
- `contract.yaml`, `baseline.*` and `candidate.*`;
- `consumer/`: a tiny Go program that reproduces the failure shape;
- `expected.yaml`: per stage (`check`, `loader`, `publish`, `rings`), the expected verdict or outcome and the failing check names, under `replay/expected.schema.json`.

`replay_test.go` builds the stubs once, in `TestMain`, and proves that each exits 0 on its baseline and fails on its candidate. It then asserts the stages whose features exist and skips the rest. The stubs fail on purpose, so lint excludes `replay/.*/consumer/` from the rules they break.

| Scenario | Shape | v0.1 Check | v0.2 Keep | v0.3 Publish | v0.4 Rings |
| --- | --- | --- | --- | --- | --- |
| `doubled-feature-file` | Duplicate rows double a feature list from 120 to 240 entries (synthetic numbers); consumer panics above 200 | BLOCK (invariant + budget) | Loader rejects it and keeps the LKG | BLOCK | — |
| `blank-policy-fields` | A policy record missing its limit; consumer dereferences null | BLOCK (invariant) | Loader rejects it and keeps the LKG | BLOCK | — |
| `emptied-record` | A delayed writer's old plan replaces a newer one; cleanup leaves an empty address list | BLOCK (invariant: never empty) | Loader rejects it and keeps the LKG | The stale plan's publish fails its parent check | — |
| `field-count-mismatch` (added in v0.2) | The definition declares 21 fields and the consumer supplies 20. Earlier content wildcarded field 21; the new content is the first to match on it, and the consumer crashes | — | Passes the checks (a schema built from the producer's definition can't see it); the consumer crashes after the swap and restarts on `good` | BLOCK (consumer test) | Canary catches it if the gate is bypassed |
| `killswitch-unhandled-rule` (added in v0.3) | A killswitch on an "execute" rule leaves a result field nil in the consumer | — | — | BLOCK (consumer test) | Canary catches and reverts it if the gate is bypassed |

### 3.11 Interfaces

**CLI:**

| Command | Phase | Purpose |
| --- | --- | --- |
| `0cular check` | 1 | Gate a candidate; report and exit code |
| `0cular diff` | 1 | Measure deltas + RFC 6902 summary between two versions |
| `0cular contract lint`, `0cular contract schema` | 1 | Validate contract files, including static CEL cost; print the contract JSON Schema |
| `0cular replay run [scenario]` | 1 | Run the replay suite (CI and demos) |
| `0cular agent` | 2 | Sidecar mode |
| `0cular store verify` | 3 | Probe that the store enforces conditional writes |
| `0cular publish` | 3 | Gate against the parent, write the blob, compare-and-swap the pointer |
| `0cular get`, `history`, `status` | 3 | Inspect versions, approvals and the LKG |
| `0cular test-consumer` | 3 | Consumer-in-the-loop tests |
| `0cular revert` | 3 | Pointer back to its parent; from Phase 4, every ring back to the fleet LKG |
| `0cular rollout start`, `status`, `step`, `pause`, `resume` | 4 | Manage rings |
| `0cular promoter run` | 4 | Long-running promoter, or a one-shot CI step |

Exit codes are in Appendix F.

**GitHub Action** (repo-stored config, v0.1):
- **Placement:** a composite action in `action/`, used as `uses: itillema/0cular/action@<sha>`. GitHub Marketplace lists only an action whose `action.yml` is at a repository root (one per repo), so this one stays unlisted (§10).
- **Inputs:** `contract`, `candidate`, `baseline` (default: the candidate's path on the base ref), `format`, `comment`, `mode` (`check`, or `comment` in the `workflow_run` workflow).
- **Contract from the base ref,** so a PR can't weaken its own contract. A contract missing on the base ref is a usage error (exit 64); a new candidate gets `--no-baseline`, noted in the summary.
- **Output:** annotations (workflow commands) and a Job Summary, which work on fork PRs. A PR comment doesn't, because a fork PR's `GITHUB_TOKEN` is read-only. So for the optional sticky comment, the `pull_request` workflow uploads the report as an artifact, and a separate `workflow_run` workflow, which never checks out or runs PR code, posts or updates the comment (Appendix C). It treats the artifact, PR number included, as untrusted.
- **Binary:** built from source at the action's ref until a first release exists; after that, the release binary, with its checksum and cosign signature verified.
- **Permissions:** `contents: read`; `pull-requests: write` and `actions: read` only in the `workflow_run` workflow.

**Packages:** `contract`, `eval`, `gate`, `report`, `loader`, `agent`, `store` (`Source`, `Store`, the sources and one subpackage per adapter), `rollout`.

### 3.12 Security model

- **Contracts are code.** They're reviewed and owned (CODEOWNERS), and they're the only source of commands for consumer tests. The Action reads them from the base ref.
- **Artifacts are untrusted data.** They get size and decode caps, strict decoding and fuzzed parsers, and nothing in them is ever executed.
- **Reports escape untrusted text.** Artifact keys and values, contract messages and paths are untrusted in every report:
  - HTML, if added, uses `html/template`;
  - Markdown (Job Summary, PR comments) escapes metacharacters, neutralizes @mentions and truncates long fields;
  - annotations escape workflow-command data and properties, as `@actions/core` does;
  - text output replaces control and bidi characters.

  Golden tests feed every renderer HTML tags, backticks, pipes, @mentions, newlines, very long strings and Unicode bidi characters.
- **Least privilege.** Publisher credentials can write blobs and the pointer; consumers read through `Source`; the promoter can write only the pointer.
- **Integrity.** SHA-256 is verified on every load. Optional Sigstore signatures on artifacts come later.
- **Supply chain:** cosign keyless signatures, SLSA provenance, a syft SBOM, Actions pinned by SHA, zizmor, Dependabot and OpenSSF Scorecard, plus minimal dependencies and govulncheck in CI.

---

## 4. Tech stack in detail

Versions seen on the Go module proxy on 2026-10-09; re-check at kickoff. `go.mod` pins them. The current Go releases on that date were go1.27.2 and go1.26.9. `go.mod` says `go 1.26.0`, and CI tests on 1.26 and 1.27.

| Area | Choice (seen 2026-10-09) | Why | Alternatives considered |
| --- | --- | --- | --- |
| Language | Go ≥ 1.26 | Static binaries; first-class in infra (Kubernetes, Prometheus, etcd) | Rust (steeper; smaller infra-config ecosystem); Python (runtime footprint in sidecars) |
| CLI | spf13/cobra v1.10.2 | Standard for Go CLIs | urfave/cli, kong |
| YAML | go.yaml.in/yaml/v3 v3.0.5 | The YAML org's v3 line: frozen, with security fixes only. Its stable successor, v4, is still a release candidate; move once v4.0.0 ships (ADR-0002). gopkg.in/yaml.v3 had its last release (v3.0.1) in 2022 | goccy/go-yaml v1.19.2 |
| TOML (later) | pelletier/go-toml/v2 v2.4.3 | Fast, maintained | BurntSushi/toml |
| JSON Schema | santhosh-tekuri/jsonschema/v6 v6.0.3 | Draft 2020-12, good errors | gojsonschema (older drafts) |
| Expressions | google/cel-go v0.30.0 | Cost limits and estimates, interruptible evaluation; used by Kubernetes admission policy | OPA v1.21.1 (optional evaluator later), CUE v0.17.1 (optional schemas later) |
| JSON diff | wI2L/jsondiff v0.7.1 | RFC 6902 output | Hand-rolled |
| S3 (v0.3) | aws-sdk-go-v2/service/s3 v1.114.2 | Conditional writes, versioning | — |
| Consul (v0.3) | hashicorp/consul/api v1.34.5 | CAS index, blocking queries | — |
| etcd (v0.3) | go.etcd.io/etcd/client/v3 v3.7.2 | Revision transactions, watch | — |
| Azure Blob (later) | azure-sdk-for-go/sdk/storage/azblob v1.8.2 | Versioning, ETags | — |
| Metrics, traces | prometheus/client_golang v1.25.0, OpenTelemetry Go v1.47.0 | Standard; client_golang includes the Prometheus HTTP API client used for health | — |
| Stats (v0.4) | aclements/go-moremath `stats.MannWhitneyUTest` (pseudo-version v0.0.0-20241023…) | Reuse, not an in-house U test; check its license (BSD-style) first | Thresholds only; gonum v0.17.0 (no Mann–Whitney U) |
| Tests | go-cmp v0.7.0, pgregory.net/rapid v1.3.0, native fuzzing, go.uber.org/goleak v1.3.0, `testing/synctest`, `net/http/httptest` | Property, fuzz and leak coverage; fake time (synctest, GA since Go 1.25) and HTTP fault injection | testify (not needed) |
| Integration tests (v0.3) | testcontainers-go v0.44.0, gofakes3 v1.2.0 | Real stores in containers; a fake S3 in unit tests | — |
| S3-compatible test server | ADR-0006: a maintained server with versioning and conditional writes | MinIO CE has been retired upstream | Real AWS S3 in a nightly job |
| Kubernetes (later) | controller-runtime v0.25.2, client-go v0.37.1, kind | Operator and sidecar patterns | — |
| Quality | golangci-lint v2; govulncheck (golang.org/x/vuln v1.8.0) as a `go.mod` tool | Lint, format and vulnerability checks in `just check` | — |
| Release | release-please (Go release type), GoReleaser, cosign (keyless), slsa-github-generator, syft | Versions and changelog from Conventional Commits; signed, attested binaries and images | — |
| Docs | Markdown in `docs/`; site later (ADR) | — | — |

**Tooling notes:**
- `.golangci.yml` uses the v2 format: `version: "2"`, with gofumpt and goimports under `formatters:`. It doesn't enable the `comments` exclusion preset, so missing doc comments are reported, and it excludes `replay/.*/consumer/` from the rules the stubs break.
- govulncheck runs as a `go.mod` tool: `go get -tool golang.org/x/vuln/cmd/govulncheck`, then `go tool govulncheck ./...`.
- Events created with the repository's `GITHUB_TOKEN`, such as release-please's tags, don't start new workflow runs (`workflow_dispatch` and `repository_dispatch` aside). So GoReleaser runs in release-please's workflow, in a job gated on its `release_created` output. The tags aren't signed; the release artifacts are.
- Pin actions by full commit SHA from `gh api` (for example `gh api repos/actions/checkout/commits/v7.0.1 --jq .sha`), never from memory. The exception is slsa-github-generator: its builders must be referenced by a full version tag so slsa-verifier can check them (§10).

---

## 5. Phases and milestones

Estimates assume 8–10 hours a week. Each phase from Phase 1 on ends with a tagged release, the replay suite at that phase's expected outcomes (§3.10), and a short demo.

### Phase 0 — Foundations (2–3 weeks)

- [x] 0.1 Confirm the name: GitHub owner, Go module path, Homebrew tap and GHCR names, plus a quick trademark search. Write ADR-0001.
- [x] 0.2 Initialize the local repo (`git init`) and add:
  - `.gitattributes` first: `* text=auto eol=lf`, and `-text` for testdata and golden files;
  - `LICENSE` (Apache-2.0), a `README` stub, `CODE_OF_CONDUCT.md` (Contributor Covenant 2.1) and `SECURITY.md` (private vulnerability reporting);
  - `CONTRIBUTING.md` (DCO sign-off with `git commit -s`, Conventional Commits, dev setup);
  - issue and PR templates, and `CODEOWNERS`.
- [x] 0.3 Scaffold:
  - `go mod init` with `go 1.26.0`, and govulncheck as a `go.mod` tool (§4);
  - a `justfile` with `check` and `fuzz` (§6), plus `.golangci.yml` (§4), `.editorconfig` and `.gitignore`;
  - a `cmd/0cular` skeleton with `version`: `main` is `os.Exit(run(...))`, cobra has `SilenceErrors` and `SilenceUsage`, and tests check the exit codes (Appendix F) on a built binary.

  From this task on, `just check` must pass after every step.
- [x] 0.4 CI workflows:
  - unit tests on Linux, macOS and Windows with Go 1.26 and 1.27, and `-race` on Linux;
  - golangci-lint, govulncheck and a fuzz smoke test (`just fuzz`, 30 s per target), on Linux only;
  - zizmor, a DCO check on pull requests, Dependabot (`gomod`, `github-actions`) and Scorecard.

  Pin actions by full commit SHA from `gh api`, with least-privilege `permissions:`.
- [x] 0.5 Write ADRs:
  - 0002 language and stack;
  - 0003 contract language (CEL + JSON Schema), with the CEL limits and pinned extension versions (§3.2);
  - 0004 Store interface and versioning semantics: write-once blobs, a pointer with a generation, and the read-only `Source` (§3.5);
  - 0005 contract file format and `apiVersion` policy, with the decode model (§3.3);
  - 0010 license (Apache-2.0) and DCO, with the sign-off flow in §7.
- [x] 0.6 Write the three v0.1 replay scenarios (§3.10), `doubled-feature-file`, `blank-policy-fields` and `emptied-record`: data, READMEs, `expected.yaml` and consumer stubs, plus `replay/expected.schema.json`. Tests prove that each stub exits 0 on its baseline and fails on its candidate, before any 0cular logic exists.
- [x] 0.7 Design-partner call:
  - write `docs/design-partners.md` (one page: what you'd get, what we'd ask);
  - write a short post the maintainer can publish.

  Target: 1–2 teams that reload data live. Both stay drafts until the maintainer publishes them.
- [x] 0.8 Write `docs/architecture.md` from §3.1 and §3.5–3.6.
- [x] 0.9 First push: the maintainer makes the GitHub repo public and pushes the signed-off commits (§7). Then confirm CI is green.

**Acceptance criteria:**
- `just check` passes.
- After the first push, CI is green on Linux, macOS and Windows, on Go 1.26 and 1.27.
- Each of the three replay stubs exits 0 on its baseline and fails on its candidate, proven by tests.
- ADRs 0001–0005 and 0010 are accepted.
- The design-partner page and post are drafted, and nothing has been posted yet.

### Phase 1 — v0.1 "Check" (5–7 weeks)

**Goal:** block bad config before it ships.

- [ ] 1.1 `contract`:
  - types and strict YAML loading (unknown fields are errors);
  - a published JSON Schema for contract files;
  - `contract lint` with helpful messages and the lint rules in §3.2–3.3: CEL type errors at the expression, static cost, `onFailure` values, budget limits, int-literal arithmetic.
- [ ] 1.2 `eval`:
  - the decode model for JSON and YAML, with its caps (§3.3), and JSON Schema;
  - CEL invariants with cost limits, `ContextEval` timeouts, pinned extensions and `zerocular.unique`;
  - measure and collection budgets, and deterministic results.

  Fuzz the decoders and the evaluator; property-test the budget math.
- [ ] 1.3 `report`: text, Markdown and JSON (`schemaVersion: 1`) with evidence values, escaped as §3.12 requires. Add golden-file tests, including the hostile values.
- [ ] 1.4 CLI: `check` (with `--baseline` or `--no-baseline`), `diff`, `contract lint`, `contract schema`, `replay run` and `version`, with the exit codes in Appendix F.
- [ ] 1.5 `gate` library: `Check` with bound approvals (§3.4), plus examples.
- [ ] 1.6 GitHub Action (§3.11): the composite action in `action/`, the contract from the base ref, annotations and a Job Summary, and the optional two-workflow sticky comment. It builds from source until v0.1.0 exists.
- [ ] 1.7 Replay: the three scenarios BLOCK at `check`, with evidence that names the violated rule.
- [ ] 1.8 Docs:
  - README quickstart (under 10 minutes);
  - `docs/contract-spec.md`, with the decode model;
  - a gate and Action guide;
  - a cookbook of common invariants (non-null, caps, `zerocular.unique`, referential checks).
- [ ] 1.9 Release v0.1.0 through one release workflow: release-please runs first, and GoReleaser runs only when `release_created` is true (§4). It builds binaries (linux, darwin, windows × amd64, arm64), cosign keyless signatures, SLSA provenance, a syft SBOM and a GHCR image. Then switch the Action to the verified release binary. First decide how releases fit the branch flow (ADR-0011, §10).

**Acceptance criteria** (initial targets; calibrate in ADRs):
- **Replay:** 3 of 3 v0.1 scenarios BLOCK with evidence that names the violated rule.
- **Fuzzing:** nightly fuzzing (10 minutes per target) finds no crashes. The evaluator never panics or exceeds its time and memory caps on malformed input.
- **Performance:** a 5 MB JSON candidate with 10 invariants and 5 budgets evaluates in under 500 ms (p95) on a laptop.
- **Action:** on a fork PR, annotations, the Job Summary and the sticky comment appear. A PR that weakens its contract is still checked against the base ref's contract.
- **Escaping:** the hostile-value golden tests pass for every renderer.
- **Onboarding:** a new user gets from zero to a blocked bad config in under 10 minutes.

### Phase 2 — v0.2 "Keep" (5–7 weeks)

**Goal:** keep consumers on the last known good when something slips through, in any language.

- [ ] 2.1 Write ADR-0009 (re-validating a cached LKG when the contract changes, §3.6) before any loader code.
- [ ] 2.2 Pointer format (§3.5): `docs/pointer-format.md`, a JSON Schema, the read-only `Source` interface, and the `file` and `http` sources. Fuzz the pointer parser.
- [ ] 2.3 `loader` (§3.6):
  - Start with re-validation, both slots and the pending marker; the apply path with `Validate` and `OnChange`;
  - polling with jitter, and backoff capped at `pollEvery`;
  - retention, `Health()`, metrics, slog, and atomic writes with the build-tagged directory fsync.

  Add goleak, race and fuzz tests, and `testing/synctest` tests with `httptest` fault injection for outages, slow responses and backoff.
- [ ] 2.4 `agent` (§3.7): metrics and `/readyz`, signal support, a container image, and a guide with the Kubernetes rules.
- [ ] 2.5 Replay: each v0.1 candidate, delivered with the gate bypassed, is rejected by the loader, which keeps the LKG. Add `field-count-mismatch`, with its stub proof: its consumer crashes after the swap and restarts on `good`.
- [ ] 2.6 `examples/`: a Go HTTP service that uses the loader, a Python sample that reads the agent's file, and a Compose demo with an HTTP source and a minimal Grafana dashboard.
- [ ] 2.7 Loader and agent guides, plus release v0.2.0.

**Acceptance criteria:**
- **Replay:** every v0.2 outcome in §3.10 holds.
- **Outage:** under `testing/synctest`, with the HTTP source failing for 24 hours of fake time, the loader serves the LKG, reports `stale`, never waits longer than `pollEvery` between attempts, and recovers within one poll after the source returns.
- **Crash fallback:** a process killed after a swap, before `goodAfter`, restarts on `good`; a clean `Close()` doesn't cause a fallback.
- **Robustness:** the loader never panics, leaks goroutines or exceeds its memory cap on malformed input.
- **Any language:** the Python sample gets fail-stale behavior through the agent, and a new pointer reaches loaders and agents in the Compose demo within one poll interval.

### Phase 3 — v0.3 "Publish" (6–8 weeks)

**Goal:** a gated publish path into real stores, with consumer-in-the-loop tests and stale-writer protection.

- [ ] 3.1 Write ADR-0006 (S3-compatible test server). Then the `Store` adapters (§3.5): `file`, `s3` (S3-compatible), and one of `consul` or `etcd`, chosen with design partners. Each has a write-once `PutBlob`, `CompareAndSwapPointer`, metadata objects, a documented size limit and a `Source` view for loaders.
- [ ] 3.2 `store verify`, the conditional-write probe. `publish` refuses a store that fails it.
- [ ] 3.3 `publish` (gate against the parent → `PutBlob` → compare-and-swap that asserts the parent), `get`, `history` and `status`. Record approvals in metadata, with an audit view (`history --approvals`).
- [ ] 3.4 `revert` and the break-glass pin (§3.9).
- [ ] 3.5 `test-consumer` (§3.8): a container runner with the default sandbox, a host runner only with `sandbox: none`, `replay` JSONL, and timeouts.
- [ ] 3.6 Replay: add `killswitch-unhandled-rule`, with its stub proof. `field-count-mismatch` and `killswitch-unhandled-rule` BLOCK through consumer tests, and the stale plan in `emptied-record` fails its parent check.
- [ ] 3.7 Integration tests on Linux (`integration` tag): testcontainers for the S3-compatible server (ADR-0006), Consul and etcd; gofakes3 in unit tests.
- [ ] 3.8 Docs, plus release v0.3.0.

**Acceptance criteria:**
- All 5 replay scenarios BLOCK before publish.
- Concurrent publishers never clobber each other (CAS race test), and a publish built on a stale parent is refused.
- `store verify` fails against a fake backend that ignores conditional writes, and `publish` then refuses to run.
- `revert` reaches loaders within one poll interval, and a pin overrides the pointer.

### Phase 4 — v0.4 "Rings" (8–10 weeks)

**Goal:** health-mediated deployment for data, with automatic revert.

- [ ] 4.1 Write ADR-0007 (rollout manifest and ring hashing). Then the manifest as pointer format 2 (§3.9), and `rollout start`, `status`, `step`, `pause` and `resume` through compare-and-swap.
- [ ] 4.2 Ring resolver in the loader and agent: SHA-256 buckets, explicit instance lists, `minInstances`, and the rejected-version list.
- [ ] 4.3 Write ADR-0008 (health evaluation). Then the health providers:
  - the Prometheus HTTP API with per-instance query templates and the configurable version join (§3.6);
  - liveness and adoption checks, threshold rules and `slo` limits;
  - Mann–Whitney U through go-moremath once its license checks out, or thresholds only.
- [ ] 4.4 Promoter (`promoter run` and a one-shot CI-step mode): the loop in §3.9, auto-revert, and a dry-run mode that logs decisions without acting.
- [ ] 4.5 `revert` for rings (every ring → fleet LKG, paused), plus telemetry helpers: info-metric registration and OTel resource attributes.
- [ ] 4.6 Demo with 10 consumer replicas, Prometheus and Grafana:
  - a valid-but-harmful config (passes the gate, raises 5xx at runtime) is caught in the canary ring and reverted;
  - a canary that crashes and stops reporting is reverted, not paused;
  - `killswitch-unhandled-rule` is shown at runtime through a test-only `--skip-gate` flag.
- [ ] 4.7 Fault tests:
  - store down mid-rollout;
  - Prometheus down (the promoter pauses);
  - a silent candidate ring (the promoter reverts);
  - clock skew;
  - a manifest edited concurrently.
- [ ] 4.8 Docs, plus release v0.4.0.

**Acceptance criteria:**
- In the demo, a runtime-only failure reaches only the canary ring (with 10 replicas, `minInstances: 1` guarantees one canary). It's reverted within the bake time plus one poll interval.
- A candidate ring that stops reporting while the control reports is reverted.
- The promoter never promotes on missing data, and pauses only when both sides are missing or Prometheus is unreachable.
- Revert works with the promoter stopped.
- The ring-share property test passes (§6).

### Later — Meet teams where they are

- Native Python and TypeScript loaders that pass a shared **conformance suite** (contract + inputs + expected results in JSON) also run by the Go implementation.
- More formats: TOML, CSV and `bytes`, under the decode model (§3.3).
- Integrations: Azure Blob and GCS adapters, and a Kubernetes recipe. A small controller only if partners need it.
- **AI-config pack:**
  - contract templates for prompts, model IDs, agent tool manifests and guardrail rules;
  - an `eval` health provider that runs an evaluation command and reads its pass rate.
- Optional evaluators: OPA/Rego and CUE schemas. Artifact signatures (Sigstore).

### Toward 1.0

- Contract spec `v1` with a compatibility policy.
- A conformance suite published for third-party loaders.
- A docs site, a governance doc, and a co-maintainer.

---

## 6. Testing and evaluation

| Layer | What | Tooling | Runs |
| --- | --- | --- | --- |
| Unit | contract, eval, budgets, report (with hostile-value goldens), pointer, ring hashing, health decisions | `testing`, go-cmp, rapid | Every push; Linux, macOS and Windows |
| Fuzz | Decoders (json, yaml), contract loader, evaluator, pointer parser | Native Go fuzzing through `just fuzz` | 30 s per target per push; 10 min nightly; Linux |
| Leak and race | Loader, agent, promoter | goleak, `-race` | Every push (race on Linux) |
| Fake time and faults | Loader backoff and outages, bake timing | `testing/synctest`, `net/http/httptest` | Every push |
| Exit codes | CLI commands run as a built binary | `testing`, `os/exec` | Every push |
| Integration | Store adapters, `store verify`, publish → loader end to end | testcontainers-go, gofakes3 | Nightly and before release; Linux only |
| Replay | The postmortem-shaped scenarios | `go test ./replay/...` | Every push |
| Demo | Compose environment with rings and Prometheus | Scripted | Before release |

**Fuzz targets.** `go test -fuzz` must match exactly one target, so `just fuzz` lists each package's targets (`go test -list '^Fuzz'`) and runs each as `go test -run '^$' -fuzz "^$name$" -fuzztime "$FUZZTIME"` (30 s by default; 10 minutes nightly).

**Property tests to have:**
- Budget math is symmetric where it should be, and `maxChangePct` is monotone.
- Ring assignment is stable for a given instance. For generated fleets of N = 2,000,000 random instance IDs and the rings in Appendix A (1%, 9% and 90% of buckets), each ring's share is within ±0.1 percentage points of its target. That's at least 4.7 standard deviations for every ring, so the test isn't flaky. It hashes 2 million IDs, which is slow under `-race`, so it runs in the nightly job.
- Loader state machine: the served config always passed its checks (or is the `open` default, or a trusted LKG under `lkg_invalid`); a pending marker at Start always leads to `good`; retention never removes `good`.
- Report ordering is deterministic.

**Metrics and initial targets:**

| Capability | Metric | Initial target | Phase |
| --- | --- | --- | --- |
| Gate | Replay scenarios blocked before publish | 3/5 (v0.1) → 5/5 (v0.3) | 1–3 |
| Loader | Crashes, leaks or LKG violations under fuzz and fault tests | 0 | 2 |
| Performance | Evaluation of a 5 MB candidate (10 invariants, 5 budgets) | < 500 ms p95 | 1 |
| Rollout | Share of instances exposed to a runtime-only bad config | ≤ canary ring | 4 |
| Rollout | Time to revert after detection | ≤ one poll interval | 4 |
| Usability | Time to first blocked bad config | < 10 min | 1 |

---

## 7. Release and open-source operations

- **Versioning:** SemVer, with 0.x until contract spec v1. Contract `apiVersion` changes follow a written deprecation policy.
- **Releases:** release-please (Go release type), driven by Conventional Commits, and GoReleaser run in one workflow; the GoReleaser job runs only when `release_created` is true (§4). It produces binaries, a GHCR image, checksums, cosign keyless signatures, SLSA provenance and a syft SBOM. The Homebrew tap is optional.
- **Sign-off (DCO, ADR-0010):** every commit is signed off with `git commit -s`, and a DCO check runs in CI on every pull request.
- **Branches and environments (ADR-0011):** `main` (production), `uat`, `qa` and `dev` are permanent. Work branches off `dev` and is promoted `dev` → `qa` → `uat` → `main`; a `hotfix/*` branch goes into `uat`. The `branch-flow` check enforces the order, and rulesets keep the four branches from being deleted or force-pushed.
- **Repo hygiene:** branch protection with required checks, Dependabot and a Scorecard badge.
- **Docs:**
  - README: pitch, the replay-suite story, a 10-minute quickstart;
  - `docs/contract-spec.md`, guides, ADRs;
  - an invariant cookbook.
- **Community:** Discussions; labels `good first issue`, `adapter`, `invariant-cookbook`, `help wanted`; a GitHub Project mirroring the phases.
- **Contributor on-ramps:** store adapters, cookbook entries, report formats, replay scenarios (with sources), and loader ports to other languages (later).
- **Communication:**
  - a launch post centered on the replay suite;
  - a talk proposal: "Replaying five config outages";
  - case studies with design partners (with consent).
- **Sustainability:** design partners first, Sponsors later. A hosted promoter or control plane is a possible commercial path after 1.0; decide only with real demand.

---

## 8. Risk register

L = likelihood, I = impact (H/M/L).

| ID | Risk | L | I | Early warning | Mitigation |
| --- | --- | --- | --- | --- | --- |
| R1 | No design partner; can't dogfood on a real fleet | H | H | No response to the Phase 0 call by the end of Phase 1 | Targeted outreach; offer hands-on help; replay suite and demo as proof; start with the CI-side Action (v0.1), which needs no fleet |
| R2 | Every team's publish path differs | H | M | Requests for many stores | Narrow `Source` and `Store` interfaces; agent mode; adapters by demand; contributor-owned adapters |
| R3 | Trust barrier for a runtime component in the production path | H | H | Teams try only the CI gate | Gate first (v0.1); small, fuzzed loader; fail-stale defaults; signed releases; SBOM; SECURITY.md |
| R4 | A 0cular bug causes an outage | M | H | Fuzz or fault-test findings; issue reports | Race, leak, fuzz and fault-injection tests; recover at callbacks; crash fallback to `good`; conservative defaults; pin to bypass; roll out 0cular's own releases in rings |
| R5 | Counterfactual value is hard to show | H | M | "Nice, but has it ever caught anything?" | Replay suite; count blocked publishes and keep their evidence; demo |
| R6 | The biggest users build in-house | H | L | — | Target mid-size teams; be the reference implementation; write about the pattern |
| R7 | Bad or missing health signals drive wrong promotions | M | H | Flapping promotions in the demo | A silent candidate ring fails; pause only when both sides are missing or Prometheus is down; adoption-based bake; dry-run mode; per-instance queries; Mann–Whitney only with enough instances |
| R8 | Semantics drift across language ports | M | M | Conformance failures | Agent mode first; shared conformance suite for every port |
| R9 | Scope creep into a feature-flag platform | M | M | Requests for per-user targeting | Non-goal stated in the README; point to OpenFeature integrations |
| R10 | Go learning curve for the maintainer | M | M | Slow code reviews | Small packages; strict linting; examples; lean on tests |
| R11 | CEL is unfamiliar to users | M | M | Contract-lint confusion in issues | Cookbook; helpful lint messages; templates |
| R12 | Supply-chain risk passed to users | L | H | govulncheck findings | Minimal dependencies; signed, attested releases; Dependabot |
| R13 | Solo burnout or bus factor | H | H | Missed estimates by > 50% | Strict scope; automation; co-maintainer by v0.3 |

---

## 9. Rewards and success metrics

**Why it's worth doing:**
- Config and data changes started five of the largest outages since mid-2024 (§1.1). Teams that reload data live get a pre-publish check, fail-stale loading and staged rollout for that class of change.
- No open, store-agnostic equivalent exists.
- The replay suite gives users concrete, checkable evidence of which control catches which failure shape.
- AI configuration (prompts, model IDs, tool manifests) is live-reloaded config too, so the same safety net applies.

| Goal | Measure | Target | When |
| --- | --- | --- | --- |
| Validation in the field | Design partners using 0cular | 1 | End of Phase 1 |
| Validation in the field | Design partners using 0cular | 2 | By v0.3 |
| Proof | Replay scenarios caught before publish | 5/5 | v0.3 |
| Proof | Runtime catch shown in a ring | Demo + write-up | v0.4 |
| Adoption | Repos or orgs using the Action or importing the module (pkg.go.dev "Imported by", code search) | 10 | By v0.3 |
| Quality | Known incidents caused by 0cular | 0 | Always |
| Visibility | Talk accepted | 1 | By v0.3 |
| Community | Co-maintainer | 1 | By v0.3 |

---

## 10. Decisions and open questions

ADRs 0001–0010 are reserved for the decisions below, and each is written in the task listed. They stay Proposed until the maintainer accepts them. New, unplanned ADRs start at 0011.

| ADR | Decision | Written in |
| --- | --- | --- |
| 0001 | Name, GitHub owner, module path | Task 0.1 |
| 0002 | Language and stack (Go) | Task 0.5 |
| 0003 | Contract language: CEL + JSON Schema (Rego and CUE optional later) | Task 0.5 |
| 0004 | Store interface and versioning semantics | Task 0.5 |
| 0005 | Contract file format and `apiVersion` policy | Task 0.5 |
| 0006 | S3-compatible test server | Task 3.1 |
| 0007 | Rollout manifest and ring hashing | Task 4.1 |
| 0008 | Health evaluation (thresholds + Mann–Whitney U) | Task 4.3 |
| 0009 | Re-validating a cached LKG when the contract changes | Task 2.1 |
| 0010 | License (Apache-2.0) and DCO | Task 0.5 |

**Open questions:**
- **Second v0.3 adapter.** Consul or etcd? Let design partners decide.
- **Marketplace listing.** The Action stays unlisted in `action/` for v0.1. Should a later release add a root `action.yml`, or a separate action repo, to get a listing?
- **SLSA provenance.** Keep slsa-github-generator as the one tag-pinned exception (§4), or produce provenance with a tool that can be pinned by SHA? Decide in task 1.9.
- **Releases and the branch flow.** release-please opens its release pull request against `main`, which accepts only `uat` (ADR-0011). Run release-please against `uat`, or tag releases on `main` after a promotion? Decide in task 1.9.
- **Git as a store.** Is a git store worth supporting, given GitOps users already have the Action?
- **Docs site tooling**, before v0.4.

---

## 11. Timeline (part-time, 8–10 h/week)

| Phase | Duration | Cumulative |
| --- | --- | --- |
| 0 Foundations | 2–3 weeks | ~3 weeks |
| 1 v0.1 Check | 5–7 weeks | ~2.5 months |
| 2 v0.2 Keep | 5–7 weeks | ~4 months |
| 3 v0.3 Publish | 6–8 weeks | ~6 months |
| 4 v0.4 Rings | 8–10 weeks | ~8 months |

If a phase runs more than 50% over, cut scope rather than extend: move tasks to the next phase and note it on the status board.

---

## 12. Appendices

### Appendix A — Full contract example

```yaml
apiVersion: 0cular/v1alpha1
kind: Contract
metadata:
  name: rate-limit-policies
artifact:
  format: json
  maxBytes: 2MiB
schema:
  jsonSchema: schemas/rate-limit-policies.schema.json
invariants:
  - name: every-policy-has-a-limit
    expr: self.policies.all(p, has(p.limit) && p.limit != null && p.limit > 0)
    message: consumers dereference limit without a null check
  - name: unique-ids
    expr: zerocular.unique(self.policies.map(p, p.id))
    message: duplicate policy ids are rejected by the consumer
diffBudgets:
  - name: policy-count-stable
    measure: size(self.policies)
    maxChangePct: 20
  - name: policy-churn
    collection: self.policies
    key: item.id
    maxRemoved: 5
    maxChangedPct: 10
consumerTests:
  - name: gateway-accepts-candidate
    container: ghcr.io/example/gateway:1.42
    run: ["gateway", "--validate-config", "{{.CandidatePath}}"]
    timeout: 60s
rollout:
  rings:
    - { name: canary, upTo: 100, minInstances: 1 }
    - { name: early,  upTo: 1000 }
    - { name: all,    upTo: 10000 }
  bake: 10m
  health:
    provider: prometheus
    url: http://prometheus:9090
    joinLabels: [namespace, pod]
    window: 5m
    interval: 1m
    queries:
      - name: error-rate
        perInstance: |
          sum by (namespace, pod) (rate(http_requests_total{code=~"5.."}[{{.Window}}]))
            / sum by (namespace, pod) (rate(http_requests_total[{{.Window}}]))
        maxAbsDegradation: 0.002
        minEffect: 0.001
        slo: 0.01
onFailure:
  noLastKnownGood: closed
```

`zerocular.unique` runs in linear time. The filter-based form, `self.policies.all(p, self.policies.filter(q, q.id == p.id).size() == 1)`, is O(n²) and can hit the cost limit on large lists; the cookbook shows it only as the example of why `zerocular.unique` exists.

### Appendix B — Check report excerpt (illustrative)

```text
0cular check  bot-features  candidate sha256:bb12…  baseline sha256:aa34…
BLOCK
  ✗ invariant  feature-cap            size(self.features) = 240; limit 200 — the proxy preallocates 200 features
  ! budget     feature-count-stable   120 → 240 (+100%); budget ±20%   (needs approval)
  ✓ schema     bot-features.schema.json
  ✓ envelope   1.8 MiB ≤ 5 MiB; valid JSON
```

### Appendix C — Workflows for repo-stored config

The check needs only read access. The optional sticky comment comes from a second workflow, because a fork PR's `GITHUB_TOKEN` is read-only.

```yaml
# .github/workflows/config-check.yml
name: config-check
on: pull_request
permissions:
  contents: read
jobs:
  check:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@<full-commit-sha>      # look up with gh api
        with: { fetch-depth: 0 }                      # the action reads the contract from the base ref
      - uses: itillema/0cular/action@<full-commit-sha>
        with:
          contract: contracts/bot-features.yaml
          candidate: config/bot-features.json
          comment: true                               # uploads the report as an artifact
```

```yaml
# .github/workflows/config-comment.yml (never checks out or runs PR code)
name: config-comment
on:
  workflow_run: { workflows: [config-check], types: [completed] }
permissions:
  actions: read
  pull-requests: write
jobs:
  comment:
    runs-on: ubuntu-latest
    steps:
      - uses: itillema/0cular/action@<full-commit-sha>
        with: { mode: comment }                       # downloads the artifact; posts or updates the comment
```

### Appendix D — Loader configuration (YAML form, for the agent)

```yaml
source: https://config.internal/bot-features/   # file path or URL; store URLs from v0.3
contract: contracts/bot-features.yaml
cacheDir: /var/cache/0cular
pollEvery: 30s          # also the cap on retry backoff
fetchTimeout: 5s
goodAfter: 10m          # time in the applied slot before a version becomes good
instanceID: ${POD_NAME}
out: /shared/config/bot-features.json   # inside a shared directory mount (§3.7)
signal: { name: HUP, pidFile: /shared/run/app.pid }
metrics: :9464
```

### Appendix E — Compose demo outline (v0.4)

```text
services: s3-compatible store · prometheus · grafana · consumer ×10 (Go sample using the loader) ·
          promoter · publisher (scripted: good change → bad-but-valid change → revert)
```

### Appendix F — Exit codes

| Code | Meaning |
| --- | --- |
| 0 | PASS |
| 1 | BLOCK, including a publish refused for a stale parent |
| 3 | NEEDS_APPROVAL |
| 64 | Usage or configuration error |
| 70 | Internal error |

Code 2 is left to the Go runtime, which uses it for an unrecovered panic. 64 and 70 follow `sysexits.h` (`EX_USAGE`, `EX_SOFTWARE`); the header is marked deprecated, but its numbers remain the common convention. Commands without a verdict use 0 for success and 1 for a failed check, such as `contract lint` errors or a failed `store verify`.

---

## 13. Sources

- Cloudflare, [Cloudflare outage on December 5, 2025](https://blog.cloudflare.com/5-december-2025-outage/)
- Cloudflare, [Cloudflare outage on November 18, 2025](https://blog.cloudflare.com/18-november-2025-outage/)
- Cloudflare, [Code Orange: Fail Small — our resilience plan](https://blog.cloudflare.com/fail-small-resilience-plan/) and [Code Orange: Fail Small is complete](https://blog.cloudflare.com/code-orange-fail-small-complete/)
- AWS, [Summary of the Amazon DynamoDB service disruption in us-east-1](https://aws.amazon.com/message/101925)
- Google Cloud, [Service Control incident report, June 12, 2025](https://status.cloud.google.com/incidents/ow5i3PPK96RduMcb1SsW)
- CrowdStrike, [Channel File 291 Incident Root Cause Analysis](https://www.crowdstrike.com/wp-content/uploads/2024/08/Channel-File-291-Incident-Root-Cause-Analysis-08.06.2024.pdf) (21 defined fields, 20 supplied) and its [executive summary](https://crowdstrike.com/wp-content/uploads/2024/08/Executive-Summary_Root-Cause-Analysis_Channel-File-291.pdf); Microsoft, [Helping our customers through the CrowdStrike outage](https://blogs.microsoft.com/blog/2024/07/20/helping-our-customers-through-the-crowdstrike-outage/) (8.5 million devices)
- Sun et al., [Testing Configuration Changes in Context to Prevent Production Failures (ctest)](https://www.usenix.org/conference/osdi20/presentation/sun), OSDI 2020; [openctest](https://github.com/xlab-uiuc/openctest); [IDoCT](https://github.com/xlab-uiuc/IDoCT)
- Tang et al., [Holistic Configuration Management at Facebook](https://doi.org/10.1145/2815400.2815401) (Configerator), SOSP 2015
- Spinnaker, [Kayenta](https://github.com/spinnaker/kayenta) and its [canary judge](https://spinnaker.io/docs/guides/user/canary/judge/) (Mann–Whitney U)
- [Flagger](https://github.com/fluxcd/flagger) (ConfigMap and Secret tracking); [Kargo](https://github.com/akuity/kargo)
- [minio/minio](https://github.com/minio/minio) (repository archived on 2026-04-25); [MinIO CE in 2026: retired upstream, source-only](https://glukhov.org/data-infrastructure/object-storage/minio-dead/)
- GitHub Docs: [Triggering a workflow](https://docs.github.com/en/actions/how-tos/write-workflows/choose-when-workflows-run/trigger-a-workflow); [Events that trigger workflows](https://docs.github.com/en/actions/reference/workflows-and-actions/events-that-trigger-workflows); [Workflow commands](https://docs.github.com/en/actions/reference/workflows-and-actions/workflow-commands); [Publishing actions in GitHub Marketplace](https://docs.github.com/en/actions/how-tos/create-and-publish-actions/publish-in-github-marketplace); [Dependabot options](https://docs.github.com/en/code-security/reference/supply-chain-security/dependabot-options-reference)
- [actions/toolkit command.ts](https://github.com/actions/toolkit/blob/main/packages/core/src/command.ts) (escaping); [release-please-action](https://github.com/googleapis/release-please-action); [slsa-github-generator](https://github.com/slsa-framework/slsa-github-generator)
- Go: [Go 1.25 release notes](https://go.dev/doc/go1.25) (synctest); [Go 1.24 release notes](https://go.dev/doc/go1.24) (tool directives); [cel-go](https://pkg.go.dev/github.com/google/cel-go/cel); [go-moremath stats](https://pkg.go.dev/github.com/aclements/go-moremath/stats); [go.yaml.in/yaml/v3](https://github.com/yaml/go-yaml); [golangci-lint configuration](https://golangci-lint.run/docs/configuration/file/)
- Kubernetes: [Sidecar containers](https://kubernetes.io/docs/concepts/workloads/pods/sidecar-containers/); [ConfigMaps](https://kubernetes.io/docs/concepts/configuration/configmap/) (`subPath` mounts get no updates); [Share process namespace](https://kubernetes.io/docs/tasks/configure-pod-container/share-process-namespace/)
- Stores: [S3 conditional writes](https://docs.aws.amazon.com/AmazonS3/latest/userguide/conditional-writes.html); [S3 uploads](https://docs.aws.amazon.com/AmazonS3/latest/userguide/upload-objects.html) (5 GB per PUT); [Consul KV](https://developer.hashicorp.com/consul/docs/v1.19.x/dynamic-app-config/kv) (512 KB); [etcd limits](https://etcd.io/docs/v3.5/dev-guide/limit/) (1.5 MiB)
- [sysexits(3)](https://man.freebsd.org/cgi/man.cgi?query=sysexits)
