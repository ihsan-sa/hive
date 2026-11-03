#!/bin/sh
# Install the Hive, or update it, on Linux. One line:
#   curl -fsSL https://hive.ihsan.cc/install.sh | sh
# Makes a Linux user (hive, or HIVE_USER=name) so agents get their own account, and runs install.sh as root.
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

main() {
  REPO="${HIVE_REPO:-https://github.com/ihsan-sa/hive}"; BRANCH="${HIVE_BRANCH:-v2}"; U="${HIVE_USER:-hive}"
  case "$U" in "" | -* | root | *[!a-z0-9_-]*) die "HIVE_USER must be a plain user name other than root, not $U" ;; esac

  case "$(uname -s)" in
    Linux) ;;
    Darwin) die "macOS isn't supported yet: the Hive needs Linux with systemd" ;;
    *) die "this is the Linux installer. On Windows, in PowerShell: irm https://hive.ihsan.cc/install.ps1 | iex" ;;
  esac
  # A hive user under plain WSL can read the Windows drives through /mnt/c, so its own account would protect nothing.
  if [ -d /mnt/c/Windows ] && [ -z "${HIVE_SHARE_WINDOWS:-}" ]; then
    die "this WSL system can see your Windows files, and so could the agents. In PowerShell, run: irm https://hive.ihsan.cc/install.ps1 | iex
It makes a separate Linux system with the Windows drives switched off. To install here anyway, set HIVE_SHARE_WINDOWS=1."
  fi
  [ -d "${HIVE_SYSTEMD_DIR:-/run/systemd/system}" ] || die "the Hive runs its agents under systemd, and this system isn't running it"

  if [ "$(id -u)" = 0 ]; then ROOT=""
  elif command -v sudo >/dev/null 2>&1; then ROOT="sudo"; echo "The Hive gets its own Linux user, which takes sudo once."
  else
    die "this needs root to make a user called $U, and there's no sudo here.
Without root: in a spare account, git clone -b $BRANCH $REPO ~/core, install Claude Code, and run ~/core/install.sh"
  fi

  if id "$U" >/dev/null 2>&1 && $ROOT test -e "$(getent passwd "$U" | cut -d: -f6)/.hive-installed"; then
    echo "$U already has the Hive. Updating it."
    $ROOT su - "$U" -c 'hive update; hive web' </dev/null
    return 0
  fi

  deps
  T=$(mktemp -d)
  trap 'rm -rf "$T"' EXIT
  echo "Getting the Hive"
  git clone -q --single-branch -b "$BRANCH" "$REPO" "$T/hive" </dev/null
  # A release is a tag, and only a tag has been through the tests. HIVE_BRANCH asks for that branch as it stands.
  if [ -z "${HIVE_BRANCH:-}" ]; then
    TAG=$(git -C "$T/hive" tag -l 'v2.*' | grep -E '^v2\.[0-9]+\.[0-9]+$' | sort -V | tail -n 1 || true)
    [ -z "$TAG" ] || git -C "$T/hive" checkout -q -B "$BRANCH" "$TAG"
  fi
  $ROOT "$T/hive/install.sh" "$U" </dev/null
}

main "$@"
