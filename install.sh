#!/usr/bin/env bash
# Install the Hive. As a user, from a clone at ~/core:  ~/core/install.sh [--no-service]
# Inside someone's own account, get.sh --this-account makes a folder with a .hive-account file holding the real home, and runs this with HOME set to that folder.
# As root, from a clone anywhere:  sudo ./install.sh [user]   makes the user (default hive), copies the clone, installs Claude Code, and runs this as them.
set -eu
die() { echo "hive install: $*" >&2; exit 1; }
CORE="$(cd "$(dirname "$0")" && pwd)"; SERVICE=1
if [ "$(id -u)" = 0 ]; then
  U="${1:-hive}"
  command -v git >/dev/null && command -v python3 >/dev/null && command -v curl >/dev/null || die "install git, python3 and curl first"
  id "$U" >/dev/null 2>&1 && [ -e "$(eval echo "~$U")/.hive-installed" ] && { echo "$U already has it: sudo -iu $U, then git -C ~/core pull"; exit 0; }
  new=""; id "$U" >/dev/null 2>&1 || { useradd -m -s /bin/bash "$U"; new=1; }
  H="$(eval echo "~$U")"
  # Some systems make homes readable by a group. The Hive's sign-ins are in this one.
  [ -z "$new" ] || chmod 700 "$H"
  loginctl enable-linger "$U" 2>/dev/null || echo "NOTE: couldn't enable linger; the hive stops when $U logs out"
  [ -e "$H/core" ] || { cp -a "$CORE" "$H/core"; chown -R "$U:" "$H/core"; }
  su - "$U" -c 'command -v claude >/dev/null || [ -x ~/.local/bin/claude ] || curl -fsSL https://claude.ai/install.sh | bash'
  echo "Claude Code is in place. The Hive's install isn't finished yet, so keep this window open..."
  P=""; [ -z "${HIVE_PLAIN:-}" ] || P="HIVE_PLAIN=1 "
  su - "$U" -c "$P"'PATH="$HOME/.local/bin:$PATH" XDG_RUNTIME_DIR=/run/user/$(id -u) ~/core/install.sh' \
    || die "the install as $U failed; fix the error above and rerun this"
  [ -n "${HIVE_PLAIN:-}" ] || { echo; echo "To print that address again:  sudo -iu $U hive web"; }
  exit 0
fi
for a in "$@"; do case "$a" in
  --no-service) SERVICE=0 ;;
  *) die "unknown option $a" ;;
esac; done

[ "$CORE" = "$HOME/core" ] || die "clone it to ~/core (it is at $CORE)"
# A Hive in a folder of someone's account: its sign-ins go in here. v2.3.0 left the folder open to other users, and every update runs this.
[ ! -e "$HOME/.hive-account" ] || chmod 700 "$HOME"
[ "$(uname -s)" = Linux ] || die "Linux only: the starter reads /proc and runs under systemd"
for t in python3 git claude; do command -v "$t" >/dev/null || die "$t not found on PATH"; done

