#!/bin/sh
# Install the Hive on Linux or macOS. One line:
#   curl -fsSL https://hive.ihsan.cc/install.sh | sh
# Running it again reinstalls. HIVE_VERSION=v0.1.2 installs that release instead
# of the latest. The binary is static; no runtime is needed.
#
# As root on Linux, HIVE_USER=<name> installs it for that user, run by a
# system service (save this file and run it, `sudo HIVE_USER=hive sh install.sh`):
# see the root section below.
set -eu

REPO="${HIVE_REPO:-ihsan-sa/hive}"
CMD="${HIVE_CMD:-hive}"
VERSION="${HIVE_VERSION:-latest}"
DIR="${HIVE_BIN_DIR:-$HOME/.local/bin}"

# Names are matched whole, as fixed strings: a/x.y is not a/x-y.
case "$REPO" in
  */*/* | /* | */ | .* | */.* | *[!A-Za-z0-9._/-]*) echo "$CMD: HIVE_REPO must be owner/name, not $REPO" >&2; exit 1 ;;
  */*) ;;
  *) echo "$CMD: HIVE_REPO must be owner/name, not $REPO" >&2; exit 1 ;;
esac
case "$CMD" in "" | -* | *[!A-Za-z0-9._-]*) echo "hive: HIVE_CMD must be a plain name" >&2; exit 1 ;; esac

# Root installs for a worker user. Root never writes, reads or runs anything
# in the worker's home: the user install below runs as the worker, from a copy
# of this file, so a link the worker plants (~/.local/bin -> /etc/cron.hourly)
# only leads a write the worker can't make, and set -e stops there. Root keeps
# its own records in /etc/hive/<user>: the repo releases come from (a rerun
# naming another is refused) and the GitHub token files, one per repo owner,
# created empty, root's and 0600. The service reads them through systemd's
# LoadCredential, so the worker never can. The user name is the person's own
# choice, never built from a repo name, and the unit and records are keyed by
# it alone.
if [ -n "${HIVE_USER:-}" ]; then
  user=$HIVE_USER
  fail() { echo "$CMD: $*" >&2; exit 1; }
  [ "$(uname -s)" = Linux ] || fail "HIVE_USER installs a system service, on Linux only"
  [ "$(id -u)" = 0 ] || fail "HIVE_USER installs for another user, which needs root"
  case "$user" in "" | -* | root | *[!a-z0-9_-]*) fail "HIVE_USER must be a plain user name other than root, not $user" ;; esac
  [ -z "${HIVE_BIN_DIR:-}" ] || fail "HIVE_BIN_DIR can't be set with HIVE_USER: the worker's binary goes in its ~/.local/bin"
  entry=$(getent passwd "$user") || fail "no user $user; create it first (useradd -m $user)"
  uid=$(printf '%s\n' "$entry" | cut -d: -f3)
  home=$(printf '%s\n' "$entry" | cut -d: -f6)
  [ "$uid" != 0 ] || fail "$user is root's uid"
  case "$home" in /*/* | /[!/]*) ;; *) fail "$user's home $home isn't a folder of its own" ;; esac
  case "$home" in *[!A-Za-z0-9._/-]* | */../* | */..) fail "$user's home $home has characters a unit can't hold" ;; esac
  [ "$(stat -L -c %u "$home" 2>/dev/null)" = "$uid" ] || fail "$user's home $home isn't $user's own folder"
  [ -f "$0" ] || fail "with HIVE_USER, save the installer and run it as a file: sudo HIVE_USER=$user sh install.sh"
  owners=$(printf '%s\n' "${HIVE_TOKEN_OWNERS:-${REPO%%/*}}" | tr '[:upper:]' '[:lower:]')
  for o in $owners; do
    case "$o" in -* | *[!a-z0-9-]*) fail "HIVE_TOKEN_OWNERS holds $o, not a GitHub owner" ;; esac
  done

  umask 077
  etc=/etc/hive/$user
  mkdir -p "$etc/tokens"
  chown root:root /etc/hive "$etc" "$etc/tokens"
  chmod 755 /etc/hive
  chmod 700 "$etc" "$etc/tokens"
  if [ -e "$etc/repo" ]; then
    had=$(cat "$etc/repo")
    [ "$had" = "$REPO" ] || fail "$user's Hive installs from $had, not $REPO; refusing. To change it, uninstall first (sudo HIVE_USER=$user sh uninstall.sh)"
  else
    printf '%s\n' "$REPO" >"$etc/repo"
  fi
  for o in $owners; do
    f="$etc/tokens/$o.token"
    [ -e "$f" ] || [ -L "$f" ] || : >"$f"
    chown root:root "$f"
    chmod 600 "$f"
  done

  tmp=$(mktemp -d)
  trap 'rm -rf "$tmp"' EXIT
  cp "$0" "$tmp/install.sh"
  chmod 755 "$tmp"
  chmod 644 "$tmp/install.sh"
  cd /
  # shellcheck disable=SC2154 # the proxy variables are passed on only when set
  runuser -u "$user" -- env -i HOME="$home" USER="$user" LOGNAME="$user" PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin \
    HIVE_REPO="$REPO" HIVE_CMD="$CMD" HIVE_VERSION="$VERSION" HIVE_SHARE="${HIVE_SHARE:-no}" \
    ${HTTPS_PROXY+"HTTPS_PROXY=$HTTPS_PROXY"} ${https_proxy+"https_proxy=$https_proxy"} ${NO_PROXY+"NO_PROXY=$NO_PROXY"} ${no_proxy+"no_proxy=$no_proxy"} \
    sh "$tmp/install.sh" || fail "installing as $user stopped (above); the service is left as it was"

  # The service runs as the worker and sees only its own home, writable, and
  # the system read-only; nothing it starts gains privileges, so a warden's
  # sudo step can't change the system from it either.
  unit="hive-$user.service"
  {
    printf '[Unit]\nDescription=The Hive of %s\nWants=network-online.target\nAfter=network-online.target\n\n[Service]\n' "$user"
    printf 'User=%s\nExecStart=%s/.local/bin/%s daemon\nKillMode=process\nRestart=on-failure\nRestartSec=10\n' "$user" "$home" "$CMD"
    for f in "$etc"/tokens/*.token; do
      if [ -f "$f" ]; then printf 'LoadCredential=%s:%s\n' "${f##*/}" "$f"; fi
    done
    printf 'ProtectSystem=strict\nProtectHome=tmpfs\nBindPaths=%s\nPrivateTmp=yes\nNoNewPrivileges=yes\n' "$home"
    # Nothing here may lay a mount over /proc or /sys (ProtectKernelTunables,
    # ProtectKernelLogs, ProtectControlGroups, ProtectProc, ProcSubset): each
    # agent's sandbox mounts a fresh proc in a new user namespace, which the
    # kernel refuses while the host's /proc has mounts over it.
    printf 'RestrictSUIDSGID=yes\nProtectKernelModules=yes\nProtectClock=yes\nLockPersonality=yes\n'
    printf '\n[Install]\nWantedBy=multi-user.target\n'
  } >"$tmp/unit"
  cmp -s "$tmp/unit" "/etc/systemd/system/$unit" || install -m 644 -o root -g root "$tmp/unit" "/etc/systemd/system/$unit"
  if [ -d /run/systemd/system ]; then
    systemctl daemon-reload
    systemctl enable "$unit"
    systemctl restart "$unit"
    echo "Started $unit."
  else
    echo "Wrote $unit; systemd isn't running here, so start it when it is: systemctl enable --now $unit"
  fi
  echo "Put a GitHub token in $etc/tokens/<owner>.token for each repo owner, or log $user into gh. Log Claude in as $user: sudo -u $user -i $CMD login"
  exit 0
