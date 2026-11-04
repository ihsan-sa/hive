#!/bin/sh
# Uninstall the Hive from a Mac. One line:
#   curl -fsSL https://hivelab.ca/uninstall-mac.sh | sh
# Deletes its VM and ~/.hive after you type delete. Its own Lima only: a Lima of yours, in ~/.lima, isn't touched.
set -eu

AGENT=hive.vm
MARK='# added by the Hive installer'

die() { echo "hive: $*" >&2; exit 1; }

main() {
  [ "$(uname -s)" = Darwin ] || die "this is the Mac uninstaller. On Linux: curl -fsSL https://hivelab.ca/uninstall.sh | sh"
  [ "$(id -u)" != 0 ] || die "run this as yourself, not with sudo"
  D="$HOME/.hive"
  [ -e "$D/.hive-mac" ] || die "there's no Hive from the Mac installer here, so nothing changed"

  echo "This deletes the Hive's Linux VM and everything in it: projects, notes, board and Claude sign-in. It also ends its agents. Your Mac's own files stay."
  ( : </dev/tty ) 2>/dev/null || die "this needs a terminal to ask you"
  printf "Type delete to go ahead: " >/dev/tty
  read -r answer </dev/tty
  [ "$answer" = delete ] || die "nothing changed"

  launchctl bootout "gui/$(id -u)/$AGENT" 2>/dev/null || true
  rm -f "$HOME/Library/LaunchAgents/$AGENT.plist"
  if [ -x "$D/lima-dist/bin/limactl" ]; then
    LIMA_HOME="$D/lima" "$D/lima-dist/bin/limactl" delete --force hive 2>/dev/null || true
  fi
  for f in "$HOME/.zprofile" "$HOME/.bash_profile" "$HOME/.profile"; do
    if grep -qF "$MARK" "$f" 2>/dev/null; then
      t=$(mktemp "$D/path.XXXXXX")
      grep -vF "$MARK" "$f" > "$t" || true
      cat "$t" > "$f"; rm -f "$t"
    fi
  done
  # The app the install made in ~/Applications; one that isn't ours stays.
  if grep -qs 'made by the Hive install' "$HOME/Applications/Hive.app/Contents/MacOS/Hive"; then rm -rf "$HOME/Applications/Hive.app"; fi
  rm -rf "$D"
  echo "The Hive is gone from this Mac."
  echo "Lima's download cache stays in ~/Library/Caches/lima (about 600 MB), as a Lima of yours may use it. If you don't use Lima, you can delete that folder."
}

main "$@"
