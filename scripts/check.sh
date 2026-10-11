#!/usr/bin/env bash
# Pre-merge checks: vet, race-enabled tests and known-vulnerability scan.
# Needs TEST_DATABASE_URL (PostgreSQL user with CREATEDB), gcc for -race, and govulncheck
# (go install golang.org/x/vuln/cmd/govulncheck@latest).
set -euo pipefail
cd "$(dirname "$0")/.."

echo "== go vet"
go vet ./...

echo "== go test -race"
CGO_ENABLED=1 go test -race -count=1 ./...

echo "== govulncheck"
GOVULNCHECK="$(command -v govulncheck || echo "$(go env GOPATH)/bin/govulncheck")"
"$GOVULNCHECK" ./...

echo "== all checks passed"
