#!/bin/sh
# test/livewin.sh [SITE] : on a Windows runner (Git Bash), the public link a Windows person clicks, end to end. The
# download page must offer /downloads/Hive-Setup.exe; that link (the site's redirect to the app's release) is fetched
# as a browser does into Downloads, must match the site's SHA256SUMS, and then gets what test/setupexe.js does to a
# download: the browser's internet-zone mark (curl sets none), a silent install, Apps & features at the version
# /app-version.json names, the Start menu shortcut reaching the Hive (or setup screen), and the uninstall.
# These two fetches reach GitHub; test/downloads.sh counts them as ours from the job's step name.
# Prints `ok live download win32 app-vVERSION ...`, or FAILED.
set -u
SITE=${1:-https://hivelab.ca}
UA='Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0 Safari/537.36'
D="${USERPROFILE:-$HOME}/Downloads"; mkdir -p "$D"; W=$(mktemp -d)
V=$(curl -fsS "$SITE/app-version.json" | sed -n 's/.*"version" *: *"\([0-9.]*\)".*/\1/p')
[ -n "$V" ] || { echo "FAILED live download win32: no version in $SITE/app-version.json"; exit 1; }
curl -fsSL -A "$UA" "$SITE/download" | grep -q 'href="/downloads/Hive-Setup.exe"' || { echo "FAILED live download win32: $SITE/download has no Hive-Setup.exe link"; exit 1; }
curl -fsSL -A "$UA" -o "$D/Hive-Setup.exe" "$SITE/downloads/Hive-Setup.exe" || { echo "FAILED live download win32: the download failed"; exit 1; }
curl -fsSL -A "$UA" -o "$W/SHA256SUMS" "$SITE/downloads/SHA256SUMS" || { echo "FAILED live download win32: no SHA256SUMS"; exit 1; }
want=$(awk '$2=="Hive-Setup.exe" || $2=="*Hive-Setup.exe" {print $1}' "$W/SHA256SUMS")
got=$(sha256sum < "$D/Hive-Setup.exe" | cut -d' ' -f1)
[ -n "$want" ] && [ "$got" = "$want" ] || { echo "FAILED live download win32: sum $got, SHA256SUMS says '$want'"; exit 1; }
size=$(wc -c < "$D/Hive-Setup.exe" | tr -d ' ')
HIVE_EXPECT_VERSION=$V node test/setupexe.js "$D/Hive-Setup.exe" > "$W/out" 2>&1; rc=$?; cat "$W/out"
[ $rc = 0 ] && grep -q '^ok setup-exe uninstall' "$W/out" || { echo "FAILED live download win32: test/setupexe.js on the live file ended $rc (above)"; exit 1; }
echo "ok live download win32 app-v$V: $size bytes from $SITE/downloads/Hive-Setup.exe, sum matches, installed at $V, opened, uninstalled"
