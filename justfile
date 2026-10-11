# Development tasks for 0cular. Run `just --list` to see them.

set windows-shell := ["powershell.exe", "-NoLogo", "-NoProfile", "-NonInteractive", "-Command"]

# How long `just fuzz` fuzzes each target. Override with FUZZTIME or `just fuzztime=5s fuzz`.
fuzztime := env("FUZZTIME", "30s")

# Lint, test with the race detector, and check for known vulnerabilities.
check: lint test vuln

# Run the linters and the formatting checks.
lint:
    golangci-lint run

# Run all tests with the race detector.
test:
    go test -race -count=1 ./...

# Check the module and its dependencies for known vulnerabilities.
vuln:
    go tool govulncheck ./...

# Fuzz each fuzz target for `fuzztime`, one at a time.
fuzz:
    go run ./internal/cmd/fuzzall -fuzztime {{ fuzztime }}

# Format the code with gofumpt and goimports.
fmt:
    golangci-lint fmt
