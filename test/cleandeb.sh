#!/bin/sh
# test/cleandeb.sh DEB|RPM IMAGE... : installs the .deb with apt (or the .rpm with dnf) on each clean system image (as a person's plain Ubuntu or
# Debian would have it, not a CI runner with every library already there), then checks nothing the app links to is
# missing and that the app starts and shows its first screen. Runs with podman, or docker if there's no podman.
# The first find (2026-11-25): libasound.so.2 wasn't in the package's Depends, so on a clean Ubuntu 24.04 or Debian 12
# apt installed it without a word and the app could not start.
# The second: with plain libasound2, apt on Ubuntu 24.04 met it with another package that provides the name and the
# app died on a missing ALSA symbol; Depends now names libasound2t64 first.
# IMAGE@gdebi or IMAGE@pkcon installs with what a double-click opens instead of apt: gdebi on Mint, GNOME Software's
# PackageKit on Debian (the same engines, without a screen).
# Last, each removes the app again (`apt remove`, or PackageKit's remove as App Center and GNOME Software do) and checks
# nothing of it is left: no /opt/Hive, menu entry, icon, command link or AppArmor profile.
# An .rpm takes Fedora images the same way: dnf install, or IMAGE@pkcon for what GNOME Software opens on a double-click.
set -u
deb=$1; shift
[ -f "$deb" ] || { echo "usage: test/cleandeb.sh PATH.deb|PATH.rpm IMAGE..."; exit 2; }
kind=deb; case "$deb" in *.rpm) kind=rpm;; esac
run=$(command -v podman || command -v docker)
dir=$(mktemp -d); cp "$deb" "$dir/app.$kind"
cat > "$dir/rpm.sh" <<'IN'
# Fedora: the same steps as in.sh below, with dnf and rpm.
# The test's own tools go in first: a dnf run between PackageKit's install and its remove changes the rpm database
# under packagekitd, and its remove then fails ("no longer valid because the system repository has changed").
dnf install -y -q xorg-x11-server-Xvfb xorg-x11-xauth procps-ng > /dev/null 2>&1 || { echo "FAILED xvfb install"; exit 1; }
case "$HOW" in
  pkcon) dnf install -y -q PackageKit dbus-daemon polkit > /dev/null 2>&1 || { echo "FAILED packagekit install"; exit 1; }
         # A system bus, as a real Fedora has; pkcon's first call starts packagekitd through it (D-Bus activation).
         # Starting packagekitd by hand probably raced that: if it was slow to take its name, a second one was activated
         # and pkcon said "The daemon crashed mid-transaction!" (1 run in 11).
         mkdir -p /run/dbus; dbus-daemon --system --fork; sleep 1
         cp /w/app.rpm /tmp/app.rpm
         if command -v pkgcli > /dev/null; then pkgcli -y install /tmp/app.rpm; else pkcon -y --noninteractive install-local /tmp/app.rpm; fi > /tmp/in.log 2>&1 ;;
  *) dnf install -y /w/app.rpm > /tmp/in.log 2>&1 ;;
esac || { echo "FAILED ${HOW:-dnf} install: $(grep -v '^$' /tmp/in.log | tail -3 | tr '\n' ' ')"; exit 1; }
rpm -q hive-app > /dev/null 2>&1 || { echo "FAILED ${HOW:-dnf} install left hive-app not installed: $(grep -v '^$' /tmp/in.log | tail -3 | tr '\n' ' ')"; exit 1; }
missing=$(ldd /opt/Hive/hive-app | grep 'not found' | awk '{print $1}' | tr '\n' ' ')
[ -z "$missing" ] || { echo "FAILED missing after install: $missing"; exit 1; }
command -v xvfb-run > /dev/null || { echo "FAILED no xvfb-run"; exit 1; }
export HOME=/tmp/home HIVE_APP_TEST=1; mkdir -p $HOME
xvfb-run -a /opt/Hive/hive-app --no-sandbox > /tmp/app.out 2>&1 &
i=0; while [ $i -lt 45 ]; do grep -qs '^.*screen ' $HOME/.config/Hive/app.log && break; sleep 1; i=$((i+1)); done
shown=$(grep -hs 'screen ' $HOME/.config/Hive/app.log | head -1 | sed 's/^.*screen /screen /')
[ -n "$shown" ] || { echo "FAILED no screen in 45 s: $(grep -v '^$' /tmp/app.out | tail -3 | tr '\n' ' ')"; exit 1; }
ver=$(rpm -q --qf '%{VERSION}' hive-app)
for p in /proc/[0-9]*; do case "$(tr '\\0' ' ' < $p/cmdline 2>/dev/null)" in /opt/Hive/hive-app*) kill ${p#/proc/} 2>/dev/null;; esac; done; sleep 2
case "$HOW" in
  pkcon) if command -v pkgcli > /dev/null; then pkgcli -y remove hive-app; else pkcon -y --noninteractive remove hive-app; fi > /tmp/rm.log 2>&1 ;;
  *) dnf remove -y hive-app > /tmp/rm.log 2>&1 ;;
