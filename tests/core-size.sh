#!/bin/sh
# Prints the size of the Hive's core as docs/core.md defines it, one
# "name value" per line, for the improve module to record with each merge's
# score: non-test Go lines in the core packages (core_lines), in every
# package (go_lines), and the release-built binary's bytes (binary_bytes).
# Usage: sh tests/core-size.sh [--lines]   (--lines skips the build)
set -e
cd "$(dirname "$0")/.."
core=$(sed -n 's/^<!-- core: \(.*\) -->$/\1/p' docs/core.md)
[ -n "$core" ] || { echo "docs/core.md has no <!-- core: ... --> line" >&2; exit 1; }
lines() { find "$@" -name '*.go' ! -name '*_test.go' ! -path '*/testdata/*' -exec cat {} + | wc -l | tr -d ' '; }
# shellcheck disable=SC2086 # the list is space-separated on purpose
for p in $core; do
	[ -d "$p" ] || { echo "docs/core.md names $p, which isn't a folder" >&2; exit 1; }
done
# shellcheck disable=SC2086
echo "core_lines $(lines $core)"
echo "go_lines $(lines cmd internal modules ./*.go)"
[ "$1" = --lines ] && exit 0
out=$(mktemp -d)
trap 'rm -rf "$out"' EXIT
CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o "$out/hive" ./cmd/hive
echo "binary_bytes $(wc -c < "$out/hive" | tr -d ' ')"