# A Hive in a folder inside someone's account. Its git repo is its own, whatever the home above it is in.
ACCOUNT=""
if [ -e "$HOME/.hive-account" ]; then
  ACCOUNT=$(cat "$HOME/.hive-account")
  case "$HOME" in *[!A-Za-z0-9_./-]*) die "$HOME has a character the service files can't hold; pick another folder" ;; esac
  case "$ACCOUNT" in /*) ;; *) die "$HOME/.hive-account doesn't hold a home folder" ;; esac
  if [ ! -e "$HOME/.git" ]; then
    [ -z "$(git -C "$ACCOUNT" rev-parse --show-toplevel 2>/dev/null)" ] || echo "NOTE: $ACCOUNT is in a git repo. $HOME has its own, so tell that repo to ignore this folder."
    git -C "$HOME" init -q
  fi
  touch "$HOME/.hive-home"
  # The service manager only reads the account's units, so that's where this folder's go too.
  mkdir -p "$ACCOUNT/.config/systemd/user" "$HOME/.config"
  [ -L "$HOME/.config/systemd" ] || ln -s "$ACCOUNT/.config/systemd" "$HOME/.config/systemd"
fi

# Agents run with bypassed permissions and everything this account can reach, so insist on a spare account.
# Once installed, the files here are the Hive's own work, and an update has to get past this.
if [ ! -e "$HOME/.hive-installed" ]; then
  other=$(find "$HOME" -mindepth 1 -maxdepth 1 ! -name '.*' ! -name core ! -name kb ! -name projects \
          ! -name run ! -name sessions ! -name board.jsonl ! -name log.jsonl ! -name reports ! -name asks.jsonl ! -name outcomes.jsonl ! -name progress.jsonl ! -name improvements.jsonl ! -name modules ! -name files ! -name settings.json ! -name 'hive-old-*' | head -3)
  [ ! -e "$HOME/.ssh" ] && [ -z "$other" ] || die "this account has files or ~/.ssh, and its agents would have full rights to them.
Use a new user (sudo ./install.sh), or install in a folder of this account with the --this-account line on the Hive's site."
fi
top=$(git -C "$HOME" rev-parse --show-toplevel 2>/dev/null || true)
[ -z "$top" ] || [ "$top" = "$HOME" ] && [ ! -e "$HOME/.git" ] || [ -e "$HOME/.hive-home" ] \
  || die "$HOME is already in a git repo ($top); the backup would commit into it"

mkdir -p "$HOME/.local/bin" "$HOME/kb" "$HOME/projects" "$HOME/.config/systemd/user"
ln -sf "$CORE/hive" "$HOME/.local/bin/hive"
# Inside an account, its own command has to give the Hive its own home. Someone's other program called this stays.
if [ -n "$ACCOUNT" ]; then
  W="$ACCOUNT/.local/bin/hive"; mkdir -p "$ACCOUNT/.local/bin"
  if [ -e "$W" ] && ! grep -qs '^# made by the Hive install' "$W"; then
    echo "NOTE: $W is something else, so I left it. The command for this Hive is $HOME/.local/bin/hive-account"; W="$HOME/.local/bin/hive-account"
  fi
  cat > "$W" <<WRAP
#!/bin/sh
# made by the Hive install: runs the Hive in $HOME with that folder as its home
unset XDG_CONFIG_HOME XDG_DATA_HOME XDG_CACHE_HOME XDG_STATE_HOME CLAUDE_CONFIG_DIR
HOME='$HOME' PATH='$HOME/.local/bin':"\$PATH" exec '$CORE/hive' "\$@"
WRAP
  chmod +x "$W"
  case ":$PATH:" in *":$ACCOUNT/.local/bin:"*) ;; *) grep -qs '.local/bin' "$ACCOUNT/.profile" || echo "NOTE: $ACCOUNT/.local/bin may not be on your PATH. The command is $W" ;; esac
fi
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
rm -rf "$HOME/modules/dash" "$HOME/modules/report" "$HOME/modules/providers"
for m in dash report providers; do "$CORE/hive" module add "$m" >/dev/null; done
# It can print the address, which is the key, and with HIVE_PLAIN what this prints goes to a log.
if [ -n "${HIVE_PLAIN:-}" ]; then "$CORE/hive" web --setup >/dev/null; else "$CORE/hive" web --setup; fi

# A new Hive gets the setup walkthrough and the sandbox. Only here: no file means an install from before these, which carries on as it was.
if [ ! -e "$HOME/.hive-installed" ] && [ ! -e "$HOME/settings.json" ]; then
  (umask 077; printf '{"power":2,"sandbox":"on","share":{"failures":true,"usage":false},"share_defaults":1,"setup_done":false}\n' > "$HOME/settings.json")
fi

if [ -z "${HIVE_PLAIN:-}" ] && [ ! -s "$HOME/.claude/.credentials.json" ] && [ -z "${ANTHROPIC_API_KEY:-}" ]; then
  echo "NOTE: no Claude login yet. The web app walks you through it."
fi

if [ "$SERVICE" = 1 ]; then
  command -v systemctl >/dev/null || die "no systemctl; rerun with --no-service and run: python3 ~/core/starter.py"
  export XDG_RUNTIME_DIR="${XDG_RUNTIME_DIR:-/run/user/$(id -u)}"
  U="$HOME/.config/systemd/user"
  # %h is always the account's home. In a folder inside it, a unit gets written out with that folder instead, each time this runs.
  place() { if [ -n "$ACCOUNT" ]; then rm -f "$U/$2"; sed "s|%h|$HOME|g" "$1" > "$U/$2"; else ln -sf "$1" "$U/$2"; fi; }
  # The service's PATH must find claude wherever it was installed (nvm, npm prefix, ...).
  local_conf() {
    if [ -n "$ACCOUNT" ]; then
      printf '[Service]\nEnvironment=PATH=%s:%s/.local/bin:/usr/local/bin:/usr/bin:/bin\nEnvironment=HOME=%s\nUnsetEnvironment=XDG_CONFIG_HOME XDG_DATA_HOME XDG_CACHE_HOME XDG_STATE_HOME CLAUDE_CONFIG_DIR\nEnvironmentFile=-%s/.config/hive/env\n' \
        "$(dirname "$(command -v claude)")" "$HOME" "$HOME" "$HOME"
    else
      printf '[Service]\nEnvironment=PATH=%s:%%h/.local/bin:/usr/local/bin:/usr/bin:/bin\nEnvironmentFile=-%%h/.config/hive/env\n' "$(dirname "$(command -v claude)")"
    fi
  }
  place "$CORE/hive-starter.service" hive-starter.service
  mkdir -p "$U/hive-starter.service.d"
  local_conf > "$U/hive-starter.service.d/local.conf"
  [ "$(loginctl show-user "$(id -un)" -p Linger --value 2>/dev/null)" = yes ] || loginctl enable-linger "$(id -un)" 2>/dev/null \
    || echo "NOTE: the Hive stops when you log out until an admin runs: sudo loginctl enable-linger $(id -un)"
  for _ in 1 2 3 4 5 6 7 8 9 10; do systemctl --user show-environment >/dev/null 2>&1 && break; sleep 1; done  # the user manager starts after linger, not with it; the bus socket alone needs dbus-user-session
  systemctl --user daemon-reload
  systemctl --user enable --now hive-starter
  place "$HOME/modules/dash/hive-dash.service" hive-dash.service
  mkdir -p "$U/hive-dash.service.d"
  local_conf > "$U/hive-dash.service.d/local.conf"
  systemctl --user daemon-reload
  systemctl --user enable hive-dash
  systemctl --user reset-failed hive-dash 2>/dev/null || true  # a crash loop's start limit would refuse this restart
  systemctl --user restart hive-dash
  # Updates from the releases; "auto_update": false in config.json stops them.
  mkdir -p "$U/hive-update.service.d"; cp "$U/hive-dash.service.d/local.conf" "$U/hive-update.service.d/local.conf"
  place "$CORE/hive-update.service" hive-update.service; place "$CORE/hive-update.timer" hive-update.timer
  systemctl --user daemon-reload
  systemctl --user enable --now hive-update.timer
  # The scoreboard: every 6 h, the hive's numbers against the day before, no model use (hive progress).
  place "$CORE/hive-progress.service" hive-progress.service; place "$CORE/hive-progress.timer" hive-progress.timer
  systemctl --user daemon-reload
  systemctl --user enable --now hive-progress.timer
  # The asks register: hourly, the person's new posts become asks in ~/asks.jsonl and delivered ones get marked (hive asks; one or two Sonnet calls).
  place "$CORE/hive-asks.service" hive-asks.service; place "$CORE/hive-asks.timer" hive-asks.timer
  systemctl --user daemon-reload
  systemctl --user enable --now hive-asks.timer
  # Outcomes: hourly, each run that ended gets a grade on whether it did what its waking post asked (hive outcomes; a Sonnet call or three).
  place "$CORE/hive-outcomes.service" hive-outcomes.service; place "$CORE/hive-outcomes.timer" hive-outcomes.timer
  systemctl --user daemon-reload
  systemctl --user enable --now hive-outcomes.timer
  # Reports and the daily check-in: every 6h; a no-op until setup is done, while "share" in settings.json is off, or with no endpoint.
  mkdir -p "$U/hive-report.service.d"; cp "$U/hive-dash.service.d/local.conf" "$U/hive-report.service.d/local.conf"
  place "$HOME/modules/report/hive-report.service" hive-report.service; place "$HOME/modules/report/hive-report.timer" hive-report.timer
  systemctl --user daemon-reload
  systemctl --user enable --now hive-report.timer
fi
touch "$HOME/.hive-installed"  # only now: a failure above can be rerun

# get.sh is around this, and says all of this itself.
[ -z "${HIVE_PLAIN:-}" ] || exit 0

# Whether the sandbox works here, in plain words. Never fails the install: the app asks what to do when it doesn't.
[ ! -e "$HOME/settings.json" ] || "$CORE/hive" sandbox check || true

cat <<MSG

The Hive is installed. Open this address in a browser on this machine, and it takes you from there:
  $("$CORE/hive" web 2>/dev/null)
It's the key to this hive, so keep it to yourself. Run hive web to see it again.
On another machine: ssh -L PORT:127.0.0.1:PORT this-machine, with the port from that address, then open it there.
If you'd rather stay in a terminal: hive
MSG
[ "$SERVICE" = 1 ] || echo "You chose no service, so start the app and the hive yourself: python3 ~/modules/dash/dash.py serve   and   python3 ~/core/starter.py"
