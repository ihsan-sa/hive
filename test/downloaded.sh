#!/bin/bash
# What a person sees when they open the Mac download: unpack the zip as a browser download would leave it
# (quarantine mark set), open it, and take screenshots. Runs on a Mac runner only. Never fails the build: it records.
# usage: test/downloaded.sh dist/Hive-...-mac.zip
set -u
zip="$1"; shots="$(cd "$(dirname "$0")" && pwd)/shots"; mkdir -p "$shots"
dl="$(mktemp -d)"; ditto -x -k "$zip" "$dl"; app="$dl/Hive.app"
shot() { screencapture -x "$shots/downloaded-$1.png" || echo "screenshot $1 failed"; }
running() { pgrep -f "$app/Contents/MacOS/Hive" >/dev/null && echo "RUNNING" || echo "NOT RUNNING"; }
echo "== system"; sw_vers; uname -m; spctl --status
echo "== signature"; codesign -dv --verbose=2 "$app" 2>&1 | grep -E 'Signature|TeamIdentifier|flags'
echo "== mark it as downloaded"
xattr -r -w com.apple.quarantine "0081;$(printf %x "$(date +%s)");Safari;$(uuidgen)" "$app"
xattr -p com.apple.quarantine "$app"
echo "== Gatekeeper's verdict"; spctl -a -vv -t exec "$app" 2>&1
echo "== double-click"; ( open "$app"; echo "open exit $?" ) & sleep 25; shot 1-first-open; echo "app: $(running)"
osascript -e 'tell application "System Events" to get name of every window of every process whose visible is true' 2>&1 | head -5
osascript -e 'tell application "System Events" to tell process "CoreServicesUIAgent" to get value of every static text of every window' 2>&1 | head -5
log show --last 2m --predicate 'process == "syspolicyd" OR process == "CoreServicesUIAgent"' --style compact 2>/dev/null | grep -iE 'hive|gatekeeper|quarantine|verif' | tail -15
echo "== press Done, then System Settings > Privacy & Security"
osascript -e 'tell application "System Events" to tell process "CoreServicesUIAgent" to click button "Done" of window 1' 2>&1 | head -2
sleep 2; open "x-apple.systempreferences:com.apple.settings.PrivacySecurity.extension?Security"; sleep 12
osascript -e 'tell application "System Events" to tell process "System Settings" to get value of every static text of every UI element of scroll area 1 of group 1 of group 2 of splitter group 1 of group 1 of window 1' 2>&1 | tr ',' '\n' | grep -iE 'hive|block|anyway' | head -5
shot 2-privacy-security; echo "app: $(running)"
osascript -e 'quit app "System Settings"' 2>&1 | head -1; pkill -x open
echo "== a second copy with the mark removed (xattr -dr com.apple.quarantine)"
dl2="$(mktemp -d)"; ditto -x -k "$zip" "$dl2"; app="$dl2/Hive.app"
xattr -r -w com.apple.quarantine "0081;$(printf %x "$(date +%s)");Safari;$(uuidgen)" "$app"; xattr -dr com.apple.quarantine "$app"
( open "$app"; echo "open exit $?" ) & sleep 25; shot 3-mark-removed; echo "app: $(running)"
pkill -x open; pkill -f "$app/Contents/MacOS/Hive"
exit 0