fi

os=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$(uname -m)" in
  x86_64|amd64) arch=amd64 ;;
  arm64|aarch64) arch=arm64 ;;
  *) echo "$CMD: unsupported architecture $(uname -m)" >&2; exit 1 ;;
esac
case "$os" in linux|darwin) ;; *) echo "$CMD: unsupported OS $os (on Windows use install.ps1)" >&2; exit 1 ;; esac

asset="hive-$os-$arch"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

echo "Downloading the Hive ($asset, $VERSION) from $REPO ..."
base="https://github.com/$REPO/releases/download/$VERSION"
[ "$VERSION" = latest ] && base="https://github.com/$REPO/releases/latest/download"
curl -fsSL "$base/$asset" -o "$tmp/$asset"
curl -fsSL "$base/checksums.txt" -o "$tmp/checksums.txt"
want=$(awk -v a="$asset" '$2 == a || $2 == "*" a {print $1; exit}' "$tmp/checksums.txt")
if command -v sha256sum >/dev/null 2>&1; then got=$(sha256sum "$tmp/$asset" | awk '{print $1}'); else got=$(shasum -a 256 "$tmp/$asset" | awk '{print $1}'); fi
if [ -z "$want" ] || [ "$want" != "$got" ]; then echo "$CMD: checksum mismatch; not installing" >&2; exit 1; fi

