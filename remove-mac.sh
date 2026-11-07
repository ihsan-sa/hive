#!/bin/sh
# Uninstall the Hive from a Mac. One line:
#   curl -fsSL <THIS-SITE>/uninstall-mac.sh | sh
# Deletes its VM and ~/.hive after you type delete. Its own Lima only: a Lima of yours, in ~/.lima, isn't touched.
# Your projects, notes and board stay unless you say to delete them: they're copied out of the VM to ~/hive-kept-DATE first. HIVE_KEEP=projects,notes,board (any of them, or none) answers without asking.
set -eu

AGENT=hive.vm
MARK='# added by the Hive installer'

die() { echo "hive: $*" >&2; exit 1; }

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

main() {
  [ "$(uname -s)" = Darwin ] || die "this is the Mac uninstaller. On Linux: curl -fsSL <THIS-SITE>/uninstall.sh | sh"
  [ "$(id -u)" != 0 ] || die "run this as yourself, not with sudo"
  D="$HOME/.hive"
  [ -e "$D/.hive-mac" ] || die "there's no Hive from the Mac installer here, so nothing changed"

  ( : </dev/tty ) 2>/dev/null || die "this needs a terminal to ask you"
  choose_keep
  K=""; [ -z "$KEEP" ] || K=$(kept_dir "$HOME")
  echo "This deletes the Hive's Linux VM and everything else in it, with its Claude sign-in, and ends its agents. Kept: $(keep_words)${K:+, copied to $K}. Your Mac's own files stay."
  printf "Type delete to go ahead: " >/dev/tty
  read -r answer </dev/tty
  [ "$answer" = delete ] || die "nothing changed"

  # Copy out what stays while the VM still runs. If that fails, nothing is deleted.
  if [ -n "$K" ]; then
    L="$D/lima-dist/bin/limactl"
    export LIMA_HOME="$D/lima"
    [ -x "$L" ] || die "can't reach the VM to copy out $(keep_words), so nothing changed"
    [ "$("$L" list hive --format '{{.Status}}' 2>/dev/null)" = Running ] || "$L" start --tty=false hive >/dev/null 2>&1 || die "couldn't start the VM to copy out $(keep_words), so nothing changed"
    mkdir -p "$K"
    # shellcheck disable=SC2016  # these expand in the VM
    "$L" shell --workdir / hive sudo -u hive -H bash -lc 'cd; set --; for p in '"$(keep_paths | tr '\n' ' ')"'; do [ -e "$p" ] && set -- "$@" "$p"; done; [ $# = 0 ] || tar -cf - "$@"' </dev/null > "$K/.copy.tar" \
      && { [ ! -s "$K/.copy.tar" ] || tar -xf "$K/.copy.tar" -C "$K"; } \
      || die "couldn't copy $(keep_words) out of the VM, so nothing was deleted. What came across so far is in $K"
    rm -f "$K/.copy.tar"
  fi

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
  [ -z "$K" ] || echo "Kept in $K: $(keep_words)."
  echo "Lima's download cache stays in ~/Library/Caches/lima (about 600 MB), as a Lima of yours may use it. If you don't use Lima, you can delete that folder."
}

main "$@"
