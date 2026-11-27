#!/bin/sh
# Uninstall the Hive from a Mac. One line:
#   curl -fsSL https://hivelab.ca/uninstall-mac.sh | sh
# Deletes one Hive (the hive user in the VM, or HIVE_USER=name for another) after you type delete. Its last Hive takes the VM and ~/.hive with it. Its own Lima only: a Lima of yours, in ~/.lima, isn't touched.
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
commas() { echo "$*" | sed 's/ /, /g'; }
vm_up() { [ -x "$L" ] && { [ "$("$L" list hive --format '{{.Status}}' 2>/dev/null)" = Running ] || "$L" start --tty=false hive >/dev/null 2>&1; }; }
# The Hives in the VM: its users with an installed Hive. If the VM can't answer, the commands the installer made here (an old one without HU= is hive's).
hives() {
  # shellcheck disable=SC2016  # these expand in the VM
  h=$(vm_up && "$L" shell --workdir / hive sudo sh -c 'getent passwd | while IFS=: read -r u _ _ _ _ h _; do [ ! -e "$h/.hive-installed" ] || echo "$u"; done' </dev/null 2>/dev/null | tr -d '\r' | grep -E '^[a-z0-9_][a-z0-9_-]*$' || true)
  [ -n "$h" ] || h=$(for f in "$D/bin/hive" "$D"/bin/hive-*; do [ -f "$f" ] || continue; u=$(sed -n 's/^HU=//p' "$f"); echo "${u:-hive}"; done)
  echo $h | tr ' ' '\n' | sort -u | tr '\n' ' ' | sed 's/ $//'
}
kept_dir() { b="$1/hive-kept-$(date +%Y%m%d)"; k="$b"; n=2; while [ -e "$k" ]; do k="$b-$n"; n=$((n + 1)); done; echo "$k"; }

main() {
  [ "$(uname -s)" = Darwin ] || die "this is the Mac uninstaller. On Linux: curl -fsSL https://hivelab.ca/uninstall.sh | sh"
  [ "$(id -u)" != 0 ] || die "run this as yourself, not with sudo"
  U="${HIVE_USER:-hive}"
  case "$U" in "" | -* | root | *[!a-z0-9_-]*) die "HIVE_USER must be a plain user name in small letters, other than root, not $U. Nothing changed" ;; esac
  case "$U" in hive | hive-*) CMD=$U ;; *) CMD=hive-$U ;; esac
  D="$HOME/.hive"
  [ -e "$D/.hive-mac" ] || die "there's no Hive from the Mac installer here, so nothing changed"
  L="$D/lima-dist/bin/limactl"
  export LIMA_HOME="$D/lima"

  ( : </dev/tty ) 2>/dev/null || die "this needs a terminal to ask you"
  HIVES=$(hives)
  if [ -n "$HIVES" ]; then
    case " $HIVES " in *" $U "*) ;; *) die "there's no Hive $U in the VM (it holds $(commas $HIVES)), so nothing changed. HIVE_USER picks which one" ;; esac
  fi
  OTHERS=$(for h in $HIVES; do [ "$h" = "$U" ] || printf '%s ' "$h"; done | sed 's/ $//')
  ALL=${HIVES:-$U}; n=$(echo $ALL | wc -w | tr -d ' ')
  if [ "$n" = 1 ]; then echo "The VM holds 1 Hive: $ALL."; else echo "The VM holds $n Hives: $(commas $ALL)."; fi
  choose_keep
  K=""; [ -z "$KEEP" ] || K=$(kept_dir "$HOME")
  if [ -n "$OTHERS" ]; then
    echo "This deletes the Hive $U in the VM, with its Claude sign-in, and ends its agents. The VM and $(commas $OTHERS) stay. Kept: $(keep_words)${K:+, copied to $K}. Your Mac's own files stay."
  else
    echo "This deletes the Hive's Linux VM and everything else in it, with its Claude sign-in, and ends its agents. Kept: $(keep_words)${K:+, copied to $K}. Your Mac's own files stay."
  fi
  printf "Type delete to go ahead: " >/dev/tty
  read -r answer </dev/tty
  [ "$answer" = delete ] || die "nothing changed"

  # Copy out what stays while the VM still runs. If that fails, nothing is deleted.
  if [ -n "$K" ]; then
    [ -x "$L" ] || die "can't reach the VM to copy out $(keep_words), so nothing changed"
    vm_up || die "couldn't start the VM to copy out $(keep_words), so nothing changed"
    mkdir -p "$K"
    # shellcheck disable=SC2016  # these expand in the VM
    "$L" shell --workdir / hive sudo -u "$U" -H bash -lc 'cd; set --; for p in '"$(keep_paths | tr '\n' ' ')"'; do [ -e "$p" ] && set -- "$@" "$p"; done; [ $# = 0 ] || tar -cf - "$@"' </dev/null > "$K/.copy.tar" \
      && { [ ! -s "$K/.copy.tar" ] || tar -xf "$K/.copy.tar" -C "$K"; } \
      || die "couldn't copy $(keep_words) out of the VM, so nothing was deleted. What came across so far is in $K"
    rm -f "$K/.copy.tar"
  fi

  # Others stay: end this one's processes, delete its user and home, and take off its own command (and, for hive, its app).
  if [ -n "$OTHERS" ]; then
    # shellcheck disable=SC2016  # these expand in the VM
    { vm_up && "$L" shell --workdir / hive sudo sh -c 'u=$1; loginctl disable-linger "$u" 2>/dev/null; loginctl terminate-user "$u" 2>/dev/null; pkill -KILL -u "$u" 2>/dev/null; sleep 1; userdel -r "$u" 2>/dev/null; sed -i "/^$u /d" /var/lib/hive/ports 2>/dev/null; ! id "$u" >/dev/null 2>&1' sh "$U" </dev/null; } \
      || die "couldn't delete the Hive $U in the VM. Its processes may still run; try again in a minute"
    rm -f "$D/bin/$CMD"
    if [ "$U" = hive ] && grep -qs 'made by the Hive install' "$HOME/Applications/Hive.app/Contents/MacOS/Hive"; then rm -rf "$HOME/Applications/Hive.app"; fi
    echo "Removed the Hive $U. The VM stays, with $(commas $OTHERS)."
    [ -z "$K" ] || echo "Kept in $K: $(keep_words)."
    return 0
  fi
  launchctl bootout "gui/$(id -u)/$AGENT" 2>/dev/null || true
  rm -f "$HOME/Library/LaunchAgents/$AGENT.plist"
  if [ -x "$L" ]; then "$L" delete --force hive 2>/dev/null || true; fi
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
