#!/bin/sh
# Install the Hive, or update it, on Linux. One line:
#   curl -fsSL https://hive.ihsan.cc/install.sh | sh
# Makes a Linux user (hive, or HIVE_USER=name) so agents get their own account, and runs install.sh as root.
# With no root, or by choice, in a folder of your own account, as you:
#   curl -fsSL https://hive.ihsan.cc/install.sh | sh -s -- --this-account
# HIVE_DIR=/path picks the folder (default ~/hive). HIVE_ACCEPT=1 accepts the warning when there's no terminal to ask on.
# Everything is inside main, so a download cut short runs nothing, and nothing below reads the rest of this script from stdin.
set -eu

die() { tick_stop; line_end; echo "hive: $*" >&2; exit 1; }

# What you see: one numbered line per step, a dot every few seconds while it works. What the commands print goes to fd 3, a log, and it's shown when something fails.
# HIVE_INSIDE=1: another installer is around this one (Windows, Mac) and has its own steps and ending, so this prints plain lines, and NOTE: on what it should show.
OPEN=0; HELD=""; TICK=""; STEP=0; STEPS=0; LOG=""; INSIDE=${HIVE_INSIDE:-}; OLD_SAID=""
tick_stop() { [ -z "$TICK" ] || { kill "$TICK" 2>/dev/null; wait "$TICK" 2>/dev/null || true; TICK=""; }; }
line_end() {
  [ "$OPEN" = 0 ] || { echo; OPEN=0; }
  [ -z "$HELD" ] || { printf '%s\n' "$HELD"; HELD=; }
}
# Each non-empty argument is a step this run will take. The log starts here, so what stops before anything changes doesn't wipe the last run's.
begin() {
  for c in "$@"; do [ -z "$c" ] || STEPS=$((STEPS + 1)); done
  if [ -n "$INSIDE" ]; then exec 3>&1
  else
    LOG="${HIVE_LOG:-${HOME:-/tmp}/.hive-install.log}"
    (umask 077; : >"$LOG") 2>/dev/null || LOG=$(mktemp)
    exec 3>>"$LOG"
  fi
  trap on_exit EXIT; trap 'exit 130' INT TERM
}
step() {
  tick_stop; line_end; STEP=$((STEP + 1))
  if [ -n "$INSIDE" ]; then echo "$1"; return 0; fi
  printf '[%s/%s] %s ' "$STEP" "$STEPS" "$1"; OPEN=1
  ( while sleep 5 >/dev/null 2>&1; do kill -0 $$ 2>/dev/null || exit; printf .; done ) </dev/null 3>&- &
  TICK=$!
}
# A line that has to be seen. In the middle of a step it waits under that step's line, so the dots stay on one line.
note() {
  if [ -n "$INSIDE" ]; then echo "NOTE: $*"; return 0; fi
  if [ "$OPEN" = 0 ]; then echo "$*"; else HELD="${HELD:+$HELD
}$*"; fi
}
# One command into the log, and its NOTE: lines onto the screen. Only for a plain command or in_hive, and with nothing after it that hides its status.
quietly() {
  from=1; [ -z "$LOG" ] || from=$(($(wc -l <"$LOG") + 1))
  rc=0; "$@" >&3 2>&3 </dev/null || rc=$?
  [ -z "$LOG" ] || while IFS= read -r l; do [ -z "$l" ] || note "$l"; done <<EOF
$(sed -n "$from,\$s/^NOTE: //p" "$LOG")
EOF
  return $rc
}
on_exit() {
  rc=$?; trap - EXIT; set +e
  tick_stop; line_end
  if [ "$rc" != 0 ] && [ -s "$LOG" ]; then
    { echo "The last lines of what it printed:"; tail -n 15 "$LOG"; echo "All of it: $LOG"; } >&2
  fi
  old_exit_note
  [ -z "${T:-}" ] || rm -rf "$T"
  exit "$rc"
}
# A command as the new Hive's account, in its own home.
in_hive() {
  if [ "$OWN" = 1 ]; then env -u XDG_CONFIG_HOME -u XDG_DATA_HOME -u XDG_CACHE_HOME -u XDG_STATE_HOME -u CLAUDE_CONFIG_DIR HOME="$H" PATH="$H/.local/bin:$PATH" XDG_RUNTIME_DIR="${XDG_RUNTIME_DIR:-/run/user/$(id -u)}" sh -c "$1"
  else $ROOT su - "$U" -c "$1"; fi
}
# The one ending. The address is the key to the hive, so it goes on the screen and never in the log. $2 is set on a new install.
ending() {
  tick_stop; line_end
  said=$OLD_SAID
  if [ -n "$OLD_DONE" ]; then said="${said:+$said
}What came and what didn't: kb/old-hive/README.md in the new Hive.
Your old folder is still at $OLD_ROOT. Delete it when you're happy."; fi
  if [ -n "${2:-}" ]; then
    sb=$(in_hive '[ ! -e "$HOME/settings.json" ] || hive sandbox check' </dev/null 2>&1 || true)
    [ -z "$sb" ] || said="${said:+$said
}$sb"
  fi
  if [ -n "$INSIDE" ]; then [ -z "$said" ] || printf '%s\n' "$said" | sed 's/^/NOTE: /'; return 0; fi
  [ -z "$said" ] || printf '%s\n' "$said"
  if [ "$OWN" = 1 ]; then web="hive web"; term="hive"; else web="sudo -iu $U hive web"; term="sudo -iu $U hive"; fi
  addr=$(in_hive 'hive web 2>/dev/null' </dev/null | sed -n '/^http/{p;q;}')
  echo; echo "$1 Open this address in a browser on this machine, and it takes you from there:"
  [ -n "$addr" ] || addr="(it didn't print just now: wait a minute, then run: $web)"
  echo "  $addr"
  echo "It's the key to this hive, so keep it to yourself. To see it again: $web"
  echo "On another machine: ssh -L PORT:127.0.0.1:PORT this-machine, with the port from that address, then open it there."
  echo "If you'd rather stay in a terminal: $term"
}

