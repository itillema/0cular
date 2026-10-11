# Security policy

0cular sits in the path of production configuration, so vulnerability reports get priority.

## Reporting a vulnerability

Report vulnerabilities privately through GitHub: on the repository's **Security** tab, choose **Report a vulnerability** ([direct link](https://github.com/itillema/0cular/security/advisories/new)). Please don't open a public issue, pull request or discussion about a suspected vulnerability.

Include:
- what's affected: the command, package or Action, and the version or commit;
- how to reproduce it, with a minimal contract and artifact if you can;
- the impact you expect.

## What to expect

0cular has one maintainer, so these are targets rather than guarantees:
- an acknowledgment within 7 days;
- an assessment and a plan within 30 days;
- coordinated disclosure: a fix or mitigation is agreed with you before anything is published, normally within 90 days of the report.

You'll be credited in the advisory unless you'd rather not be named.

## Supported versions

Before 1.0, only the latest release gets security fixes. 0cular is pre-alpha and has no releases yet.

## Scope

In scope: the `0cular` CLI, the Go packages in this module, the GitHub Action in `action/`, and the release artifacts and container image.

The replay scenarios in `replay/` contain consumer stubs that crash on purpose. Those crashes aren't vulnerabilities.
