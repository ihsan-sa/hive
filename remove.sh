#!/bin/sh
# Uninstall the Hive on Linux. One line:
#   curl -fsSL https://hivelab.ca/uninstall.sh | sh
# Deletes the hive user (or HIVE_USER=name) and its home, after you type delete.
# Your projects, notes and board stay unless you say to delete them: they're moved to ~/hive-kept-DATE first. HIVE_KEEP=projects,notes,board (any of them, or none) answers without asking.
# Claude Code stays signed in only inside that home, so its sign-in goes too.
# A Hive inside your own account (sh -s -- --this-account) removes only its folder, services and command. It never deletes your account.
set -eu

die() { echo "hive: $*" >&2; exit 1; }

# The AppArmor profile the install may have added. It's for the whole machine, so this only tells you. The same bytes as get.sh, get-mac.sh and sandbox.py's _userns_fix().
AA_TEXT='# installed by the Hive\nabi <abi/4.0>,\ninclude <tunables/global>\nprofile bwrap /usr/bin/bwrap flags=(attach_disconnected,mediate_deleted) {\n  allow capability,\n  allow file rwlkm /{**,},\n  allow network,\n  allow unix,\n  allow ptrace,\n  allow signal,\n  allow mqueue,\n  allow io_uring,\n  allow userns,\n  allow mount,\n  allow umount,\n  allow pivot_root,\n  allow dbus,\n  allow pix /** -> &bwrap//&unpriv_bwrap,\n}\nprofile unpriv_bwrap flags=(attach_disconnected,mediate_deleted) {\n  allow file rwlkm /{**,},\n  allow network,\n  allow unix,\n  allow ptrace,\n  allow signal,\n  allow mqueue,\n  allow io_uring,\n  allow userns,\n  allow mount,\n  allow umount,\n  allow pivot_root,\n  allow dbus,\n  allow pix /** -> &unpriv_bwrap,\n  audit deny capability,\n}\n'
bwrap_note() {
  f="${HIVE_AA_DIR:-/etc/apparmor.d}/bwrap"
  [ -e "$f" ] || return 0
  if printf "$AA_TEXT" | cmp -s - "$f"; then
    echo "The AppArmor profile the Hive added is still at $f, and nothing here changed it. It re-allows unprivileged user namespaces for bubblewrap, for every user of this machine. Other bubblewrap programs may rely on it. To remove it: sudo apparmor_parser -R $f && sudo rm $f"
  else
    echo "$f isn't the text the Hive wrote, so nothing changed there."
  fi
}

# The app-menu icon get.sh made, in the account that ran it (yours). Only the one it made.
launcher_gone() {
  D="${XDG_DATA_HOME:-$HOME/.local/share}"
  if grep -qs '^# made by the Hive install' "$D/applications/hive.desktop"; then rm -f "$D/applications/hive.desktop" "$D/icons/hicolor/512x512/apps/hive.png"; fi
}

# What can stay: your projects, notes (the knowledge base) and board (your chats, with their files). Each stays unless you say to delete it.
ask_del() {
  printf "Delete %s too? [y/N] " "$2" >/dev/tty
  read -r a </dev/tty || a=""
  case "$a" in y | Y | yes | Yes | YES) ;; *) KEEP="$KEEP $1" ;; esac
}
choose_keep() {
  KEEP=""
  if [ -n "${HIVE_KEEP+set}" ]; then
    for k in $(echo "$HIVE_KEEP" | tr ',' ' '); do
      case "$k" in projects | notes | board) KEEP="$KEEP $k" ;; none) ;; *) die "HIVE_KEEP takes projects, notes, board or none, not $k" ;; esac
    done
    return 0
  fi
  echo "Your projects, notes and board can stay, moved to a folder of their own. Press Enter to keep each one, or type y to delete it too."
  ask_del projects "your projects (projects)"
  ask_del notes "the Hive's notes (kb)"
  ask_del board "the board: your chats and their files (board.jsonl, files)"
}
keep_paths() { for k in $KEEP; do case "$k" in projects) echo projects ;; notes) echo kb ;; board) echo board.jsonl; echo files ;; esac; done; }
keep_words() { w=$(echo $KEEP | sed 's/ /, /g; s/\(.*\), /\1 and /'); echo "${w:-nothing}"; }
kept_dir() { b="$1/hive-kept-$(date +%Y%m%d)"; k="$b"; n=2; while [ -e "$k" ]; do k="$b-$n"; n=$((n + 1)); done; echo "$k"; }

