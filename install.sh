#!/usr/bin/env bash
# Install the Hive. As a user, from a clone at ~/core:  ~/core/install.sh [--this-account] [--no-service]
# As root, from a clone anywhere:  sudo ./install.sh [user]   makes the user (default hive), copies the clone, installs Claude Code, and runs this as them.
set -eu
die() { echo "hive install: $*" >&2; exit 1; }
CORE="$(cd "$(dirname "$0")" && pwd)"; SERVICE=1; SHARED=0
if [ "$(id -u)" = 0 ]; then
  U="${1:-hive}"
  command -v git >/dev/null && command -v python3 >/dev/null && command -v curl >/dev/null || die "install git, python3 and curl first"
  id "$U" >/dev/null 2>&1 && [ -e "$(eval echo "~$U")/.hive-installed" ] && { echo "$U already has it: sudo -iu $U, then git -C ~/core pull"; exit 0; }
  id "$U" >/dev/null 2>&1 || useradd -m -s /bin/bash "$U"
  H="$(eval echo "~$U")"
  loginctl enable-linger "$U" 2>/dev/null || echo "NOTE: couldn't enable linger; the hive stops when $U logs out"
  [ -e "$H/core" ] || { cp -a "$CORE" "$H/core"; chown -R "$U:" "$H/core"; }
  su - "$U" -c 'command -v claude >/dev/null || [ -x ~/.local/bin/claude ] || curl -fsSL https://claude.ai/install.sh | bash'
  su - "$U" -c 'PATH="$HOME/.local/bin:$PATH" XDG_RUNTIME_DIR=/run/user/$(id -u) ~/core/install.sh' \
    || die "the install as $U failed; fix the error above and rerun this"
  echo; echo "To print that address again:  sudo -iu $U hive web"; exit 0
fi
for a in "$@"; do case "$a" in
  --no-service) SERVICE=0 ;;
  --this-account) SHARED=1 ;;
  *) die "unknown option $a" ;;
esac; done

[ "$CORE" = "$HOME/core" ] || die "clone it to ~/core (it is at $CORE)"
[ "$(uname -s)" = Linux ] || die "Linux only: the starter reads /proc and runs under systemd"
for t in python3 git claude; do command -v "$t" >/dev/null || die "$t not found on PATH"; done

# Agents run with bypassed permissions and everything this account can reach, so insist on a spare account.
if [ "$SHARED" = 0 ]; then
  other=$(find "$HOME" -mindepth 1 -maxdepth 1 ! -name '.*' ! -name core ! -name kb ! -name projects \
          ! -name run ! -name sessions ! -name board.jsonl ! -name log.jsonl ! -name modules ! -name files | head -3)
  [ ! -e "$HOME/.ssh" ] && [ -z "$other" ] || die "this account has files or ~/.ssh, and its agents would have full rights to them.
Use a new user (sudo ./install.sh), or pass --this-account if you accept that."
fi
top=$(git -C "$HOME" rev-parse --show-toplevel 2>/dev/null || true)
[ -z "$top" ] || [ "$top" = "$HOME" ] && [ ! -e "$HOME/.git" ] || [ -e "$HOME/.hive-home" ] \
  || die "$HOME is already in a git repo ($top); the backup would commit into it"

mkdir -p "$HOME/.local/bin" "$HOME/kb" "$HOME/projects" "$HOME/.config/systemd/user"
ln -sf "$CORE/hive" "$HOME/.local/bin/hive"
# A login bash reads only the first of these it finds (Fedora and RHEL make ~/.bash_profile, so ~/.profile would be skipped)
for PROFILE in "$HOME/.bash_profile" "$HOME/.bash_login" "$HOME/.profile"; do [ -e "$PROFILE" ] && break; done
# ~/.local/bin may not be on PATH in a fresh login shell
grep -qs '.local/bin' "$PROFILE" || echo 'PATH="$HOME/.local/bin:$PATH"' >> "$PROFILE"
# sudo -iu and su - don't set this, and systemctl --user needs it
grep -qs 'XDG_RUNTIME_DIR' "$PROFILE" || echo 'export XDG_RUNTIME_DIR="${XDG_RUNTIME_DIR:-/run/user/$(id -u)}"' >> "$PROFILE"