mkdir -p "$DIR"
for c in "$CMD" hive-v0; do [ -x "$DIR/$c" ] && "$DIR/$c" shutdown >/dev/null 2>&1 || true; done
install -m 755 "$tmp/$asset" "$DIR/$CMD"
# The command was hive-v0 up to v0.1.2; an old binary left there becomes an alias.
if [ "$CMD" != hive-v0 ] && [ -e "$DIR/hive-v0" ]; then ln -sf "$CMD" "$DIR/hive-v0"; fi

case ":$PATH:" in
  *":$DIR:"*) ;;
  *)
    # macOS opens zsh, which never reads .profile, so .zshrc is written there.
    for rc in "$HOME/.profile" "$HOME/.bashrc" "$HOME/.zshrc"; do
      [ -f "$rc" ] || [ "$rc" = "$HOME/.profile" ] || [ "$rc:$os" = "$HOME/.zshrc:darwin" ] || continue
      # shellcheck disable=SC2016 # $PATH is written literally, for the shell to expand
      grep -qsF "$DIR" "$rc" || printf '\nexport PATH="%s:$PATH"\n' "$DIR" >> "$rc"
    done
    echo "Added $DIR to your PATH (open a new terminal)."
    ;;
esac
"$DIR/$CMD" --version

# Sharing failures stays off unless the person says yes. A pipe from curl has
# no stdin to ask on, so the question goes to the terminal; with no terminal,
# or on Enter, it's no. HIVE_SHARE=yes|no answers ahead of time, and a
# reinstall that was answered before doesn't ask again. HIVE_TTY stands in for
# the terminal in tests. The question is report.Question word for word
# (TestQuestionSaysWhatIsSent). Releases before v0.1.18 have no report
# command (any exit but 0 or 1 from `report asked`): they are installed without
# the question, as their own installer did (TestInstallerOldReleases).
share="${HIVE_SHARE:-}"
tty="${HIVE_TTY:-/dev/tty}"
asked=0
"$DIR/$CMD" report asked >/dev/null 2>&1 || asked=$?
if [ "$asked" -gt 1 ]; then
  share=
elif [ -z "$share" ] && [ "$asked" -ne 0 ]; then
  share=no
  if (: <"$tty") 2>/dev/null; then
    printf '\nSend failure and usage reports to the Hive developer so problems get fixed? Each failure report holds what failed, when and why (up to 2,000 characters), and the agent reply or the last lines of the task log (up to 4,000 characters). A usage report counts runs, turns, merges, reverts, cost and dropped reports, with the harness name and the time span it covers. Every report has the Hive version, the OS and a random install id. Secrets, emails and your home folder are scrubbed on this machine first. (y/N) ' >>"$tty"
    read -r share <"$tty" || share=no
    [ -n "$share" ] || share=no # Enter is no, and is kept as no
  fi
fi
case "$share" in
  "") ;;
  [yY]*) "$DIR/$CMD" report on ;;
  *) "$DIR/$CMD" report off ;;
esac
echo "Installed. Run: $CMD"
