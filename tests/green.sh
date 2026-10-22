#!/bin/sh
# The suite a change must pass before it lands: vet, unit tests, end to end.
set -e
cd "$(dirname "$0")/.."
go vet ./...
go test -short ./...
go test ./internal/e2e/ -timeout 30m
