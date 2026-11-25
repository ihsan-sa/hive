#!/bin/sh
# test/notify-live.sh DEB VERSION : the update notice as a person on Linux sees it. notice-live.sh runs with
# HIVE_APP_TEST=1, which skips the system notification; this one doesn't. In a clean ubuntu:24.04 (podman, or docker)
# with a session D-Bus and a notification daemon (dunst, printing what it shows), the .deb's app is started with no
# HIVE_SITE, so it reads the live site. Checks: dunst shows "A newer Hive app is available" with "Version VERSION",
# newer-seen.txt then says VERSION, clicking it opens the download page (a stand-in xdg-open writes down the address),
# and a second launch shows no second notification (once per version).
set -u
deb=$1; want=$2
[ -f "$deb" ] && [ -n "$want" ] || { echo "usage: test/notify-live.sh PATH.deb VERSION"; exit 2; }
run=$(command -v podman || command -v docker)
dir=$(mktemp -d); cp "$deb" "$dir/app.deb"
cat > "$dir/in.sh" <<'IN'
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq >/dev/null 2>&1
apt-get install -y -qq /w/app.deb xvfb xauth dbus dbus-x11 dunst libnotify-bin > /tmp/apt.log 2>&1 || { echo "FAILED install: $(tail -3 /tmp/apt.log | tr '\n' ' ')"; exit 1; }
# What a click opens: Electron's shell.openExternal runs xdg-open; this one writes the address down instead of a browser.
printf '#!/bin/sh\necho "$@" >> /tmp/opened.txt\n' > /usr/local/bin/xdg-open; chmod +x /usr/local/bin/xdg-open
unset HIVE_SITE HIVE_APP_TEST; export HOME=/tmp/home; mkdir -p $HOME
xvfb-run -a dbus-run-session -- sh /w/desk.sh
IN
cat > "$dir/desk.sh" <<'IN'
dunst -print > /tmp/dunst.log 2>&1 &
sleep 2
stop() { for p in /proc/[0-9]*; do case "$(tr '\\0' ' ' < $p/cmdline 2>/dev/null)" in /opt/Hive/hive-app*) kill ${p#/proc/} 2>/dev/null;; esac; done; sleep 3; }
/opt/Hive/hive-app --no-sandbox > /tmp/app1.out 2>&1 &
i=0; while [ $i -lt 40 ]; do grep -qs 'A newer Hive app' /tmp/dunst.log && break; sleep 1; i=$((i+1)); done
sleep 2
ver=$(dpkg-query -W -f='${Version}' hive-app)
n1=$(grep -c "summary: 'A newer Hive app is available'" /tmp/dunst.log)
body=$(grep -o 'Version [0-9.]*\. Click to open the download page\.' /tmp/dunst.log | head -1)
seen=$(cat $HOME/.config/Hive/newer-seen.txt 2>/dev/null)
# The click: dunst's default action on the newest notification.
dunstctl action 0 > /dev/null 2>&1; sleep 3
opened=$(head -1 /tmp/opened.txt 2>/dev/null)
stop
/opt/Hive/hive-app --no-sandbox > /tmp/app2.out 2>&1 &
sleep 30
n2=$(grep -c "summary: 'A newer Hive app is available'" /tmp/dunst.log)
notes=$(grep -c 'update notice' $HOME/.config/Hive/app.log)
stop
echo "app $ver: first launch $n1 notification(s) in ${i} s, body \"${body:-none}\", seen ${seen:-none}, click opened ${opened:-nothing}; second launch: $((n2 - n1)) more, log has $notes update notice line(s)"
IN
out=$($run run --rm --network=host -v "$dir:/w:ro" ubuntu:24.04 sh /w/in.sh 2>&1 | tail -1)
cp=$?
rm -rf "$dir"
case "$out" in *": first launch 1 notification(s) in "*", body \"Version $want. Click to open the download page.\", seen $want, click opened https://"*"; second launch: 0 more, log has 2 update notice line(s)") echo "ok notify $out"; exit 0;; esac
echo "FAILED ${out#FAILED }"; exit 1