deps() {
  missing=""
  for t in git python3 curl; do command -v "$t" >/dev/null 2>&1 || missing="$missing $t"; done
  [ -z "$missing" ] && return 0
  if command -v apt-get >/dev/null 2>&1; then
    $ROOT env DEBIAN_FRONTEND=noninteractive apt-get update -qq </dev/null >&3 2>&3 && $ROOT env DEBIAN_FRONTEND=noninteractive apt-get install -y -qq $missing </dev/null >&3 2>&3
  elif command -v dnf >/dev/null 2>&1; then $ROOT dnf install -y -q $missing </dev/null >&3 2>&3
  elif command -v pacman >/dev/null 2>&1; then $ROOT pacman -S --noconfirm --needed $(echo "$missing" | sed 's/python3/python/') </dev/null >&3 2>&3
  elif command -v zypper >/dev/null 2>&1; then $ROOT zypper -n install $missing </dev/null >&3 2>&3
  else die "install$missing with your package manager, then run this again"
  fi
}

# The sandbox around agents needs bubblewrap, and root is only here once. Not fatal: the app says what's missing and offers the choice.
sandbox_dep() {
  command -v bwrap >/dev/null 2>&1 && return 0
  echo "Installing bubblewrap (the sandbox around agents)" >&3
  if command -v apt-get >/dev/null 2>&1; then
    $ROOT env DEBIAN_FRONTEND=noninteractive apt-get -o DPkg::Lock::Timeout=120 update -qq </dev/null >&3 2>&3 && $ROOT env DEBIAN_FRONTEND=noninteractive apt-get -o DPkg::Lock::Timeout=120 install -y -qq bubblewrap </dev/null >&3 2>&3
  elif command -v dnf >/dev/null 2>&1; then $ROOT dnf install -y -q bubblewrap </dev/null >&3 2>&3
  elif command -v pacman >/dev/null 2>&1; then $ROOT pacman -S --noconfirm --needed bubblewrap </dev/null >&3 2>&3
  elif command -v zypper >/dev/null 2>&1; then $ROOT zypper -n install bubblewrap </dev/null >&3 2>&3
  else false
  fi || note "Couldn't install bubblewrap, so the sandbox won't work until it's there. The app will say so."
}

