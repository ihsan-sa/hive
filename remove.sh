#!/bin/sh
# Uninstall the Hive on Linux. One line:
#   curl -fsSL https://hive.ihsan.cc/uninstall.sh | sh
# Deletes the hive user (or HIVE_USER=name) and everything in its home, after you type delete.
# Claude Code stays signed in only inside that home, so its sign-in goes too.
set -eu

die() { echo "hive: $*" >&2; exit 1; }

main() {
  U="${HIVE_USER:-hive}"
  case "$U" in "" | -* | root | *[!a-z0-9_-]*) die "HIVE_USER must be a plain user name other than root, not $U" ;; esac
  if [ "$(id -u)" = 0 ]; then ROOT=""; else command -v sudo >/dev/null 2>&1 || die "this needs root"; ROOT="sudo"; fi
  id "$U" >/dev/null 2>&1 || die "no user $U, so nothing to remove"
  H=$(getent passwd "$U" | cut -d: -f6)
  $ROOT test -e "$H/.hive-installed" || die "$U doesn't have a Hive installed by this package, so I won't touch it"

  echo "This deletes the user $U and all of $H: its projects, notes, board and Claude sign-in."
  [ -r /dev/tty ] || die "this needs a terminal to ask you"
  printf "Type delete to go ahead: " >/dev/tty
  read -r answer </dev/tty
  [ "$answer" = delete ] || die "nothing changed"

  $ROOT loginctl disable-linger "$U" 2>/dev/null || true
  $ROOT loginctl terminate-user "$U" 2>/dev/null || true
  $ROOT pkill -KILL -u "$U" 2>/dev/null || true
  sleep 1
  $ROOT userdel -r "$U" 2>/dev/null || die "couldn't delete $U; check for its processes and run: sudo userdel -r $U"
  echo "Removed."
}

main "$@"
