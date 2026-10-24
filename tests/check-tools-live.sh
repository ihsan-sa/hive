#!/usr/bin/env bash
# Live check of agent-installed tools: the real uv puts python 3 into the tools
# folder through the install verb, and a worker in the platform sandbox
# (bubblewrap on stock Ubuntu) runs it. Needs a host whose sandbox works and
# the network. Toolchain setup as in green.sh.
set -euo pipefail
cd "$(dirname "$0")/.."
if [ -d "$HOME/.cc/toolchains/go1.27.3" ]; then
	T="${TMPDIR:-/tmp}/hive-green"
	mkdir -p "$T"
	export PATH="$HOME/.cc/toolchains/go1.27.3/bin:$PATH" GOTOOLCHAIN=local GOPROXY=off GOFLAGS=-mod=mod
	export GOMODCACHE="$HOME/.cc/toolchains/go-modcache" GOCACHE="$T/cache" GOPATH="$T/gopath"
fi
go build -o "${TMPDIR:-/tmp}/hive-probe" ./cmd/hive
"${TMPDIR:-/tmp}/hive-probe" sandbox-probe
HIVE_E2E_SANDBOX=auto HIVE_E2E_TOOLS=live go test ./internal/e2e/ -run TestToolInstall -count=1 -v -timeout 20m