# Ubuntu 24.04 stops bubblewrap making a user namespace unless a profile allows it (newer Ubuntu ships one). This is the shape of newer Ubuntu's bwrap-userns-restrict: bubblewrap may make its namespace, and what runs inside has no capabilities.
# The same bytes as remove.sh, get-mac.sh and sandbox.py's _userns_fix(): remove.sh knows the file is ours by matching them, and test_get.sh holds them equal.
AA_TEXT='# installed by the Hive\nabi <abi/4.0>,\ninclude <tunables/global>\nprofile bwrap /usr/bin/bwrap flags=(attach_disconnected,mediate_deleted) {\n  allow capability,\n  allow file rwlkm /{**,},\n  allow network,\n  allow unix,\n  allow ptrace,\n  allow signal,\n  allow mqueue,\n  allow io_uring,\n  allow userns,\n  allow mount,\n  allow umount,\n  allow pivot_root,\n  allow dbus,\n  allow pix /** -> &bwrap//&unpriv_bwrap,\n}\nprofile unpriv_bwrap flags=(attach_disconnected,mediate_deleted) {\n  allow file rwlkm /{**,},\n  allow network,\n  allow unix,\n  allow ptrace,\n  allow signal,\n  allow mqueue,\n  allow io_uring,\n  allow userns,\n  allow mount,\n  allow umount,\n  allow pivot_root,\n  allow dbus,\n  allow pix /** -> &unpriv_bwrap,\n  audit deny capability,\n}\n'
bwrap_blocked() {
  [ "$(cat "${HIVE_USERNS_LIMIT:-/proc/sys/kernel/apparmor_restrict_unprivileged_userns}" 2>/dev/null)" = 1 ] || return 1
  ! grep -qs '^ *profile .*/usr/bin/bwrap' "${HIVE_AA_DIR:-/etc/apparmor.d}"/*
}
bwrap_profile() {
  command -v bwrap >/dev/null 2>&1 && bwrap_blocked || return 0
  d=${HIVE_AA_DIR:-/etc/apparmor.d}
  note "Letting bubblewrap make its private space ($d/bwrap): this re-allows unprivileged user namespaces for bubblewrap, a limit Ubuntu turns on by default, for every user of this machine, and that widens the kernel's attack surface. It stays if you remove the Hive, which then prints how to take it away."
  printf "$AA_TEXT" | $ROOT tee "$d/bwrap" >/dev/null 2>&3 && $ROOT apparmor_parser -r "$d/bwrap" </dev/null >&3 2>&3 \
    || note "Couldn't load the bubblewrap profile, so the sandbox may not work until it's there. The app will say so."
}

clone() {
  [ -z "${T:-}" ] || return 0
  T=$(mktemp -d)
  step "Getting the Hive"
  git clone -q --single-branch -b "$BRANCH" "$REPO" "$T/hive" </dev/null >&3 2>&3
  # A release is a tag, and only a tag has been through the tests. HIVE_BRANCH asks for that branch as it stands.
  if [ -z "${HIVE_BRANCH:-}" ]; then
    TAG=$(git -C "$T/hive" tag -l 'v2.*' | grep -E '^v2\.[0-9]+\.[0-9]+$' | sort -V | tail -n 1 || true)
    [ -z "$TAG" ] || git -C "$T/hive" checkout -q -B "$BRANCH" "$TAG" >&3 2>&3
  fi
  # Kept apart, because the clone itself gets moved.
  [ ! -e "$T/hive/import-old.py" ] || cp "$T/hive/import-old.py" "$T/import-old.py"
}

# The old Hive (v0.x), a Go program with its own folder. It's removed first, then the new one goes in, then its things are brought across.
# Its folder is the backup: it's only ever renamed, never changed apart from its state file, .hive/v2-import ("pending", then "done"), which is how a rerun carries on.
# Anything in that account could have been written by whatever ran there, so root never reads or writes in it: as_old does, and only exact expected values are taken from what comes back.
OLD_A=""; OLD_HM=""; OLD_ROOT=""; OLD_STATE=""; OLD_BINS=""; OLD_UNIT=""; OLD_SYS=""; OLD_HALF=0; OLD_DONE=""
OLD_PROBE='
for b in hive hive-v0; do f=$hm/.local/bin/$b
  if [ -f "$f" ] && [ ! -L "$f" ] && grep -aqs hive-standalone-build "$f"; then echo "bin $f"; fi
done
[ ! -e "$hm/.config/systemd/user/hive.service" ] || echo "unit $hm/.config/systemd/user/hive.service"
p=""; [ ! -f "$hm/.config/hive/workspace" ] || p=$(cat "$hm/.config/hive/workspace" 2>/dev/null || true); p=${p%/}
for r in "$p" "$hm/hive" "$hm"/hive-old-*; do
  [ -n "$r" ] && [ -e "$r/.hive" ] || continue
  # The new Hive in a folder has a .hive too.
  [ ! -e "$r/.hive-account" ] && [ ! -e "$r/.hive-installed" ] || continue
  [ -e "$r/.hive/events.jsonl" ] || [ -e "$r/.hive/config.json" ] || continue
  s=new; [ ! -f "$r/.hive/v2-import" ] || s=$(cat "$r/.hive/v2-import" 2>/dev/null || echo new)
  case "$s" in done) continue;; pending) ;; *) s=new;; esac
  echo "root $s $r"
done
'

home_of() { if [ "$1" = "$(id -un)" ] && [ -n "${HOME:-}" ] && [ "${HIVE_OWN:-0}" = 1 ]; then echo "$HOME"; else getent passwd "$1" | cut -d: -f6; fi; }
q() { printf "'%s'" "$(printf %s "$1" | sed "s/'/'\\\\''/g")"; }
# A command as that account. Not through su when it's already us.
as_user() { if [ "$1" = "$(id -un)" ]; then sh -c "$2"; else $ROOT su - "$1" -c "$2"; fi; }
# A command on the old Hive's files, as its account. No login shell, so nothing of that account's runs and its stdout is only what the command says (a tar stream, for one), and it needn't have a shell of its own. None of root's environment.
as_old() { if [ "$1" = "$(id -un)" ]; then sh -c "$2" </dev/null; else $ROOT su -s /bin/sh "$1" -c "cd / && exec env -i PATH=\"\$PATH\" sh -c $(q "$2")" </dev/null; fi; }
# A folder as it came back from that account: one absolute path with nothing odd in it.
old_path_ok() { case "$1" in /*) ;; *) return 1;; esac; case "$1" in *[[:cntrl:]]* | */../* | */..) return 1;; esac; }
old_first_root() { printf '%s\n' "$1" | sed -n "s/^root $2 //p" | while IFS= read -r x; do if old_path_ok "$x"; then printf '%s\n' "$x"; break; fi; done; }
old_said() { printf '%s\n' "$1" | grep -Fxq -- "$2"; }

