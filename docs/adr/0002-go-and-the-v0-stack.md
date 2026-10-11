# ADR-0002: Go and the v0 stack

- **Status:** Accepted
- **Date:** 2026-10-10

## Context

0cular ships three kinds of software: a CLI and a GitHub Action that check changes, a loader library that services embed, and a sidecar agent. The loader runs inside other teams' production processes, so it must be small, fast to start, easy to embed and hard to crash. The tools that 0cular sits beside (Kubernetes, Prometheus, etcd, Consul, the major cloud SDKs) are written in Go, and so is the reference implementation of CEL, the expression language chosen in ADR-0003.

Every dependency is also code that 0cular's users run, so the stack should stay small, maintained and auditable.

## Decision

Write 0cular in **Go**. `go.mod` declares `go 1.26.0`, and CI tests the two supported Go releases (1.26 and 1.27) on Linux, macOS and Windows. The minimum moves up when Go drops support for a release.

The stack, with the versions current on 2026-10-10:

| Area | Choice | Version | Notes |
| --- | --- | --- | --- |
| CLI | spf13/cobra | v1.10.2 | |
| Task runner | just | 1.58.0 | Pinned in CI |
| JSON | encoding/json | Go stdlib | Duplicate keys are handled by the decode model (ADR-0005) |
| YAML | go.yaml.in/yaml/v3 | v3.0.5 | See below |
| JSON Schema | santhosh-tekuri/jsonschema/v6 | v6.0.3 | Draft 2020-12 |
| Expressions | google/cel-go | v0.30.0 | See below |
| JSON diff | wI2L/jsondiff | v0.7.1 | RFC 6902 |
| Metrics and traces | prometheus/client_golang, OpenTelemetry Go | v1.25.0, v1.47.0 | |
| Statistics (v0.4) | aclements/go-moremath | v0.0.0-20241023150245-c8bbc672ef66 | BSD-3-Clause; untagged |
| Tests | go-cmp, pgregory.net/rapid, go.uber.org/goleak | v0.7.0, v1.3.0, v1.3.0 | Plus `testing/synctest` and `net/http/httptest` |
| Integration tests (v0.3) | testcontainers-go, gofakes3 | v0.44.0, v1.2.0 | |
| Stores (v0.3) | aws-sdk-go-v2/service/s3, hashicorp/consul/api, go.etcd.io/etcd/client/v3 | v1.114.3, v1.34.5, v3.7.2 | |
| Lint and format | golangci-lint v2 | v2.14.0 | gofumpt and goimports as formatters |
| Vulnerabilities | govulncheck (golang.org/x/vuln) | v1.8.0 | A `tool` in `go.mod` |
| Release (v0.1) | release-please, GoReleaser v2, cosign, syft | | Provenance tooling is decided in task 1.9 |

Rules for the stack:

1. **A dependency that isn't in this table is a stack change.** It needs the maintainer's approval and an ADR.
2. **The table records the starting point, not the current versions.** Dependabot proposes upgrades with a seven-day cooldown, and `go.mod` is the source of truth.
3. **YAML stays on go.yaml.in/yaml/v3 for now.** The YAML organization calls v1–v3 "frozen legacy" with security fixes only, and recommends v4 for new projects. But v4 has no stable release yet (the newest is v4.0.0-rc.6). Move to v4 once v4.0.0 is tagged; that move is a stack change with its own ADR.
4. **cel-go is pinned to v0.30.0, its latest release.** The Go module proxy also lists v0.31.0 and v0.32.0, but their tags were deleted upstream and they were never published as releases. Don't use them.
5. **govulncheck runs as a `go.mod` tool** (`go tool govulncheck ./...`). Module pruning keeps tool-only requirements from becoming requirements of modules that import 0cular. x/vuln v1.8.0 itself requires `go 1.26.0`, so it doesn't raise the `go` line.
6. **go-moremath's BSD-3-Clause license** requires binary distributions to reproduce its copyright notice. Release builds ship the licenses of the dependencies they include (task 1.9).

## Consequences

- One language covers the CLI, the library, the agent and the Action, and produces static binaries for every platform.
- The loader can be embedded in Go services directly. Other languages use the agent until native loaders exist; those must pass a shared conformance suite.
- Pinning the Go minimum to the oldest supported release keeps the library usable by most Go services, at the cost of waiting a release for new language and library features. For example, `encoding/json/v2` arrived in Go 1.27, so it can't be used yet.

## Alternatives considered

- **Rust:** strong safety guarantees, but a steeper learning curve, a smaller infra-config ecosystem, and Cargo can't register a crate whose name starts with a digit.
- **Python:** fast to write, but a runtime in every sidecar, and weak isolation for a library inside the host process.
- **TypeScript/Node.js:** the same runtime cost, and a smaller infrastructure ecosystem.

## References

- [Go release policy](https://go.dev/doc/devel/release#policy)
- [go-yaml project status](https://github.com/yaml/go-yaml#project-status)
- [Tool dependencies](https://go.dev/doc/modules/managing-dependencies#tools)
- [go-moremath license](https://github.com/aclements/go-moremath/blob/master/LICENSE)
