#!/bin/sh
# test/notice-live.sh DEB [WANT] : installs the .deb in a clean ubuntu:24.04 (podman, or docker), starts the app under
# xvfb with no HIVE_SITE, so it asks the live site (https://hivelab.ca/app-version.json) which app is current, and
# reads its log after 30 s. WANT is the version the app should say is newer ("update notice WANT"); with no WANT the
# app must say nothing (it is the current one). The container first fetches the same file itself, so a quiet app
# means it found nothing newer, not that it had no network.
set -u
deb=$1; want=${2:-}
[ -f "$deb" ] || { echo "usage: test/notice-live.sh PATH.deb [VERSION]"; exit 2; }
run=$(command -v podman || command -v docker)
dir=$(mktemp -d); cp "$deb" "$dir/app.deb"
cat > "$dir/in.sh" <<'IN'
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq >/dev/null 2>&1
apt-get install -y -qq /w/app.deb xvfb xauth curl ca-certificates > /tmp/apt.log 2>&1 || { echo "FAILED install: $(tail -3 /tmp/apt.log | tr '\n' ' ')"; exit 1; }
site=$(curl -s --max-time 15 https://hivelab.ca/app-version.json)
case "$site" in '{"version":"'*) ;; *) echo "FAILED the container could not read the live app-version.json: $site"; exit 1;; esac
unset HIVE_SITE; export HOME=/tmp/home HIVE_APP_TEST=1; mkdir -p $HOME
xvfb-run -a /opt/Hive/hive-app --no-sandbox > /tmp/app.out 2>&1 &
sleep 30
ver=$(dpkg-query -W -f='${Version}' hive-app)
[ -s $HOME/.config/Hive/app.log ] || { echo "FAILED app $ver wrote no log in 30 s: $(tail -3 /tmp/app.out | tr '\n' ' ')"; exit 1; }
note=$(grep -h 'update notice' $HOME/.config/Hive/app.log | head -1 | sed 's/^.*update notice/update notice/')
echo "app $ver, site $site, log: ${note:-no update notice}"
IN
out=$($run run --rm --network=host -v "$dir:/w:ro" ubuntu:24.04 sh /w/in.sh 2>&1 | tail -1)
rm -rf "$dir"
if [ -n "$want" ]; then case "$out" in *", log: update notice $want") echo "ok notice $out"; exit 0;; esac
else case "$out" in *", log: no update notice") echo "ok quiet $out"; exit 0;; esac; fi
echo "FAILED ${out#FAILED }"; exit 1
