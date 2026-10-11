# ADR-0001: Name, owner and module path

- **Status:** Accepted
- **Date:** 2026-10-10

## Context

The project needs a name, a GitHub owner and a Go module path before any code exists. The module path appears in every import, so it can't change cheaply later, and moving the repository to another owner changes it too.

The chosen name, **0cular**, starts with the digit zero. Some identifiers allow that; others forbid it or mangle it:

| Surface | Leading digit | What happens to `0cular` |
| --- | --- | --- |
| GitHub user, organization and repository names | Allowed | — |
| Go module path elements | Allowed | — |
| CLI binary name | Allowed | — |
| OCI image names (GHCR) | Allowed, lowercase only | — |
| Homebrew cask tokens | Allowed | — |
| YAML strings such as `apiVersion: 0cular/v1alpha1` | Allowed | — |
| Prometheus metric names | Outside the classic rule `[a-zA-Z_:][a-zA-Z0-9_:]*` | PromQL can't parse `0cular_config_info{…}`. Prometheus 3 needs `{"0cular_config_info", …}`, and scrapers that don't negotiate UTF-8 see `_cular_config_info` |
| OpenTelemetry instrument names | Must start with a letter | The Go SDK reports an error, and the Prometheus translation renames the metric `_0cular_…` |
| Environment variable names | Not portable (POSIX) | bash rejects `export 0CULAR_PIN_X=lkg`. systemd's `Environment=` drops the variable with a warning, so a break-glass pin would silently do nothing. Kubernetes rejected such names before 1.32 |
| CEL identifiers | Must start with a letter or `_` | `0cular.unique(…)` doesn't parse |
| Go package names | Must be identifiers | `package 0cular` doesn't compile |
| Homebrew formula names | Become Ruby class names | `class 0cular < Formula` can't load; GoReleaser's deprecated `brews` would generate it |
| Cargo crate names | Rejected | — |

The letter-o spelling, "ocular", is a common word and is taken almost everywhere: the GitHub organization `ocular`, npm, PyPI, crates.io, Docker Hub and every `ocular.*` domain checked. One of these is crashappsec/ocular, an active Go and Kubernetes security-scanning tool aimed at a similar audience. Search engines correct "0cular" to "ocular".

A quick trademark search (not legal advice) found three live USPTO OCULAR word marks, all in Class 9. The closest is Reg. 5148860, held by Teledyne Digital Imaging for scientific-camera software. The search found no 0CULAR mark and nothing in Class 42; EUIPO and WIPO weren't checked.

## Decision

1. **Name:** 0cular, pronounced "zero-cular", wherever a leading digit is allowed:

   | Surface | Value |
   | --- | --- |
   | Repository | `github.com/itillema/0cular` |
   | Go module path | `github.com/itillema/0cular` |
   | CLI binary | `0cular`, built from `cmd/0cular` |
   | Contract `apiVersion` | `0cular/v1alpha1` |
   | Container image | `ghcr.io/itillema/0cular` |
   | GitHub Action | `itillema/0cular/action` |
   | Homebrew | Cask `0cular` in `itillema/homebrew-tap`, published with GoReleaser's `homebrew_casks`, if a tap is added |
   | Domain | `0cular.com`, held by the maintainer |
   | Default cache directory | `/var/cache/0cular` |

2. **Owner:** the maintainer's personal GitHub account, `itillema`.
3. **Letter-first form:** `zerocular`, wherever a leading digit is illegal:

   | Surface | Value |
   | --- | --- |
   | Prometheus metrics | `zerocular_config_info`, `zerocular_loads_total`, and so on |
   | OpenTelemetry meter and instruments | `zerocular`, `zerocular.…` |
   | Environment variables | `ZEROCULAR_PIN_<NAME>` and any later `ZEROCULAR_*` |
   | CEL function namespace | `zerocular.unique(list)`, which replaces `kg.unique` |
   | Go package named after the project | `package zerocular`, if one is ever needed |
   | Homebrew formula | `zerocular` with the alias `0cular`, if a formula is ever needed |