# Backup: the starter commits ~ every few minutes. Only the board, log and notes; core is its own repo.
[ -e "$HOME/.gitignore" ] || printf '/*\n!/.gitignore\n!/board.jsonl\n!/log.jsonl\n!/kb/\n' > "$HOME/.gitignore"
[ -e "$HOME/.git" ] || git -C "$HOME" init -q
touch "$HOME/.hive-home"  # this home's repo is ours, so a rerun after a failure can carry on
git -C "$HOME" config user.name >/dev/null  || git -C "$HOME" config user.name hive
git -C "$HOME" config user.email >/dev/null || git -C "$HOME" config user.email hive@localhost
git config --global user.name >/dev/null  || git config --global user.name hive
git config --global user.email >/dev/null || git config --global user.email hive@localhost

# The web app is the bundled dash. It's copied fresh, so rerunning this after a pull updates it; the secret stays.
rm -rf "$HOME/modules/dash"
"$CORE/hive" module add dash >/dev/null
"$CORE/hive" web --setup

if [ ! -s "$HOME/.claude/.credentials.json" ] && [ -z "${ANTHROPIC_API_KEY:-}" ]; then
  echo "NOTE: no Claude login yet. The web app walks you through it."
fi

if [ "$SERVICE" = 1 ]; then
  command -v systemctl >/dev/null || die "no systemctl; rerun with --no-service and run: python3 ~/core/starter.py"
  export XDG_RUNTIME_DIR="${XDG_RUNTIME_DIR:-/run/user/$(id -u)}"
  U="$HOME/.config/systemd/user"
  ln -sf "$CORE/hive-starter.service" "$U/hive-starter.service"
  # The service's PATH must find claude wherever it was installed (nvm, npm prefix, ...).
  mkdir -p "$U/hive-starter.service.d"
  printf '[Service]\nEnvironment=PATH=%s:%%h/.local/bin:/usr/local/bin:/usr/bin:/bin\nEnvironmentFile=-%%h/.config/hive/env\n' \
    "$(dirname "$(command -v claude)")" > "$U/hive-starter.service.d/local.conf"
  [ "$(loginctl show-user "$(id -un)" -p Linger --value 2>/dev/null)" = yes ] || loginctl enable-linger "$(id -un)" 2>/dev/null \
    || echo "NOTE: the Hive stops when you log out until an admin runs: sudo loginctl enable-linger $(id -un)"
  for _ in 1 2 3 4 5 6 7 8 9 10; do systemctl --user show-environment >/dev/null 2>&1 && break; sleep 1; done  # the user manager starts after linger, not with it; the bus socket alone needs dbus-user-session
  systemctl --user daemon-reload
  systemctl --user enable --now hive-starter
  ln -sf "$HOME/modules/dash/hive-dash.service" "$U/hive-dash.service"
  mkdir -p "$U/hive-dash.service.d"
  printf '[Service]\nEnvironment=PATH=%s:%%h/.local/bin:/usr/local/bin:/usr/bin:/bin\nEnvironmentFile=-%%h/.config/hive/env\n' \
    "$(dirname "$(command -v claude)")" > "$U/hive-dash.service.d/local.conf"
  systemctl --user daemon-reload
  systemctl --user enable hive-dash
  systemctl --user restart hive-dash
fi
touch "$HOME/.hive-installed"  # only now: a failure above can be rerun

cat <<MSG

The Hive is installed. Open this address in a browser on this machine, and it takes you from there:
  $("$CORE/hive" web 2>/dev/null)
It's the key to this hive, so keep it to yourself. Run hive web to see it again.
On another machine: ssh -L PORT:127.0.0.1:PORT this-machine, with the port from that address, then open it there.
If you'd rather stay in a terminal: hive
MSG
[ "$SERVICE" = 1 ] || echo "You chose no service, so start the app and the hive yourself: python3 ~/modules/dash/dash.py serve   and   python3 ~/core/starter.py"
