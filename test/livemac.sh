#!/bin/bash
# test/livemac.sh [FILE] [SITE] : on a Mac runner, the public link a Mac person clicks, end to end. FILE is
# Hive-mac-arm64.zip (the default) or Hive-mac-x64.zip. The download page must offer /downloads/FILE; that link (the
# site's redirect to the app's release) is fetched as a browser does into ~/Downloads, gets the quarantine mark a browser gives a download (curl sets none), must match the
# site's SHA256SUMS and hold the version /app-version.json names, and then gets what test/macdl.js does to a download:
# unpacked with the mark, moved into /Applications, the README's Open Anyway, opened from there, the update notice,
# and /Applications/Hive.app removed. Opened, it must show its setup screen (the Apple silicon runner, where no Hive can
# run), or with HIVE_LIVE_EXPECT=hive (the Intel runner after test/real.js) go straight to the Hive.
# These two fetches reach GitHub; test/downloads.sh counts them as ours from the job's step name.
# Prints `ok live download darwin app-vVERSION ...`, or FAILED.
set -u
F=${1:-Hive-mac-arm64.zip}; SITE=${2:-https://hivelab.ca}; EXPECT=${HIVE_LIVE_EXPECT:-setup}
UA='Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.1 Safari/605.1.15'
D="$HOME/Downloads"; mkdir -p "$D"; W=$(mktemp -d)
fail() { echo "FAILED live download darwin: $*"; exit 1; }
V=$(curl -fsS "$SITE/app-version.json" | sed -n 's/.*"version" *: *"\([0-9.]*\)".*/\1/p')
[ -n "$V" ] || fail "no version in $SITE/app-version.json"
curl -fsSL -A "$UA" "$SITE/download" | grep -q "href=\"/downloads/$F\"" || fail "$SITE/download has no $F link"
curl -fsSL -A "$UA" -o "$D/$F" "$SITE/downloads/$F" || fail "the download failed"
xattr -w com.apple.quarantine "0083;$(printf %x "$(date +%s)");Safari;$(uuidgen)" "$D/$F"
mark=$(xattr -p com.apple.quarantine "$D/$F") || fail "the quarantine mark did not stick"
curl -fsSL -A "$UA" -o "$W/SHA256SUMS" "$SITE/downloads/SHA256SUMS" || fail "no SHA256SUMS"
want=$(awk -v f="$F" '$2==f || $2=="*"f {print $1}' "$W/SHA256SUMS")
got=$(shasum -a 256 < "$D/$F" | cut -d' ' -f1)
[ -n "$want" ] && [ "$got" = "$want" ] || fail "sum $got, SHA256SUMS says '$want'"
ditto -x -k "$D/$F" "$W/x" || fail "the zip does not unpack"
inside=$(plutil -extract CFBundleShortVersionString raw "$W/x/Hive.app/Contents/Info.plist") || fail "no Hive.app/Contents/Info.plist in the zip"
[ "$inside" = "$V" ] || fail "the zip holds $inside, /app-version.json says $V"
size=$(wc -c < "$D/$F" | tr -d ' ')
[ -f test/shots/macdl.png ] && mv test/shots/macdl.png "$W/macdl.png"
# Where the Hive should answer, the Hive's VM in words before and (on a failure) after the open, with Lima's last lines.
LH="$HOME/.hive/lima"; LC="$HOME/.hive/lima-dist/bin/limactl"
vm() { [ -x "$LC" ] && LIMA_HOME="$LH" "$LC" list hive --format '{{.Status}}' 2>&1 || echo "no limactl at $LC"; }
[ "$EXPECT" = hive ] && echo "the Hive's VM before the open: $(vm)"
HIVE_DL_EXPECT=$([ "$EXPECT" = hive ] || echo setup) node test/macdl.js "$D/$F" > "$W/out" 2>&1; rc=$?; cat "$W/out"
[ -f test/shots/macdl.png ] && mv test/shots/macdl.png test/shots/livemac.png; [ -f "$W/macdl.png" ] && mv "$W/macdl.png" test/shots/macdl.png
if [ $rc != 0 ] && [ "$EXPECT" = hive ]; then
  echo "the Hive's VM after the open: $(vm)"
  for f in "$LH/hive/ha.stderr.log" "$LH/hive/serial.log" "$LH/hive/serialv.log"; do [ -f "$f" ] && { echo "--- last of $f"; tail -n 25 "$f"; }; done
fi
[ $rc = 0 ] && grep -q '^ok mac download launch' "$W/out" || fail "test/macdl.js on the live file ended $rc (above)"
[ -e /Applications/Hive.app ] && fail "/Applications/Hive.app is still there after the test"
echo "ok live download darwin app-v$V: $size bytes from $SITE/downloads/$F, quarantine mark ${mark%%;*}, sum matches, holds $V, opened from /Applications to $([ "$EXPECT" = hive ] && echo the Hive || echo its setup screen), removed"
