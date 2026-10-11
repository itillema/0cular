# Contributing to 0cular

Thanks for your interest. 0cular is pre-alpha and has one maintainer, so issues and small, focused pull requests are the easiest way to help.

## Before you start

- For anything bigger than a small fix, open an issue first so we can agree on the approach.
- Design decisions are recorded as ADRs in [`docs/adr/`](docs/adr/). Changing a recorded decision takes a new ADR.
- Everyone taking part follows the [Code of Conduct](CODE_OF_CONDUCT.md).
- To report a vulnerability, follow [SECURITY.md](SECURITY.md) instead of opening an issue.

## Development setup

You need:
- Go 1.26 or newer;
- [just](https://just.systems/);
- [golangci-lint](https://golangci-lint.run/) v2, the same version CI uses (v2.14.0);
- on Windows, a C compiler such as WinLibs or MSYS2 gcc, because the race detector needs cgo;
- Docker, only for the integration tests (from v0.3) and the demo.

The main commands:

```bash
just check            # lint, tests with the race detector, and govulncheck
just fuzz             # each fuzz target for FUZZTIME (default 30s)
go test ./replay/...  # the replay suite alone
```

## Pull requests

- `just check` must pass.
- Add tests with every change, and update the docs when behavior changes.
- Use [Conventional Commits](https://www.conventionalcommits.org/) for commit messages (`feat:`, `fix:`, `docs:`, `test:`, `build:`, `ci:`, `chore:`, `refactor:`). Release notes are generated from them.
- Sign off every commit (see below). A DCO check runs on every pull request.

## Developer Certificate of Origin

By signing off a commit, you certify the [Developer Certificate of Origin](https://developercertificate.org/): that you wrote the change or otherwise have the right to submit it under the project's license. Sign off with `git commit -s`, which adds a line like this:

```text
Signed-off-by: Your Name <you@example.com>
```

The name and email must match the commit's author. To sign off commits you've already made, run `git rebase --signoff <base>`.

## Replay scenarios

New scenarios are welcome. Each one needs:
- a link to a public postmortem;
- synthetic data, clearly labeled as such, using RFC 5737 addresses and `.example` names;
- a consumer stub that exits 0 on its baseline and fails on its candidate, proven by the replay tests;
- an `expected.yaml` with the expected outcome at each stage.

## License

By contributing, you agree that your contributions are licensed under the [Apache License 2.0](LICENSE).
