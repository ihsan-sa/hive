#!/bin/sh
# What a person gets from the live site, checked end to end: sh test/live.sh [SITE]
# - the download page links exactly the five files (no 404 button, no file without a button)
# - each /downloads/NAME redirects to the release that /app-version.json names (app-vVERSION), not another
# - each file, downloaded through the site as a browser does, matches that release's SHA256SUMS
# Prints `ok live NAME` per file and `ok live site VERSION` at the end; FAILED lines otherwise.
set -u
SITE=${1:-https://hivelab.ca}
FILES="Hive-Setup.exe Hive-linux-amd64.deb Hive-linux-x86_64.rpm Hive-mac-arm64.zip Hive-mac-x64.zip"
W=$(mktemp -d "${TMPDIR:-/tmp}/live.XXXXXX"); trap 'rm -rf "$W"' EXIT
bad=0; fail() { echo "FAILED live $*"; bad=1; }

V=$(curl -fsS "$SITE/app-version.json" | sed -n 's/.*"version" *: *"\([0-9.]*\)".*/\1/p')
[ -n "$V" ] || { echo "FAILED live: no version in $SITE/app-version.json"; exit 1; }
TAG=app-v$V

curl -fsSL "$SITE/download" > "$W/page.html" || { echo "FAILED live: no $SITE/download"; exit 1; }
linked=$(grep -oE 'href="/downloads/[^"]+"' "$W/page.html" | sed 's#href="/downloads/##; s#"$##' | sort -u | tr '\n' ' ')
want=$(echo $FILES | tr ' ' '\n' | sort | tr '\n' ' ')
[ "$linked" = "$want" ] || fail "page links: '$linked', want '$want'"

loc() { curl -sS -o /dev/null -w '%{redirect_url}' "$SITE/downloads/$1"; }
case "$(loc SHA256SUMS)" in */releases/download/$TAG/SHA256SUMS) ;; *) fail "SHA256SUMS does not point at $TAG: $(loc SHA256SUMS)";; esac
curl -fsSL "$SITE/downloads/SHA256SUMS" > "$W/SHA256SUMS" || { echo "FAILED live: no SHA256SUMS"; exit 1; }

for f in $FILES; do
  to=$(loc "$f")
  case "$to" in */releases/download/$TAG/$f) ;; *) fail "$f points at '$to', not release $TAG"; continue;; esac
  curl -fsSL -A 'Mozilla/5.0' -o "$W/$f" "$SITE/downloads/$f" || { fail "$f: download failed"; continue; }
  want=$(awk -v f="$f" '$2==f || $2=="*"f {print $1}' "$W/SHA256SUMS")
  got=$(sha256sum "$W/$f" | cut -d' ' -f1)
  [ -n "$want" ] || { fail "$f: not in SHA256SUMS"; continue; }
  [ "$got" = "$want" ] && echo "ok live $f $(wc -c < "$W/$f") bytes, sum matches $TAG" || fail "$f: sum $got, SHA256SUMS says $want"
  rm -f "$W/$f"
done
[ $bad = 0 ] && echo "ok live site $V: page, redirects and all five files match release $TAG"
exit $bad
