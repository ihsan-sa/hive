#!/bin/sh
# Install the Hive, or update it, on Linux. One line:
#   curl -fsSL https://hive.ihsan.cc/install.sh | sh
# Makes a Linux user (hive, or HIVE_USER=name) so agents get their own account, and runs install.sh as root.
# With no root, or by choice, in a folder of your own account, as you:
#   curl -fsSL https://hive.ihsan.cc/install.sh | sh -s -- --this-account
# HIVE_DIR=/path picks the folder (default ~/hive). HIVE_ACCEPT=1 accepts the warning when there's no terminal to ask on.
# Everything is inside main, so a download cut short runs nothing, and nothing below reads the rest of this script from stdin.
set -eu

die() { echo "hive: $*" >&2; exit 1; }

deps() {
  missing=""
  for t in git python3 curl; do command -v "$t" >/dev/null 2>&1 || missing="$missing $t"; done
  [ -z "$missing" ] && return 0
  echo "Installing$missing"
  if command -v apt-get >/dev/null 2>&1; then
    $ROOT env DEBIAN_FRONTEND=noninteractive apt-get update -qq </dev/null && $ROOT env DEBIAN_FRONTEND=noninteractive apt-get install -y -qq $missing </dev/null
  elif command -v dnf >/dev/null 2>&1; then $ROOT dnf install -y -q $missing </dev/null
  elif command -v pacman >/dev/null 2>&1; then $ROOT pacman -S --noconfirm --needed $(echo "$missing" | sed 's/python3/python/') </dev/null
  elif command -v zypper >/dev/null 2>&1; then $ROOT zypper -n install $missing </dev/null
  else die "install$missing with your package manager, then run this again"
  fi
}

# The sandbox around agents needs bubblewrap, and root is only here once. Not fatal: the app says what's missing and offers the choice.
sandbox_dep() {
  command -v bwrap >/dev/null 2>&1 && return 0
  echo "Installing bubblewrap (the sandbox around agents)"
  if command -v apt-get >/dev/null 2>&1; then
    $ROOT env DEBIAN_FRONTEND=noninteractive apt-get -o DPkg::Lock::Timeout=120 update -qq </dev/null && $ROOT env DEBIAN_FRONTEND=noninteractive apt-get -o DPkg::Lock::Timeout=120 install -y -qq bubblewrap </dev/null
  elif command -v dnf >/dev/null 2>&1; then $ROOT dnf install -y -q bubblewrap </dev/null
  elif command -v pacman >/dev/null 2>&1; then $ROOT pacman -S --noconfirm --needed bubblewrap </dev/null
  elif command -v zypper >/dev/null 2>&1; then $ROOT zypper -n install bubblewrap </dev/null
  else false
  fi || echo "Couldn't install bubblewrap, so the sandbox won't work until it's there. The app will say so." >&2
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
  echo "Letting bubblewrap make its private space ($d/bwrap): this re-allows unprivileged user namespaces for bubblewrap, a limit Ubuntu turns on by default, for every user of this machine, and that widens the kernel's attack surface. It stays if you remove the Hive, which then prints how to take it away."
  printf "$AA_TEXT" | $ROOT tee "$d/bwrap" >/dev/null && $ROOT apparmor_parser -r "$d/bwrap" </dev/null \
    || echo "Couldn't load the bubblewrap profile, so the sandbox may not work until it's there. The app will say so." >&2
}

clone() {
  T=$(mktemp -d)
  trap 'rm -rf "$T"' EXIT
  echo "Getting the Hive"
  git clone -q --single-branch -b "$BRANCH" "$REPO" "$T/hive" </dev/null
  # A release is a tag, and only a tag has been through the tests. HIVE_BRANCH asks for that branch as it stands.
  if [ -z "${HIVE_BRANCH:-}" ]; then
    TAG=$(git -C "$T/hive" tag -l 'v2.*' | grep -E '^v2\.[0-9]+\.[0-9]+$' | sort -V | tail -n 1 || true)
    [ -z "$TAG" ] || git -C "$T/hive" checkout -q -B "$BRANCH" "$TAG"
  fi
}

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
  if [ -e "$H" ] && [ ! -e "$H/.hive-account" ]; then die "$H is already there and isn't a Hive, so I won't touch it. Pick another folder: HIVE_DIR=/path"; fi
  UNIT="$HOME/.config/systemd/user/hive-starter.service"
  if [ -e "$UNIT" ] && ! grep -qs "^ExecStart=.* $H/core/" "$UNIT"; then die "this account already runs a Hive from another folder. See $UNIT"; fi

  if [ -e "$H/.hive-installed" ]; then
    echo "$H already has the Hive. Updating it."
    env -u XDG_CONFIG_HOME -u XDG_DATA_HOME -u XDG_CACHE_HOME -u XDG_STATE_HOME -u CLAUDE_CONFIG_DIR HOME="$H" PATH="$H/.local/bin:$PATH" sh -c 'hive update; hive web' </dev/null
    return 0
  fi

  cat <<WARN

The Hive will run in your own account, as you, with its files in $H.
Agents work there without asking permission for each step, inside a sandbox around that folder. From inside, your other files, ~/.ssh and your other logins aren't there to read.
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
  if [ "${HIVE_ACCEPT:-}" != 1 ]; then
    ( : </dev/tty ) 2>/dev/null || die "there's no terminal to ask you on. If you accept the above, set HIVE_ACCEPT=1"
    printf "Type yes to install it in this account: " >/dev/tty
    read -r answer </dev/tty
    [ "$answer" = yes ] || die "nothing changed"
  fi

  [ -e "$H/core" ] || clone
  mkdir -p "$H"
  echo "$HOME" > "$H/.hive-account"
  [ -e "$H/core" ] || mv "$T/hive" "$H/core"
  # Its own Claude Code in its own home: yours, its sign-in and its updates aren't touched. The Hive signs in separately, in the web app.
  if [ ! -x "$H/.local/bin/claude" ]; then
    echo "Installing Claude Code in $H"
    curl -fsSL https://claude.ai/install.sh | env HOME="$H" bash >/dev/null || true
    [ -x "$H/.local/bin/claude" ] || die "Claude Code didn't install in $H. Run this again to carry on"
  fi
  env -u XDG_CONFIG_HOME -u XDG_DATA_HOME -u XDG_CACHE_HOME -u XDG_STATE_HOME -u CLAUDE_CONFIG_DIR \
    HOME="$H" PATH="$H/.local/bin:$PATH" XDG_RUNTIME_DIR="${XDG_RUNTIME_DIR:-/run/user/$(id -u)}" "$H/core/install.sh" </dev/null
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
Without root, in a folder of your own account (agents then work in a sandbox around that folder, if this machine allows it; the app says): curl -fsSL https://hive.ihsan.cc/install.sh | sh -s -- --this-account
Or in a spare account: git clone -b $BRANCH $REPO ~/core, install Claude Code, and run ~/core/install.sh"
  fi

  if id "$U" >/dev/null 2>&1 && $ROOT test -e "$(getent passwd "$U" | cut -d: -f6)/.hive-installed"; then
    echo "$U already has the Hive. Updating it."
    $ROOT su - "$U" -c 'hive update; hive web' </dev/null
    return 0
  fi

  deps
  sandbox_dep; bwrap_profile
  clone
  $ROOT "$T/hive/install.sh" "$U" </dev/null
}

main "$@"