4. **Guard against the digit spelling.** An operator under pressure may type the brand: `0CULAR_PIN_BOT_FEATURES`. Docker, Kubernetes 1.32+ and PowerShell all deliver such names to the process. So the loader, the agent and the CLI warn about any environment variable that starts with `0CULAR_`, and name the `ZEROCULAR_` spelling. This ships with the pin (task 3.4).
5. **Shell completion.** Cobra derives `0CULAR_ACTIVE_HELP` and `0CULAR_COMPLETION_DESCRIPTIONS` from the command name, and POSIX shells can't export those. The completion docs point to Cobra's generic `COBRA_ACTIVE_HELP` and `COBRA_COMPLETION_DESCRIPTIONS` instead.

## Consequences

- The brand stays visible in everything users install and import, and every machine identifier is valid where it's used.
- The identifiers operators type during an incident have no 0-versus-O ambiguity.
- There are two spellings to learn. The README and the docs for metrics, environment variables and CEL show both, and spell out "zero-cular".
- Metric names are longer: `zerocular_loads_total`.
- Ports to other languages use `zerocular`, because Python import names and Cargo crate names can't start with a digit.
- Typosquatting: reserving `0cular` on npm, PyPI and Docker Hub before the first release would close the easy gaps. That's an outward-facing step for the maintainer to decide.
- A proper trademark clearance (USPTO, EUIPO and WIPO, in Classes 9 and 42) is advisable before heavy branding.
- Moving from the personal account to an organization later would change the module path, so it would need a new ADR and a migration note.

## Alternatives considered

- **Rename to a letter-first name.** It avoids the homoglyph, the collisions and the second spelling. Rejected: the maintainer chose to keep 0cular.
- **`ocular` as the letter-first form.** It's the shortest and matches how the name sounds. Rejected: an O-for-0 slip in a break-glass variable fails silently under systemd, and "ocular" is shared with other projects, including crashappsec/ocular.
- **`x0cular`.** It keeps the string `0cular`. Rejected: `x` reads as "experimental", and `x0` resembles a hex prefix.
- **Prometheus UTF-8 names** (`{"0cular_config_info"}`). Rejected: every query needs quoting, and legacy scrapers see escaped names.
- **A GitHub organization as owner.** It gives co-maintainers a stable home. Rejected for now: the maintainer chose the personal account.

## References

- Prometheus: [data model](https://prometheus.io/docs/concepts/data_model/), [UTF-8 names](https://prometheus.io/docs/guides/utf8/), [escaping schemes](https://prometheus.io/docs/instrumenting/escaping_schemes/)
- OpenTelemetry: [instrument name syntax](https://opentelemetry.io/docs/specs/otel/metrics/api/#instrument-name-syntax)
- Environment variables:
  - [POSIX Base Definitions, chapter 8](https://pubs.opengroup.org/onlinepubs/9799919799/basedefs/V1_chap08.html)
  - [systemd.exec](https://www.freedesktop.org/software/systemd/man/latest/systemd.exec.html): "cannot be empty or start with a digit"
  - [KEP-4369](https://github.com/kubernetes/enhancements/tree/master/keps/sig-node/4369-allow-special-characters-environment-variable): relaxed validation, beta in Kubernetes 1.32 and GA in 1.34
- Languages: the [CEL language definition](https://github.com/google/cel-spec/blob/master/doc/langdef.md); Go [identifiers](https://go.dev/ref/spec#Identifiers) and [module paths](https://go.dev/ref/mod#go-mod-file-ident)
- Packaging: Homebrew [cask tokens](https://docs.brew.sh/Cask-Cookbook#token-reference); GoReleaser [deprecations](https://goreleaser.com/deprecations/#brews); Cobra [shell completions](https://github.com/spf13/cobra/blob/main/site/content/completions/_index.md)
- Collisions: [crashappsec/ocular](https://github.com/crashappsec/ocular); USPTO [Reg. 5148860](https://tsdr.uspto.gov/#caseNumber=86684196&caseType=SERIAL_NO&searchType=statusSearch)