# Sets OLD_* for the first of these accounts with anything of the old Hive's. Another's is only mentioned: it's somebody else's.
old_scan() {
  for a in "$@"; do
    [ -n "$a" ] && id "$a" >/dev/null 2>&1 || continue
    hm=$(home_of "$a"); [ -n "$hm" ] || continue
    found=$(as_old "$a" "hm=$(q "$hm"); $OLD_PROBE" 2>/dev/null) || found=""
    # This one is root's own file, so root reads it.
    sys="${HIVE_ETC:-/etc}/systemd/system/hive-$a.service"; $ROOT grep -qs '/\.local/bin/.* daemon' "$sys" || sys=""
    [ -n "$found$sys" ] || continue
    if [ -n "$OLD_A" ]; then [ "$a" = "$OLD_A" ] || echo "NOTE: $a also has an old Hive. I'm leaving that one alone. To bring it across instead, run this with HIVE_OLD_USER=$a."; continue; fi
    OLD_A=$a; OLD_HM=$hm; OLD_SYS=$sys
    for b in hive hive-v0; do
      ! old_said "$found" "bin $hm/.local/bin/$b" || OLD_BINS="$OLD_BINS${OLD_BINS:+
}$hm/.local/bin/$b"
    done
    ! old_said "$found" "unit $hm/.config/systemd/user/hive.service" || OLD_UNIT="$hm/.config/systemd/user/hive.service"
    r=$(old_first_root "$found" pending); OLD_STATE=pending
    [ -n "$r" ] || { r=$(old_first_root "$found" new); OLD_STATE=new; }
    OLD_ROOT=$r
  done
  for f in "${HIVE_ETC:-/etc}"/systemd/system/hive-*.service; do
    $ROOT grep -qs '/\.local/bin/.* daemon' "$f" || continue
    n=${f##*/hive-}; n=${n%.service}
    [ "$n" = "$OLD_A" ] || echo "NOTE: $f is an old Hive of $n's. It's left alone and keeps running."
  done
}

