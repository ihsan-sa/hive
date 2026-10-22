#!/bin/sh
# The suite a change must pass before it lands: vet, unit tests, end to end.
set -e
cd "$(dirname "$0")/.."
# The host's gate runs without the local SDK on PATH (see CLAUDE.md).
command -v go >/dev/null || PATH="$(ls -d "$HOME"/sdk/go*/bin 2>/dev/null | tail -1):$PATH"
go vet ./...
go test -short ./...
go test ./internal/e2e/ -timeout 30m
