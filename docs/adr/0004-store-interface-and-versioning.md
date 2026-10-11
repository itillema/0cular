# ADR-0004: Store interface and versioning semantics

- **Status:** Accepted
- **Date:** 2026-10-10

## Context

Teams already keep live-reloaded data somewhere: files, S3, Consul, etcd. 0cular has to work with those stores rather than replace them. Three outages shaped what the store layer must guarantee:

- **Stale writers.** In the DynamoDB DNS outage (October 2025), a delayed writer applied an old plan over a newer one, and cleanup then deleted it. A publish must notice that the store moved on since it read it.
- **No rollback target.** Reverting needs the previous version to still exist, unchanged.
- **Loaders in the blast radius.** The loader runs inside the consumer, so it must not carry store SDKs or write access.

## Decision

1. **Versions are content-addressed.** A version is `sha256:<hex>` of the artifact's bytes. The loader verifies the digest on every load and never applies a version whose bytes don't match.
2. **Each artifact has write-once blobs and one mutable pointer:**
   - **Blob:** the artifact bytes, plus a write-once metadata object: version, parent, contract hash, gate result digest, approvals, publisher and time. Neither is ever overwritten.
   - **Pointer:** a small JSON object with `formatVersion`, `artifact`, `generation` (incremented on every change, for compare-and-swap), `version`, `parent`, `updatedAt` and `updatedBy`. In v0.4, `formatVersion: 2` adds the rollout manifest. A loader that doesn't know a pointer's `formatVersion` keeps its last known good, so old loaders can't bypass rings.
3. **Interfaces.** Loaders and agents depend only on the read-only `Source`; publishers and the promoter use `Store`:

   ```go
   type Source interface {
       Pointer(ctx context.Context, artifact string) (Pointer, error)
       Blob(ctx context.Context, artifact string, v Version) ([]byte, error)
   }

   type Store interface {
       Source
       PutBlob(ctx context.Context, artifact string, data []byte, meta Meta) (Version, error) // write-once
       CompareAndSwapPointer(ctx context.Context, artifact string, next Pointer, expectedGeneration uint64) error
       History(ctx context.Context, artifact string, limit int) ([]Meta, error)
   }
   ```

   An optional `Watcher` interface adds change notification where the backend has it.
4. **Publish asserts its parent.** `publish` reads the pointer (generation G, version P), gates the candidate against P, writes the blob, and swaps the pointer only if it's still at G. If the pointer moved, the publish fails with exit code 1, and it doesn't retry. Automation that builds a candidate from a known version passes `--parent <version>`, and the publish fails if the pointer has moved past it.
5. **Conditional writes are verified, not assumed.** `store verify` probes the backend: a write with a wrong ETag, index or revision must be refused, and so must a repeated write-once write. `publish` runs the probe once per process and refuses a store that fails it.
6. **Adapters** map the operations onto each backend's conditional writes:

   | Adapter | Write-once blob | Pointer compare-and-swap | Change notification | Size limit |
   | --- | --- | --- | --- | --- |
   | `file` | Content-addressed files | Atomic rename plus a lock file | Polling | Disk |
   | `s3` | `If-None-Match: *` | `If-Match` with the pointer's ETag | Polling | 5 GB per PUT |
   | `consul` | `?cas=0` | `?cas=<ModifyIndex>` | Blocking queries | 512 KB per value |
   | `etcd` | Transaction on `create_revision = 0` | Transaction comparing `mod_revision` | Watch | 1.5 MiB per request |

   Adapters refuse blobs over their limit, so contracts should keep `artifact.maxBytes` below it.

## Consequences

- A stale writer fails loudly instead of silently replacing newer data, which is the DynamoDB failure shape.
- Every previous version stays available and verifiable, so revert is a pointer change, and a loader can always re-verify what it caches.
- Least privilege follows from the split. Loaders only read; publishers write blobs and the pointer; the promoter writes only the pointer.
- Any backend with conditional writes can be supported. Backends without them are refused by `store verify` rather than trusted.
- A publish writes two objects and swaps a third. Changes across several artifacts aren't transactional.

## Alternatives considered

- **Sequential version numbers or timestamps:** they say nothing about content, and two writers can produce the same number.
- **One mutable object per artifact:** no history to revert to, and no way to assert the parent.
- **The backend's own versioning, such as S3 object versions:** it isn't uniform across backends, and it doesn't give a compare-and-swap on "the current version".
- **Git as the store:** whether it's worth adding for teams that already use the GitHub Action is an open question in the build plan.

## References

- [S3 conditional writes](https://docs.aws.amazon.com/AmazonS3/latest/userguide/conditional-writes.html)
- [Consul KV](https://developer.hashicorp.com/consul/docs/v1.19.x/dynamic-app-config/kv)
- [etcd API guarantees](https://etcd.io/docs/v3.5/learning/api_guarantees/)
- [Summary of the Amazon DynamoDB service disruption in us-east-1](https://aws.amazon.com/message/101925)
