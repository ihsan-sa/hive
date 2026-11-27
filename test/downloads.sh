#!/bin/sh
# Downloads by people: sh test/downloads.sh
# GitHub counts every fetch of an app-v* file, ours included (live.sh, app-release.sh's link check, notice checks, the
# workflow's public-link steps), so the raw counts say nothing alone. Per release this prints the five app files'
# downloads minus
#   - the counts frozen when the ledger started (test/downloads-base.txt, 2026-11-26: those may all be ours),
#   - our own fetches here since, one line each in test/own-downloads.log (`test/own-download TAG NAME WHY` adds one),
#   - the workflow's fetches since: each job attempt on the test branch whose step named in CI_STEPS below ran counts
#     its files once, for the release that was newest when the step started.
# What is left is `people N`: fetches nobody here logged. SHA256SUMS is shown, not counted. GitHub's counts lag our
# fetches by minutes, so right after a test N can dip below 0 for a while; read it again later.
set -u
D=$(cd "$(dirname "$0")" && pwd)
REPO=${HIVE_APP_REPO:-ihsan-sa/hive}
W=$(mktemp -d "${TMPDIR:-/tmp}/downloads.XXXXXX"); trap 'rm -rf "$W"' EXIT
api() { if [ -n "${GH_TOKEN:-}" ]; then gh api "$1"; else curl -fsS "https://api.github.com/$1"; fi; }
SINCE=$(sed -n 's/.*started (\([0-9T:-]*\)Z).*/\1/p' "$D/downloads-base.txt")
[ -n "$SINCE" ] || { echo "FAILED downloads: no start time in downloads-base.txt"; exit 1; }
tags=$(api "repos/$REPO/git/matching-refs/tags/app-v" | python3 -I -c 'import json,sys; [print(r["ref"].split("/")[-1]) for r in json.load(sys.stdin)]') || { echo "FAILED downloads: no tag list"; exit 1; }
[ -n "$tags" ] || { echo "FAILED downloads: no app-v* tags"; exit 1; }
for t in $tags; do api "repos/$REPO/releases/tags/$t" > "$W/rel-$t.json" || { echo "FAILED downloads: $t"; exit 1; }; done
api "repos/$REPO/actions/runs?branch=test-desktop-app&created=%3E%3D${SINCE}:00Z&per_page=100" > "$W/runs.json" || { echo "FAILED downloads: no run list"; exit 1; }
for r in $(python3 -I -c 'import json,sys; [print(x["id"]) for x in json.load(open(sys.argv[1]))["workflow_runs"]]' "$W/runs.json"); do
  api "repos/$REPO/actions/runs/$r/jobs?filter=all&per_page=100" > "$W/jobs-$r.json" || { echo "FAILED downloads: jobs of run $r"; exit 1; }
done
python3 -I - "$W" "$D/downloads-base.txt" "$D/own-downloads.log" $tags <<'PY'
import glob, json, os, sys
w, base_f, own_f, tags = sys.argv[1], sys.argv[2], sys.argv[3], sys.argv[4:]
# The workflow steps that fetch release files, by step name, and what each fetches.
CI_STEPS = {"the public download link, as a Windows person clicks it": ["Hive-Setup.exe", "SHA256SUMS"],
            "the public download link, as an Apple silicon Mac person clicks it": ["Hive-mac-arm64.zip", "SHA256SUMS"],
            "the public download link, as an Intel Mac person clicks it": ["Hive-mac-x64.zip", "SHA256SUMS"]}
def rows(path):
    try: return [l.split() for l in open(path) if l.strip() and not l.startswith("#")]
    except FileNotFoundError: return []
base = {(r[0], r[1]): int(r[2]) for r in rows(base_f)}
rel = {t: json.load(open(os.path.join(w, "rel-%s.json" % t))) for t in tags}
own, ci = {}, {}
for r in rows(own_f):
    if len(r) >= 3: own[(r[1], r[2])] = own.get((r[1], r[2]), 0) + 1
pub = sorted((rel[t]["published_at"], t) for t in tags if rel[t].get("published_at"))
for f in glob.glob(os.path.join(w, "jobs-*.json")):
    for j in json.load(open(f))["jobs"]:
        for s in j.get("steps") or []:
            if s["name"] in CI_STEPS and s.get("conclusion") in ("success", "failure") and s.get("started_at"):
                live = [t for p, t in pub if p <= s["started_at"]]
                for name in CI_STEPS[s["name"]] if live else []:
                    ci[(live[-1], name)] = ci.get((live[-1], name), 0) + 1
for t in tags:
    raw = b = o = c = 0; sums = ""
    for a in rel[t]["assets"]:
        k = (t, a["name"]); n = a["download_count"]
        if a["name"] == "SHA256SUMS":
            sums = " SHA256SUMS %d-%d-%d-%d=%d" % (n, base.get(k, 0), own.get(k, 0), ci.get(k, 0), n - base.get(k, 0) - own.get(k, 0) - ci.get(k, 0)); continue
        raw += n; b += base.get(k, 0); o += own.get(k, 0); c += ci.get(k, 0)
    print("%s people %d (raw %d, before the ledger %d, ours here since %d, CI since %d;%s)" % (t, raw - b - o - c, raw, b, o, c, sums))
PY