this_account() {
  [ "$(id -u)" != 0 ] || die "--this-account is for your own account, and this is root"
  H="${HIVE_DIR:-$HOME/hive}"; H="${H%/}"
  [ "$H" != "${HOME%/}" ] || die "that's your home folder, so I won't touch it"
  [ -e "$H/.hive-account" ] && [ "$(cat "$H/.hive-account")" = "$HOME" ] || die "$H isn't a Hive installed in this account, so I won't touch it"

  ( : </dev/tty ) 2>/dev/null || die "this needs a terminal to ask you"
  choose_keep
  K=""; [ -z "$KEEP" ] || K=$(kept_dir "$(dirname "$H")")
  echo "This deletes $H and its Claude sign-in, and ends its agents. Kept: $(keep_words)${K:+, moved to $K}. The rest of your account stays."
  printf "Type delete to go ahead: " >/dev/tty
  read -r answer </dev/tty
  [ "$answer" = delete ] || die "nothing changed"

  systemctl --user stop hive-report.timer hive-report.service hive-update.timer hive-update.service hive-asks.timer hive-asks.service hive-outcomes.timer hive-outcomes.service hive-progress.timer hive-progress.service hive-dash.service hive-starter.service 2>/dev/null || true
  systemctl --user disable hive-report.timer hive-update.timer hive-asks.timer hive-outcomes.timer hive-progress.timer hive-dash.service hive-starter.service 2>/dev/null || true
  # The starter leaves its agents running when it stops, so end them too. Only this service's agents: your own sessions aren't in it.
  systemctl --user kill -s KILL hive-starter.service hive-update.service 2>/dev/null || true
  systemctl --user reset-failed hive-starter.service hive-dash.service hive-update.service 2>/dev/null || true
  # Agents' sandboxes and the broker they talk through. The broker and a sandbox started by the web terminal's door aren't in a service, so end them by name. Only this folder's.
  pkill -KILL -f "^[^ ]*python[^ ]* -I $H/core/broker\.py( |\$)" 2>/dev/null || true
  pkill -KILL -f "^[^ ]*bwrap .*--bind $H/\.sandbox/home " 2>/dev/null || true
  U="$HOME/.config/systemd/user"
  for n in hive-report.service hive-starter.service hive-dash.service hive-update.service hive-asks.service hive-outcomes.service hive-progress.service; do rm -rf "${U:?}/$n" "${U:?}/$n.d"; done
  rm -f "$U/hive-update.timer" "$U/hive-report.timer" "$U/hive-asks.timer" "$U/hive-outcomes.timer" "$U/hive-progress.timer"
  systemctl --user daemon-reload 2>/dev/null || true
  W="$HOME/.local/bin/hive"
  if grep -qs '^# made by the Hive install' "$W"; then rm -f "$W"; fi
  if [ -n "$K" ]; then
    mkdir -p "$K"
    for p in $(keep_paths); do [ ! -e "$H/$p" ] || mv "$H/$p" "$K/" || die "couldn't move $H/$p to $K. The Hive is stopped; nothing more was deleted"; done
  fi
  rm -rf "${H:?}"
  launcher_gone
  echo "Removed."
  [ -z "$K" ] || echo "Kept in $K: $(keep_words)."
  bwrap_note
}

main() {
  if [ "$(uname -s)" = Darwin ]; then die "on a Mac, the test build has its own line: curl -fsSL https://hivelab.ca/uninstall-mac.sh | sh"; fi
  for a in "$@"; do case "$a" in --this-account) this_account; return 0 ;; *) die "unknown option $a" ;; esac; done
  U="${HIVE_USER:-hive}"
  case "$U" in "" | -* | root | *[!a-z0-9_-]*) die "HIVE_USER must be a plain user name other than root, not $U" ;; esac
  if [ "$(id -u)" = 0 ]; then ROOT=""; else command -v sudo >/dev/null 2>&1 || die "this needs root"; ROOT="sudo"; fi
  if ! id "$U" >/dev/null 2>&1; then
    [ ! -e "$HOME/hive/.hive-account" ] || die "no user $U, but there's a Hive in $HOME/hive. To remove that, add: sh -s -- --this-account"
    die "no user $U, so nothing to remove"
  fi
  H=$(getent passwd "$U" | cut -d: -f6)
  $ROOT test -e "$H/.hive-installed" || die "$U doesn't have a Hive installed by this package, so I won't touch it"

  case "${HOME%/}/" in "${H%/}"/*) die "run this from your own account, not from $U's" ;; esac
  [ -r /dev/tty ] || die "this needs a terminal to ask you"
  choose_keep
  K=""; [ -z "$KEEP" ] || K=$(kept_dir "$HOME")
  echo "This deletes the user $U and $H, with its Claude sign-in. Kept: $(keep_words)${K:+, moved to $K}."
  printf "Type delete to go ahead: " >/dev/tty
  read -r answer </dev/tty
  [ "$answer" = delete ] || die "nothing changed"

  $ROOT loginctl disable-linger "$U" 2>/dev/null || true
  $ROOT loginctl terminate-user "$U" 2>/dev/null || true
  $ROOT pkill -KILL -u "$U" 2>/dev/null || true
  sleep 1
  if [ -n "$K" ]; then
    mkdir -p "$K"
    for p in $(keep_paths); do ! $ROOT test -e "$H/$p" || $ROOT mv "$H/$p" "$K/" || die "couldn't move $H/$p to $K. The Hive is stopped; nothing more was deleted"; done
    $ROOT chown -R "$(id -u):$(id -g)" "$K"
  fi
  $ROOT userdel -r "$U" 2>/dev/null || die "couldn't delete $U; check for its processes and run: sudo userdel -r $U"
  launcher_gone
  echo "Removed."
  [ -z "$K" ] || echo "Kept in $K: $(keep_words)."
  bwrap_note
}

main "$@"