esac || { echo "FAILED ${HOW:-dnf} remove: $(grep -v '^$' /tmp/rm.log | tail -3 | tr '\n' ' ')"; exit 1; }
rpm -q hive-app > /dev/null 2>&1 && { echo "FAILED remove left hive-app installed: $(grep -v '^$' /tmp/rm.log | tail -3 | tr '\n' ' ')"; exit 1; }
left=$(ls -d /opt/Hive /usr/bin/hive-app /etc/alternatives/hive-app /var/lib/alternatives/hive-app /usr/share/applications/hive-app.desktop /usr/share/icons/hicolor/*/apps/hive-app.png 2>/dev/null | tr '\n' ' ')
[ -z "$left" ] || { echo "FAILED left after remove: $left"; exit 1; }
echo "$ver installed with ${HOW:-dnf}, nothing missing, the app showed its $shown in ${i} s; removed with ${HOW:-dnf}, nothing left"
IN
cat > "$dir/in.sh" <<'IN'
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq >/dev/null 2>&1
case "$HOW" in
  gdebi) apt-get install -y -qq gdebi-core > /dev/null 2>&1 || { echo "FAILED gdebi-core install"; exit 1; }
         gdebi -n /w/app.deb > /tmp/apt.log 2>&1 ;;
  pkcon) apt-get install -y -qq packagekit polkitd dbus > /dev/null 2>&1 || { echo "FAILED packagekit install"; exit 1; }
         # PackageKit 1.3.4 (Ubuntu 26.04) has pkgcli in place of pkcon; older ones keep pkcon in packagekit-tools.
         command -v pkgcli > /dev/null || apt-get install -y -qq packagekit-tools > /dev/null 2>&1
         # pkcon's first call starts packagekitd through the system bus (D-Bus activation), as on a real system.
         mkdir -p /run/dbus; dbus-daemon --system --fork; sleep 1
         cp /w/app.deb /tmp/app.deb
         if command -v pkgcli > /dev/null; then pkgcli -y install /tmp/app.deb; else pkcon -y --noninteractive install-local /tmp/app.deb; fi > /tmp/apt.log 2>&1 ;;
  *) apt-get install -y -qq /w/app.deb > /tmp/apt.log 2>&1 ;;
esac || { echo "FAILED ${HOW:-apt} install: $(grep -v '^$' /tmp/apt.log | tail -3 | tr '\n' ' ')"; exit 1; }
dpkg -s hive-app 2>/dev/null | grep -q '^Status: install ok installed' || { echo "FAILED ${HOW:-apt} install left hive-app not installed: $(grep -v '^$' /tmp/apt.log | tail -3 | tr '\n' ' ')"; exit 1; }
missing=$(ldd /opt/Hive/hive-app | grep 'not found' | awk '{print $1}' | tr '\n' ' ')
[ -z "$missing" ] || { echo "FAILED missing after install: $missing"; exit 1; }
# Only the test needs a screen; the app's own start is what's checked.
apt-get install -y -qq xvfb xauth > /dev/null 2>&1 || { echo "FAILED xvfb install"; exit 1; }
export HOME=/tmp/home HIVE_APP_TEST=1; mkdir -p $HOME
xvfb-run -a /opt/Hive/hive-app --no-sandbox > /tmp/app.out 2>&1 &
i=0; while [ $i -lt 45 ]; do grep -qs '^.*screen ' $HOME/.config/Hive/app.log && break; sleep 1; i=$((i+1)); done
shown=$(grep -hs 'screen ' $HOME/.config/Hive/app.log | head -1 | sed 's/^.*screen /screen /')
[ -n "$shown" ] || { echo "FAILED no screen in 45 s: $(grep -v '^$' /tmp/app.out | tail -3 | tr '\n' ' ')"; exit 1; }
ver=$(dpkg-query -W -f='${Version}' hive-app)
# Then the README's removal, with the same engine: nothing of the app may stay (the app's own settings in ~/.config/Hive
# do stay, as the README says; the AppArmor profile is only put in where AppArmor runs, which a container doesn't).
for p in /proc/[0-9]*; do case "$(tr '\\0' ' ' < $p/cmdline 2>/dev/null)" in /opt/Hive/hive-app*) kill ${p#/proc/} 2>/dev/null;; esac; done; sleep 2
case "$HOW" in
  pkcon) if command -v pkgcli > /dev/null; then pkgcli -y remove hive-app; else pkcon -y --noninteractive remove hive-app; fi > /tmp/rm.log 2>&1 ;;
  *) apt-get remove -y -qq hive-app > /tmp/rm.log 2>&1 ;;
esac || { echo "FAILED ${HOW:-apt} remove: $(grep -v '^$' /tmp/rm.log | tail -3 | tr '\n' ' ')"; exit 1; }
dpkg -s hive-app 2>/dev/null | grep -q '^Status: install ok installed' && { echo "FAILED remove left hive-app installed: $(grep -v '^$' /tmp/rm.log | tail -3 | tr '\n' ' ')"; exit 1; }
left=$(ls -d /opt/Hive /usr/bin/hive-app /etc/alternatives/hive-app /etc/apparmor.d/hive-app /usr/share/applications/hive-app.desktop /usr/share/icons/hicolor/*/apps/hive-app.png 2>/dev/null | tr '\n' ' ')
[ -z "$left" ] || { echo "FAILED left after remove: $left"; exit 1; }
echo "$ver installed with ${HOW:-apt}, nothing missing, the app showed its $shown in ${i} s; removed with ${HOW:-apt}, nothing left"
IN
fail=0
for spec in "$@"; do
  img=$spec; how=; case "$spec" in *@gdebi|*@pkcon) img=${spec%@*}; how=${spec##*@};; esac
  script=in.sh; [ $kind = rpm ] && script=rpm.sh
  out=$($run run --rm --network=host -e HOW="$how" -v "$dir:/w:ro" "$img" sh /w/$script 2>&1 | tail -1)
  # Only the script's own last line counts as a pass; anything else (a crash, a log line) is a failure.
  case "$out" in *" installed with "*", the app showed its screen "*"; removed with "*", nothing left") echo "ok clean $kind $spec: $out";;
    *) echo "FAILED clean $kind $spec: ${out#FAILED }"; fail=1;; esac
done
rm -rf "$dir"
exit $fail