# Everything that can stop this is checked before anything changes. $1 is the account the new Hive goes in, $2 its folder.
old_preflight() {
  [ -n "$OLD_A" ] || return 0
  if [ -n "$OLD_SYS" ] && [ -z "$ROOT" ] && [ "$(id -u)" != 0 ]; then
    die "the old Hive runs as a system service, and this line doesn't use sudo. Stop it first, then run this again:
  sudo systemctl disable --now ${OLD_SYS##*/}
  sudo rm $OLD_SYS"
  fi
  OLD_RENAME=""
  case "${OLD_ROOT##*/}" in
    "" | hive-old-*) ;;
    *) # Its name is only needed when the new Hive goes there, or the new Hive's own account won't have anything else in it.
      if [ "$2" = "$OLD_ROOT" ]; then OLD_RENAME=1
      elif [ "$1" = "$OLD_A" ] && [ "$2" = "$OLD_HM" ] && [ "${OLD_ROOT%/*}" = "$OLD_HM" ]; then OLD_RENAME=1; fi ;;
  esac
  # The new Hive refuses an account with other things in it, because its agents would have full rights to them.
  if [ "$1" = "$OLD_A" ] && [ "$2" = "$OLD_HM" ]; then
    skip=""; [ -z "$OLD_RENAME" ] || skip=${OLD_ROOT##*/}
    other=$(as_old "$OLD_A" "find $(q "$OLD_HM") -mindepth 1 -maxdepth 1 ! -name '.*' ! -name core ! -name kb ! -name projects ! -name run ! -name sessions ! -name board.jsonl ! -name log.jsonl ! -name modules ! -name files ! -name settings.json ! -name 'hive-old-*' ! -name $(q "${skip:-.}") | head -n 3")
    if [ -n "$other" ] || as_old "$OLD_A" "test -e $(q "$OLD_HM/.ssh")"; then
      die "the old Hive is in the account $OLD_A, which also has ~/.ssh or other files, and the new Hive's agents would have full rights to them. Nothing changed. Put the new Hive in its own user and bring the old one's things across:
  HIVE_USER=newhive HIVE_OLD_USER=$OLD_A  in front of the sh of the same line"
    fi
  fi
  [ -n "$OLD_ROOT" ] || return 0
  # What only root could read (made by a container, say) would stop the copy after the old Hive is off, so it's found now.
  unread=$(as_old "$OLD_A" "cd $(q "$OLD_ROOT") && find projects .hive/kb .hive/chats .hive/config.json -path 'projects/*/.checkouts' -prune -o ! -type l \\( ! -readable -o -type d ! -executable \\) -print 2>/dev/null | head -n 3" | tr -cd '[:print:]\n')
  [ -z "$unread" ] || die "the account $OLD_A can't read some of the old Hive's files, so they can't be brought across. Nothing changed. Give them to that account, then run this again. In $OLD_ROOT:
$unread"
  OLD_KB=$(as_old "$OLD_A" "cd $(q "$OLD_ROOT") && du -sk --exclude=.checkouts projects .hive/kb .hive/chats 2>/dev/null" | awk '{n+=$1} END {print n+0}')
  OLD_N=$(as_old "$OLD_A" "cd $(q "$OLD_ROOT/projects") 2>/dev/null && ls -1 | wc -l || echo 0")
  d=$2; while [ ! -d "$d" ]; do d=${d%/*}; [ -n "$d" ] || d=/; done
  free=$(df -Pk "$d" | awk 'NR==2 {print $4}')
  [ "$((OLD_KB + OLD_KB / 10 + 1048576))" -lt "$free" ] || die "the old Hive's things take $((OLD_KB / 1024)) MB, and they're copied, so the old folder stays as the backup. $d has $((free / 1024)) MB free, and that's too tight. Nothing changed."
}

old_report() {
  [ -n "$OLD_A" ] || return 0
  [ "$OLD_STATE" != pending ] || { echo "Carrying on with the old Hive from the last run. Its folder is at $OLD_ROOT."; return 0; }
  echo; echo "Found an old Hive (v0.x) in the account $OLD_A:"
  printf '%s\n' "$OLD_BINS" | sed -n 's/^./  program: &/p'
  [ -z "$OLD_UNIT$OLD_SYS" ] || echo "  service: ${OLD_UNIT:-$OLD_SYS}"
  [ -z "$OLD_ROOT" ] || echo "  folder: $OLD_ROOT ($OLD_N projects, $((OLD_KB / 1024)) MB to bring)"
  echo; echo "It will be stopped and removed first, so both don't run side by side. Agents in the middle of a turn may finish that turn."
  if [ -n "$OLD_ROOT" ]; then
    echo "Then these are copied into the new Hive: projects with their git history, the knowledge base, goals, and chats as pages to read."
    echo "They don't come: sign-ins, tokens and keys (you sign in again), past runs, task lists and schedules, and agents' checkouts."
    if [ -n "$OLD_RENAME" ]; then echo "The old folder is your backup. It's renamed to ${OLD_ROOT%/*}/hive-old-$(date +%Y%m%d), and nothing is deleted from it. Delete it when you're happy."
    else echo "The old folder is your backup and stays where it is. Delete it when you're happy."; fi
  fi
  $ROOT test ! -d "${HIVE_ETC:-/etc}/hive/$OLD_A" || echo "${HIVE_ETC:-/etc}/hive/$OLD_A holds the old Hive's tokens, readable only by root. It stays. Delete it when you're happy."
  echo
}

ask_yes() {
  [ "${HIVE_ACCEPT:-}" != 1 ] || return 0
  ( : </dev/tty ) 2>/dev/null || die "there's no terminal to ask you on, and nothing changed. If you accept the above, set HIVE_ACCEPT=1"
  printf "Type yes to %s: " "$1" >/dev/tty
  read -r answer </dev/tty
  [ "$answer" = yes ] || die "nothing changed"
}

# The state file is replaced rather than written through, so a link there points nowhere new, and one left by root's write in an older installer still goes.
old_state() { as_old "$OLD_A" "f=$(q "$OLD_ROOT/.hive/v2-import"); rm -f \"\$f\" && printf %s $1 > \"\$f\""; }

# Stop it and take it off. Run twice, it does nothing new.
old_remove() {
  [ -n "$OLD_A" ] || return 0
  [ -e "$T/import-old.py" ] || die "this Hive's release can't bring an old one across yet. Nothing changed. Your old Hive is still running"
  OLD_HALF=1
  [ -z "$OLD_ROOT" ] || [ "$OLD_STATE" = pending ] || { old_state "pending"; OLD_STATE=pending; }
  step "Stopping the old Hive"
  if [ -n "$OLD_SYS" ]; then
    $ROOT systemctl disable --now "${OLD_SYS##*/}" </dev/null >/dev/null 2>&1 || true
    $ROOT rm -f "$OLD_SYS"
    $ROOT systemctl daemon-reload </dev/null >/dev/null 2>&1 || true
  fi
  printf '%s\n' "$OLD_BINS" | while IFS= read -r b; do [ -z "$b" ] || as_user "$OLD_A" "timeout 60 $(q "$b") uninstall" </dev/null >/dev/null 2>&1 || true; done
  # Whatever its uninstall didn't get to, or a program too old or broken to run it.
  as_user "$OLD_A" 'XDG_RUNTIME_DIR=${XDG_RUNTIME_DIR:-/run/user/$(id -u)} systemctl --user disable --now hive.service; pkill -u "$(id -u)" -f "/\.local/bin/hive(-v0)? daemon"; true' </dev/null >/dev/null 2>&1 || true
  printf '%s\n' "$OLD_BINS" | while IFS= read -r b; do [ -z "$b" ] || as_old "$OLD_A" "rm -f $(q "$b")"; done
  [ -z "$OLD_UNIT" ] || as_old "$OLD_A" "rm -f $(q "$OLD_UNIT")"
  OLD_BINS=""; OLD_UNIT=""; OLD_SYS=""
  if [ -n "$OLD_ROOT" ] && [ -n "$OLD_RENAME" ]; then
    to="${OLD_ROOT%/*}/hive-old-$(date +%Y%m%d)"; i=1
    while as_old "$OLD_A" "test -e $(q "$to")"; do i=$((i + 1)); to="${OLD_ROOT%/*}/hive-old-$(date +%Y%m%d)-$i"; done
    as_old "$OLD_A" "mv $(q "$OLD_ROOT") $(q "$to")"
    OLD_ROOT=$to
    note "The old folder is now $to"
  fi
  # Its uninstall forgets where the folder is, and a rerun after a failure has to find it. It goes when the import is done.
  [ -z "$OLD_ROOT" ] || as_old "$OLD_A" "mkdir -p $(q "$OLD_HM/.config/hive") && printf '%s\\n' $(q "$OLD_ROOT") > $(q "$OLD_HM/.config/hive/workspace")"
}

