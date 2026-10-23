#!/usr/bin/env bash
# The live matrix: the e2e suite on the fake harness and the live probes on
# real Claude Code (plan usage), each under bubblewrap and bare, all four at
# once, then a pass/fail table. Logs stay in the folder it prints.
#
# Safe on a shared machine: it re-runs itself in a systemd user scope capped by
# TasksMax and RuntimeMaxSec (an uncapped probe once forked thousands of
# copies of itself), under a fake HOME, with systemctl, loginctl and crontab
# stubbed on PATH so nothing is installed or enabled on the real account.
# LIVE_CELLS picks cells (default: fake-bwrap fake-bare claude-bwrap claude-bare).
set -euo pipefail
cd "$(dirname "$0")/.."  # from here on $0 may be wrong, so the re-run names tests/live.sh
if [ -z "${HIVE_LIVE_SCOPED:-}" ]; then
	exec systemd-run --user --scope --quiet -p TasksMax="${LIVE_TASKS:-4096}" -p RuntimeMaxSec="${LIVE_SECONDS:-14400}" \
		env HIVE_LIVE_SCOPED=1 bash tests/live.sh "$@"
fi

# Go and its caches come from the real account before HOME moves.
if [ -d "$HOME/.cc/toolchains/go1.27.3" ]; then
	export PATH="$HOME/.cc/toolchains/go1.27.3/bin:$PATH" GOTOOLCHAIN=local GOPROXY=off GOFLAGS=-mod=mod
	export GOMODCACHE="$HOME/.cc/toolchains/go-modcache"
fi
export GOMODCACHE="${GOMODCACHE:-$(go env GOMODCACHE)}" GOCACHE="${GOCACHE:-$(go env GOCACHE)}" GOPATH="$(go env GOPATH)"

out=$(mktemp -d "${TMPDIR:-/tmp}/hive-live-XXXXXX")
real=$HOME
export HOME="$out/home"
mkdir -p "$HOME/.claude" "$out/stubs"
# Claude Code's login, linked so a token refresh reaches the real account too.
if [ -f "$real/.claude/.credentials.json" ]; then
	ln -s "$real/.claude/.credentials.json" "$HOME/.claude/.credentials.json"
fi
for s in systemctl loginctl crontab; do
	printf '#!/bin/sh\necho "%s $*" >> "%s/stubbed.log"\n' "$s" "$out" > "$out/stubs/$s"
	chmod +x "$out/stubs/$s"
done
export PATH="$out/stubs:$PATH"

# cell NAME PATTERN ENV...: one go test run, its log and exit code in $out.
cell() {
	local name=$1 run=$2
	shift 2
	env "$@" go test ./internal/e2e/ -count=1 -v -timeout 4h -run "$run" > "$out/$name.log" 2>&1
	echo $? > "$out/$name.code"
}
cells=(${LIVE_CELLS:-fake-bwrap fake-bare claude-bwrap claude-bare})
set +e
for c in "${cells[@]}"; do
	case $c in
	fake-bwrap) cell "$c" . HIVE_E2E_SANDBOX=auto & ;;
	fake-bare) cell "$c" . HIVE_E2E_SANDBOX=bare & ;;
	claude-bwrap) cell "$c" TestLive HIVE_E2E_HARNESS=claude HIVE_E2E_SANDBOX=auto & ;;
	claude-bare) cell "$c" TestLive HIVE_E2E_HARNESS=claude HIVE_E2E_SANDBOX=bare & ;;
	*) echo "unknown cell $c" >&2; exit 2 ;;
	esac
done
wait

tests=$(cat "$out"/*.log | sed -n 's/^--- [A-Z]*: \(Test[A-Za-z0-9_]*\) .*/\1/p' | sort -u)
printf '%-28s' test
printf ' %-13s' "${cells[@]}"
echo
for t in $tests; do
	printf '%-28s' "$t"
	for c in "${cells[@]}"; do
		r=$(sed -n "s/^--- \([A-Z]*\): $t .*/\1/p" "$out/$c.log" | head -1)
		printf ' %-13s' "${r:--}"
	done
	echo
done
status=0
for c in "${cells[@]}"; do
	code=$(cat "$out/$c.code")
	[ "$code" = 0 ] || { echo "$c: go test exited $code"; status=1; }
done
[ -s "$out/stubbed.log" ] && echo "stubbed calls: $(wc -l < "$out/stubbed.log") (see $out/stubbed.log)"
echo "logs: $out"
exit $status
