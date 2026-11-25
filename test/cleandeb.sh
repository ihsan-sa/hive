#!/bin/sh
# test/cleandeb.sh DEB IMAGE... : installs the .deb with apt on each clean system image (as a person's plain Ubuntu or
# Debian would have it, not a CI runner with every library already there), then checks nothing the app links to is
# missing and that the app starts and shows its first screen. Runs with podman, or docker if there's no podman.
# The first find (2026-11-25): libasound.so.2 wasn't in the package's Depends, so on a clean Ubuntu 24.04 or Debian 12
# apt installed it without a word and the app could not start.
set -u
deb=$1; shift
[ -f "$deb" ] || { echo "usage: test/cleandeb.sh PATH.deb IMAGE..."; exit 2; }
run=$(command -v podman || command -v docker)
dir=$(mktemp -d); cp "$deb" "$dir/app.deb"
cat > "$dir/in.sh" <<'IN'
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq >/dev/null 2>&1
apt-get install -y -qq /w/app.deb > /tmp/apt.log 2>&1 || { echo "FAILED apt install: $(tail -3 /tmp/apt.log)"; exit 1; }
missing=$(ldd /opt/Hive/hive-app | grep 'not found' | awk '{print $1}' | tr '\n' ' ')
[ -z "$missing" ] || { echo "FAILED missing after install: $missing"; exit 1; }
# Only the test needs a screen; the app's own start is what's checked.
apt-get install -y -qq xvfb xauth > /dev/null 2>&1 || { echo "FAILED xvfb install"; exit 1; }
export HOME=/tmp/home HIVE_APP_TEST=1; mkdir -p $HOME
xvfb-run -a /opt/Hive/hive-app --no-sandbox > /tmp/app.out 2>&1 &
i=0; while [ $i -lt 45 ]; do grep -qs '^.*screen ' $HOME/.config/Hive/app.log && break; sleep 1; i=$((i+1)); done
shown=$(grep -hs 'screen ' $HOME/.config/Hive/app.log | head -1 | sed 's/^.*screen /screen /')
[ -n "$shown" ] || { echo "FAILED no screen in 45 s: $(grep -v '^$' /tmp/app.out | tail -3)"; exit 1; }
echo "$(apt-cache policy hive-app 2>/dev/null | sed -n 's/ *Installed: //p') installed with apt, nothing missing, the app showed its $shown in ${i} s"
IN
fail=0
for img in "$@"; do
  out=$($run run --rm --network=host -v "$dir:/w:ro" "$img" sh /w/in.sh 2>&1 | tail -1)
  case "$out" in FAILED*) echo "FAILED clean deb $img: ${out#FAILED }"; fail=1;; *) echo "ok clean deb $img: $out";; esac
done
rm -rf "$dir"
exit $fail