# Copy across and sort out. $1 is the account the new Hive is in, $2 its home. Stays out of the old folder apart from the state file.
old_import() {
  [ -n "$OLD_ROOT" ] || { OLD_HALF=0; return 0; }
  S="$2/.old-hive-incoming"; rc="$T/old-tar-rc"
  step "Bringing your old Hive's things across"
  $ROOT true </dev/null
  { r=0; as_old "$OLD_A" "cd $(q "$OLD_ROOT") && tar c -T /dev/null --exclude='projects/*/.checkouts' \$(for p in projects .hive/kb .hive/chats .hive/config.json; do [ ! -e \"\$p\" ] || echo \"\$p\"; done) 2>/dev/null" || r=$?; echo "$r" > "$rc"; } \
    | as_user "$1" "chmod -R u+w $(q "$S") 2>/dev/null; rm -rf $(q "$S"); mkdir -p $(q "$S") && tar x --no-same-owner -f - -C $(q "$S")" 2>&3 \
    || die "couldn't copy the old Hive's things into $S"
  # 1 is a file that changed while it was being read: an agent finishing its turn. Anything above that is a real failure.
  [ "$(cat "$rc")" -le 1 ] || die "couldn't read all of the old Hive's things from $OLD_ROOT"
  as_old "$OLD_A" "f=$(q "$OLD_ROOT/.hive/events.jsonl"); [ ! -f \"\$f\" ] || grep -hs '\"kind\":\"project\.' \"\$f\" || true" | as_user "$1" "cat > $(q "$S/projects.jsonl")"
  if [ "$1" = "$(id -un)" ]; then OLD_SAID=$(env -u XDG_CONFIG_HOME -u XDG_DATA_HOME -u XDG_CACHE_HOME -u XDG_STATE_HOME HOME="$2" python3 -I - "$S" "$OLD_ROOT" < "$T/import-old.py" 2>&3)
  else OLD_SAID=$($ROOT su - "$1" -c "python3 -I - $(q "$S") $(q "$OLD_ROOT")" < "$T/import-old.py" 2>&3); fi
  old_state "done"
  as_old "$OLD_A" "rm -f $(q "$OLD_HM/.config/hive/workspace")"
  OLD_HALF=0; OLD_DONE=1
}

old_exit_note() { [ "$OLD_HALF" != 1 ] || echo "The old Hive is stopped${OLD_ROOT:+, and its folder is whole at $OLD_ROOT}. Run the same line again to carry on." >&2; }

