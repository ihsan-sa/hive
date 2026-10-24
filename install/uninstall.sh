#!/bin/sh
# Uninstall the Hive (your folder, ~/hive by default, is left alone):
#   curl -fsSL https://hive.ihsan.cc/uninstall.sh | sh
# It removes hive and hive-v0, its name up to v0.1.2. As root,
# HIVE_USER=<name> removes the system service install.sh set up for that user
# and root's records for it (the repo it installs from and its token files);
# the worker's own binary is removed as the worker.
set -eu
CMD="${HIVE_CMD:-hive}"
DIR="${HIVE_BIN_DIR:-$HOME/.local/bin}"
case "$CMD" in "" | -* | *[!A-Za-z0-9._-]*) echo "hive: HIVE_CMD must be a plain name" >&2; exit 1 ;; esac
if [ -n "${HIVE_USER:-}" ]; then
  user=$HIVE_USER
  fail() { echo "$CMD: $*" >&2; exit 1; }
  [ "$(id -u)" = 0 ] || fail "HIVE_USER uninstalls another user's Hive, which needs root"
  case "$user" in "" | -* | root | *[!a-z0-9_-]*) fail "HIVE_USER must be a plain user name other than root, not $user" ;; esac
  unit="hive-$user.service"
  if [ -d /run/systemd/system ]; then systemctl disable --now "$unit" 2>/dev/null || true; fi
  rm -f "/etc/systemd/system/$unit"
  if [ -d /run/systemd/system ]; then systemctl daemon-reload; fi
  rm -rf "/etc/hive/$user"
  if entry=$(getent passwd "$user"); then
    home=$(printf '%s\n' "$entry" | cut -d: -f6)
    cd /
    # shellcheck disable=SC2016 # expanded by the worker's shell
    runuser -u "$user" -- env -i HOME="$home" PATH=/usr/bin:/bin CMD="$CMD" sh -c 'for c in "$CMD" hive-v0; do rm -f "$HOME/.local/bin/$c"; done' ||
      fail "removing $user's binary as $user failed; the service and root's records are gone"
  fi
  echo "The Hive of $user is uninstalled. Its folder and projects are untouched."
  exit 0
fi
for c in "$CMD" hive-v0; do
  if [ -x "$DIR/$c" ]; then "$DIR/$c" uninstall || true; fi
  rm -f "$DIR/$c"
done
echo "The Hive is uninstalled. Your folder and its projects are untouched."
