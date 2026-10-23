#!/usr/bin/env bash
# The suite a change must pass before it lands: vet, unit tests, end to end.
# On the box, Go comes offline from the box toolchains; elsewhere the Go on PATH is used.
set -euo pipefail
cd "$(dirname "$0")/.."
if [ -d "$HOME/.cc/toolchains/go1.27.3" ]; then
	T="${TMPDIR:-/tmp}/hive-green"
	mkdir -p "$T"
	export PATH="$HOME/.cc/toolchains/go1.27.3/bin:$PATH" GOTOOLCHAIN=local GOPROXY=off GOFLAGS=-mod=mod
	export GOMODCACHE="$HOME/.cc/toolchains/go-modcache" GOCACHE="$T/cache" GOPATH="$T/gopath"
fi
go vet ./...
go test -short ./...
HIVE_E2E_TUI=1 go test ./internal/e2e/ -timeout 30m   # the control center in tmux too, where tmux is installed
