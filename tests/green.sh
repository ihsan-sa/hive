#!/usr/bin/env bash
# Gate for cc-green and the lander: offline Go from the box toolchains, vet, unit tests, e2e.
set -euo pipefail
cd "$(dirname "$0")/.."
T="${TMPDIR:-/tmp}/hive-green"
mkdir -p "$T"
export PATH="$HOME/.cc/toolchains/go1.27.3/bin:$PATH" GOTOOLCHAIN=local GOPROXY=off GOFLAGS=-mod=mod
export GOMODCACHE="$HOME/.cc/toolchains/go-modcache" GOCACHE="$T/cache" GOPATH="$T/gopath"
go vet ./...
go test -short ./...
go test ./internal/e2e/ -timeout 30m