# The Hive in a folder of this account, as this user. No sudo. It's the same user, so the warning has to be plain.
this_account() {
  [ "$(id -u)" != 0 ] || die "--this-account is for your own account, and this is root. Log in as yourself, or use the line without it to make a separate user"
  H="${HIVE_DIR:-$HOME/hive}"; H="${H%/}"
  case "$H" in /*) ;; *) die "HIVE_DIR must be a full path, not $H" ;; esac
  case "$H" in *[!A-Za-z0-9_./-]*) die "$H has a space or a character the service files can't hold. Pick another folder: HIVE_DIR=/path" ;; esac
  [ "$H" != "${HOME%/}" ] || die "the Hive needs a folder of its own, not your home folder"
  missing=""
  for t in git python3 curl; do command -v "$t" >/dev/null 2>&1 || missing="$missing $t"; done
  [ -z "$missing" ] || die "install$missing with your package manager, then run this again. This line doesn't use sudo"
  command -v systemctl >/dev/null 2>&1 || die "the Hive runs its agents under systemd, and there's no systemctl here"
  [ ! -e "$HOME/.hive-installed" ] || die "this whole account is already a Hive"
  ROOT=""; HIVE_OWN=1
  old_scan "$(id -un)"
  old_preflight "" "$H"
  if [ -e "$H" ] && [ ! -e "$H/.hive-account" ] && [ "$H" != "$OLD_ROOT" ]; then die "$H is already there and isn't a Hive, so I won't touch it. Pick another folder: HIVE_DIR=/path"; fi
  UNIT="$HOME/.config/systemd/user/hive-starter.service"
  if [ -e "$UNIT" ] && ! grep -qs "^ExecStart=.* $H/core/" "$UNIT"; then die "this account already runs a Hive from another folder. See $UNIT"; fi

  if [ -e "$H/.hive-installed" ]; then
    old_report
    if [ -n "$OLD_A" ] && [ "$OLD_STATE" != pending ]; then ask_yes "remove the old Hive and bring its things into this one"; fi
    begin "$OLD_A" "$OLD_A" 1 "$OLD_ROOT"
    if [ -n "$OLD_A" ]; then clone; old_remove; fi
    update_it
    old_import "$(id -un)" "$H"
    ending "$UPDATED"
    return 0
  fi

  old_report
  cat <<WARN

The Hive will run in your own account, as you, with its files in $H.
Agents run in a sandbox around that folder, and work there without asking. From inside, your other files, ~/.ssh and your other logins aren't there to read.
Three things stay open to them: that folder, the sign-in of the account they run on, and the internet, so anything they can read they can send.
To do something outside the sandbox they ask. The app asks you how much of that the Hive may settle by itself; an approved step runs as you.
WARN
  command -v bwrap >/dev/null 2>&1 || cat <<WARN

The sandbox needs bubblewrap, which isn't on this machine, and this line doesn't use sudo. Until it's installed (on Ubuntu or Debian: sudo apt install bubblewrap), no agent starts unless you choose to run without the sandbox.
WARN
  if bwrap_blocked; then cat <<WARN

This Ubuntu stops bubblewrap making its private space, and this line doesn't use sudo. Until an administrator runs this, no agent starts unless you choose to run without the sandbox. It re-allows unprivileged user namespaces for bubblewrap, a limit Ubuntu turns on by default, for every user of this machine, and that widens the kernel's attack surface. It stays if you remove the Hive, which then prints how to take it away:
  printf '$AA_TEXT' | sudo tee /etc/apparmor.d/bwrap >/dev/null && sudo apparmor_parser -r /etc/apparmor.d/bwrap
WARN
  fi
  cat <<WARN

Without the sandbox, which happens only if you choose it in the app, that folder doesn't hold them in. They then have full rights to everything this account can reach:
  - all your files, which they can read, change or delete
  - ~/.ssh and any keys it holds, so any machine those keys open
  - your logins and tokens, in your browser's files, ~/.config and anywhere else
WARN
  if command -v sudo >/dev/null 2>&1 && sudo -n true 2>/dev/null </dev/null; then
    echo "  - this whole machine, because this account can use sudo without a password"
  fi
  cat <<WARN
They can also use the network, so anything they can read they can send. They stop when the machine sleeps, and when you log out unless linger is on.
For a wall that doesn't rest on the sandbox alone, stop here and use the line without --this-account: it makes a separate user, which takes sudo once.

WARN
  if [ -n "$OLD_A" ] && [ "$OLD_STATE" != pending ]; then ask_yes "remove the old Hive and install this one in this account"
  elif [ -z "$OLD_A" ]; then ask_yes "install it in this account"; fi

  need=""; if [ -n "$OLD_A" ] || [ ! -e "$H/core" ]; then need=1; fi
  cl=""; [ -x "$H/.local/bin/claude" ] || cl=1
  begin "$need" "$OLD_A" "$cl" 1 "$OLD_ROOT"
  [ -z "$need" ] || clone
  old_remove
  # Its sign-ins will be in here, and other users of this machine could read it.
  mkdir -p "$H"; chmod 700 "$H"
  echo "$HOME" > "$H/.hive-account"
  [ -e "$H/core" ] || mv "$T/hive" "$H/core"
  # Its own Claude Code in its own home: yours, its sign-in and its updates aren't touched. The Hive signs in separately, in the web app.
  if [ ! -x "$H/.local/bin/claude" ]; then
    step "Installing Claude Code in $H"
    curl -fsSL https://claude.ai/install.sh 2>&3 | env HOME="$H" bash >&3 2>&3 || true
    [ -x "$H/.local/bin/claude" ] || die "Claude Code didn't install in $H. Run this again to carry on"
  fi
  step "Setting up the Hive"
  quietly env -u XDG_CONFIG_HOME -u XDG_DATA_HOME -u XDG_CACHE_HOME -u XDG_STATE_HOME -u CLAUDE_CONFIG_DIR \
    HIVE_PLAIN=1 HOME="$H" PATH="$H/.local/bin:$PATH" XDG_RUNTIME_DIR="${XDG_RUNTIME_DIR:-/run/user/$(id -u)}" "$H/core/install.sh"
  old_import "$(id -un)" "$H"
  ending "The Hive is installed." new
}

# Not fatal: the Hive that's there still works.
update_it() {
  step "Updating the Hive"
  UPDATED="The Hive was already here, and it's up to date."
  rc=0; quietly in_hive 'hive update' || rc=$?
  [ "$rc" = 0 ] || { UPDATED="The Hive was already here."; note "Couldn't update it just now, so it stays on the version it has. What it said is in ${LOG:-the lines above}."; }
}

main() {
  OWN=0
  for a in "$@"; do case "$a" in --this-account) OWN=1 ;; *) die "unknown option $a" ;; esac; done
  REPO="${HIVE_REPO:-https://github.com/ihsan-sa/hive}"; BRANCH="${HIVE_BRANCH:-v2}"; U="${HIVE_USER:-hive}"
  case "$U" in "" | -* | root | *[!a-z0-9_-]*) die "HIVE_USER must be a plain user name other than root, not $U" ;; esac

  case "$(uname -s)" in
    Linux) ;;
    Darwin) die "macOS isn't supported yet. A test build exists that has never run on a real Mac, so it may not work. To try it anyway:
  curl -fsSL https://hive.ihsan.cc/install-mac.sh | HIVE_MAC_TEST=1 sh" ;;
    *) die "this is the Linux installer. On Windows, in PowerShell: irm https://hive.ihsan.cc/install.ps1 | iex" ;;
  esac
  # A hive user under plain WSL can read the Windows drives through /mnt/c, so its own account would protect nothing.
  if [ -d /mnt/c/Windows ] && [ -z "${HIVE_SHARE_WINDOWS:-}" ]; then
    die "this WSL system can see your Windows files, and so could the agents. In PowerShell, run: irm https://hive.ihsan.cc/install.ps1 | iex
It makes a separate Linux system with the Windows drives switched off. To install here anyway, set HIVE_SHARE_WINDOWS=1."
  fi
  [ -d "${HIVE_SYSTEMD_DIR:-/run/systemd/system}" ] || die "the Hive runs its agents under systemd, and this system isn't running it"

  if [ "$OWN" = 1 ]; then this_account; return 0; fi

  if [ "$(id -u)" = 0 ]; then ROOT=""
  elif command -v sudo >/dev/null 2>&1; then ROOT="sudo"; echo "The Hive gets its own Linux user, which takes sudo once."
  else
    die "this needs root to make a user called $U, and there's no sudo here.
Without root, in a folder of your own account (agents then run in a sandbox around that folder, if this machine allows it; the app says): curl -fsSL https://hive.ihsan.cc/install.sh | sh -s -- --this-account
Or in a spare account: git clone -b $BRANCH $REPO ~/core, install Claude Code, and run ~/core/install.sh"
  fi

  # Asked here, and not halfway through a step. An old Hive can't be looked for without it.
  $ROOT true </dev/null || die "couldn't get root through sudo. Nothing changed"
  # The account the old Hive may be in: named, this one, or the one you run this from.
  old_scan "${HIVE_OLD_USER:-}" "$U" "${SUDO_USER:-$(id -un)}"
  [ -z "${HIVE_OLD_USER:-}" ] || [ "$OLD_A" = "$HIVE_OLD_USER" ] || die "found no old Hive in the account $HIVE_OLD_USER. Nothing changed"
  UH=$(getent passwd "$U" | cut -d: -f6 || true)
  old_preflight "$U" "${UH:-/home/$U}"
  old_report
  if [ -n "$OLD_A" ] && [ "$OLD_STATE" != pending ]; then ask_yes "remove the old Hive and install the new one"; fi

  if id "$U" >/dev/null 2>&1 && $ROOT test -e "$UH/.hive-installed"; then
    begin "$OLD_A" "$OLD_A" "$OLD_A" 1 "$OLD_ROOT"
    if [ -n "$OLD_A" ]; then step "Getting what it needs"; deps; clone; old_remove; fi
    update_it
    old_import "$U" "$UH"
    ending "$UPDATED"
    return 0
  fi

  begin 1 1 "$OLD_A" 1 "$OLD_ROOT"
  step "Getting what it needs"
  deps
  sandbox_dep; bwrap_profile
  clone
  old_remove
  step "Setting up the Hive and Claude Code"
  quietly $ROOT env HIVE_PLAIN=1 "$T/hive/install.sh" "$U"
  old_import "$U" "$(getent passwd "$U" | cut -d: -f6)"
  ending "The Hive is installed." new
}

main "$@"
