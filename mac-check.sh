#!/bin/sh
# For someone with a Mac: does the Hive really work here? One line, about 20 minutes:
#   curl -fsSL https://hivelab.ca/mac-check.sh -o mac-check.sh && sh mac-check.sh
# It installs the Hive if it isn't there, checks it, asks you to do three things, and writes a report you send back.
# The report has versions, memory and results. It has no address, secret, user name or file of yours.
# HIVE_CHECK_AUTO=1 is for a Mac runner with no person: nothing is asked, and sleep is only a pause of the VM.
# HIVE_CHECK_FAULT=1 (with AUTO) breaks this Mac's port forward after the pause, so the recovery path has to run.
# HIVE_CHECK_LINE=FILE installs from that file, not the site.
# After the check, to test uninstall: run the uninstall line it prints, then: sh mac-check.sh --after-uninstall
set -u

SITE="${HIVE_SITE:-https://hivelab.ca}"
R="$HOME/hive-mac-report.txt"
L="$HOME/.hive/lima-dist/bin/limactl"
export LIMA_HOME="$HOME/.hive/lima"
export HIVE_MAC_TEST=1  # this script exists to test the test build
AUTO="${HIVE_CHECK_AUTO:-}"
FAULT="${HIVE_CHECK_FAULT:-}"
LINE="${HIVE_CHECK_LINE:-}"
fails=0

say() { printf '%s\n' "$*" | tee -a "$R"; }
pass() { say "ok    $1"; }
bad() { say "FAIL  $1"; fails=$((fails + 1)); }
check() { label=$1; shift; if "$@" >/dev/null 2>&1; then pass "$label"; else bad "$label"; fi; }  # label: what it runs has its own n
hive_() { "$HOME/.hive/bin/hive" "$@"; }
vm() { "$L" shell --workdir / hive "$@"; }
as_hive() { vm sudo -iu hive sh -c "$1"; }
address() { hive_ web 2>/dev/null | tr -d '\r' | sed -n 's/.*\(http:[^ ]*\).*/\1/p' | head -n 1; }
answers() { a=$(address); curl -fsS -o /dev/null -m 5 "${a%%#*}"; }
# Up to 3 minutes: a VM that has just started may still be starting its services.
# tries counts the attempts, for the report. An argument shortens the wait (seconds).
answers_soon() { t0_=$(date +%s); tries=0; while [ $(( $(date +%s) - t0_ )) -lt "${1:-180}" ]; do tries=$((tries + 1)); answers 2>/dev/null && { waited=$(( $(date +%s) - t0_ )); return 0; }; sleep 2; done; waited=never; return 1; }
# For the report when the app doesn't answer. The port and states only: the address holds a login key.
why_no_answer() {
  a=$(address); p=${a#http://*:}; p=${p%%/*}
  say "      answered after: ${waited:-?} s, ${tries:-?} tries. Port: ${p:-no address}"
  say "      in the VM: $(as_hive 'systemctl --user is-active hive-dash; ss -ltn | grep -c ":'"${p:-8077}"' "; curl -s -o /dev/null -m 5 -w "%{http_code}" http://127.0.0.1:'"${p:-8077}"'/login' 2>&1 | tr -d '\r' | tr '\n' ' ' | cut -c1-120)"
  say "      on this Mac: $(curl -s -o /dev/null -m 5 -w '%{http_code}' "http://127.0.0.1:${p:-8077}/login" 2>&1 | cut -c1-40)"
  forward_log
}
# Lima's port-forward lines for the app's port (last 5), so a real sleep that breaks the forward shows in the report.
forward_log() {
  a=$(address); p=${a#http://*:}; p=${p%%/*}
  grep -iE 'forward|listen' "$LIMA_HOME/hive/ha.stderr.log" 2>/dev/null | grep -E "${p:-8077}|rror" | tail -n 5 | cut -c1-160 | while IFS= read -r l; do say "      lima: $l"; done
}
# After a pause or sleep the Mac side of the port forward can go stale while the app in the VM is fine
# (Lima log: "Connection reset by peer", no new forward). If the app is active in the VM but the Mac's port is
# dead, make Lima forward again: first restart the app (the VM's agent announces the port anew), then, if that
# is not enough, restart the VM once. Says which one worked. recovered= holds the step.
recover_forward() {
  recovered=
  [ "$(as_hive 'systemctl --user is-active hive-dash' | tr -d '\r')" = active ] || return 1
  say "      the app is active in the VM but this Mac's port is dead: restarting the app so Lima forwards again"
  as_hive 'systemctl --user restart hive-dash' >/dev/null 2>&1
  if answers_soon 30; then recovered="the app restart"; return 0; fi
  say "      still dead: restarting the VM once"
  "$L" stop hive >/dev/null 2>&1; "$L" start hive >/dev/null 2>&1
  if answers_soon 120; then recovered="the VM restart"; return 0; fi
  return 1
}
# For the forced-fault run: make this Mac's forward to the app stale while the app in the VM stays fine.
# First ask Lima's ssh master to cancel the forward; if the forward is not an ssh one, stop the Mac-side
# listener when it is an ssh process. Says what it did; fails (return 1) if the port still answers.
break_forward() {
  a=$(address); p=${a#http://*:}; p=${p%%/*}; [ -n "$p" ] || return 1
  ssh -F "$LIMA_HOME/hive/ssh.config" -O cancel -L "127.0.0.1:$p:127.0.0.1:$p" lima-hive >/dev/null 2>&1 && how="ssh cancel of the forward"
  say "      fault: the listener on this Mac's port is: $(lsof -nP -iTCP:"$p" -sTCP:LISTEN 2>/dev/null | awk 'NR==2 {print $1}')"
  if answers 2>/dev/null; then
    # Not an ssh forward (Lima's own): stop the guest agent in the VM, which is what announces the port to the Mac.
    vm sudo -n pkill -9 -f lima-guestagent >/dev/null 2>&1; sleep 5; how="kill of the guest agent"
  fi
  if answers 2>/dev/null; then say "      fault: could not break the forward"; return 1; fi
  say "      fault: broke this Mac's forward by a ${how:-?}"
}
the_line() { if [ -n "$LINE" ]; then sh "$LINE"; else curl -fsSL "$SITE/install-mac.sh" | sh; fi; }
ask() { [ -z "$AUTO" ] || return 0; printf '\n%s\nPress Return when done. ' "$1"; read -r _ </dev/tty; }
yes_no() { [ -z "$AUTO" ] || { say "skip  $2 (no person here)"; return 0; }; printf '\n%s [y/n] ' "$1"; read -r a </dev/tty; case "$a" in y* | Y*) pass "$2" ;; *) bad "$2"; printf 'What happened? (Return to skip) '; read -r a </dev/tty; [ -z "$a" ] || say "      said: $a" ;; esac; }
clock_gap() { echo $(( $(vm date +%s | tr -d '\r') - $(date +%s) )); }

after_uninstall() {
  : > "$R"; say "Hive Mac check, after uninstall"
  [ ! -e "$HOME/.hive" ] && pass "the folder is gone" || bad "the folder is still there"
  [ ! -e "$HOME/Library/LaunchAgents/hive.vm.plist" ] && pass "the login start is gone" || bad "the login start is still there"
  if grep -qF '.hive/bin' "$HOME/.zprofile" "$HOME/.bash_profile" "$HOME/.profile" 2>/dev/null; then bad "a PATH line is still there"; else pass "PATH lines are gone"; fi
  pgrep -f 'hive/lima' >/dev/null && bad "VM programs are still running" || pass "no VM programs running"
  say "done, $fails failed."; echo "Please send back: $R"
}

main() {
  [ "${1:-}" != --after-uninstall ] || { after_uninstall; exit 0; }
  [ "$(uname -s)" = Darwin ] || { echo "This is for a Mac."; exit 1; }
  [ -n "$AUTO" ] || ( : </dev/tty ) 2>/dev/null || { echo "This needs a terminal, as it asks you things. Save it to a file and run: sh mac-check.sh"; exit 1; }
  : > "$R"
  say "Hive Mac check $(date -u +%Y-%m-%dT%H:%MZ)"
  say "macOS $(sw_vers -productVersion), $(sysctl -n machdep.cpu.brand_string 2>/dev/null), arm64=$(sysctl -n hw.optional.arm64 2>/dev/null || echo 0), shell arch $(uname -m)"
  say "memory $(( $(sysctl -n hw.memsize) / 1073741824 )) GB, free disk $(( $(df -k "$HOME" | awk 'NR==2 {print $4}') / 1048576 )) GB, power $(pmset -g batt 2>/dev/null | head -n 1 | sed "s/.*'\(.*\)'.*/\1/")"
  xcode-select -p >/dev/null 2>&1 && say "Apple's developer tools: installed" || say "Apple's developer tools: not installed"
  command -v brew >/dev/null 2>&1 && say "Homebrew: installed" || say "Homebrew: not installed"
  [ -x "$HOME/.hive/bin/hive" ] && say "Hive: already installed" || say "Hive: first install"

  say; say "--- the install line"
  t0=$(date +%s)
  if the_line; then pass "the line finished in $(( ($(date +%s) - t0) / 60 )) min"; else bad "the line failed"; say "See above in your Terminal; please paste what it said."; exit 1; fi
  check "the VM is running" sh -c "[ \"\$('$L' list hive --format '{{.Status}}')\" = Running ]"
  say "VM: $(vm uname -m | tr -d '\r') $(vm sh -c '. /etc/os-release; echo $PRETTY_NAME' | tr -d '\r'), Lima $("$L" --version 2>/dev/null | cut -d' ' -f3), Claude Code $(as_hive 'claude --version' 2>/dev/null | head -n 1 | tr -d '\r')"
  check "the VM uses Apple's framework" sh -c "'$L' list hive --format '{{.VMType}}' | grep -qx vz"
  check "the app answers on this Mac" answers_soon

  say; say "--- what agents can't reach"
  u=$(id -un)
  check "no Mac folder in the VM" sh -c "! '$L' shell --workdir / hive sudo -iu hive sh -c 'ls /Users/$u /Volumes' 2>/dev/null | grep -q ."
  check "no mounts in the VM's setup" sh -c "! '$L' list hive --format '{{.Config.Mounts}}' | grep -q Location"
  check "no SSH agent from the Mac" sh -c "! SSH_AUTH_SOCK=\"\${SSH_AUTH_SOCK:-}\" '$L' shell --workdir / hive sudo -iu hive sh -c 'test -n \"\$SSH_AUTH_SOCK\"'"
  # A listener on this Mac's localhost, which an agent must not reach. nc is on every Mac.
  nc -l 127.0.0.1 18077 >/dev/null 2>&1 & nc_pid=$!; sleep 1
  check "the hive user can't reach this Mac's localhost" sh -c "! '$L' shell --workdir / hive sudo -iu hive timeout 5 bash -c 'exec 3<>/dev/tcp/192.168.5.2/18077'"
  kill "$nc_pid" 2>/dev/null
  check "the hive user has no sudo" sh -c "! '$L' shell --workdir / hive sudo -iu hive sudo -n true"
  check "a port an agent opens elsewhere doesn't reach this Mac" sh -c "'$L' shell --workdir / hive sudo -iu hive sh -c 'nohup timeout 15 python3 -m http.server 18078 --bind 127.0.0.1 >/dev/null 2>&1 &' ; sleep 6; ! curl -fsS -m 3 -o /dev/null http://127.0.0.1:18078/"
  if as_hive 'test -f ~/core/sandbox.py'; then
    check "the sandbox works in the VM" as_hive 'cd ~/core && python3 -c "import sandbox, sys; sys.exit(0 if sandbox.check()[\"works\"] else 1)"'
  else say "skip  the sandbox works in the VM (this release has no sandbox yet)"; fi

  ask "Sign in and try it: run  ~/.hive/bin/hive open  in a new Terminal window (or use the page it opened). Sign in to Claude there and ask for something small, such as: write a haiku in a file."
  yes_no "Did the sign-in work, and did an agent do the task?" "sign-in and a first agent, in the Mac's browser"

  say; say "--- sleep and wake"
  before=$(as_hive 'systemctl --user is-active hive-starter hive-dash' | tr -d '\r' | tr '\n' ' ')
  ask "Put the Mac to sleep: Apple menu, Sleep. Leave it for at least 3 minutes. Then wake it and come back here. (Start a small task in the Hive first, if you can.)"
  if [ -n "$AUTO" ]; then
    say "This is a pause of the VM for 90 s, not a real sleep."
    pkill -STOP -f com.apple.Virtualization.VirtualMachine; sleep 90; pkill -CONT -f com.apple.Virtualization.VirtualMachine
  fi
  t0=$(date +%s); gap=99999; while [ $(( $(date +%s) - t0 )) -lt 60 ]; do gap=$(clock_gap); g=${gap#-}; [ "$g" -gt 3 ] || break; sleep 3; done
  say "VM clock vs Mac after waking: ${gap} s, after $(( $(date +%s) - t0 )) s"
  g=${gap#-}; if [ "$g" -le 3 ]; then pass "the VM's clock caught up"; else bad "the VM's clock is still off"; fi
  check "the VM has a network after waking" as_hive 'curl -fsS -m 15 -o /dev/null https://api.anthropic.com/ || [ $? = 22 ]'
  if [ -n "$AUTO" ] && [ -n "$FAULT" ]; then break_forward || bad "the forced fault could not be set"; fi
  if answers_soon 30; then pass "the app answers after waking, after $waited s, $tries tries"; say "      forward log after waking:"; forward_log
  else
    why_no_answer
    if recover_forward; then pass "the app answers after waking, after the stale port forward was fixed by $recovered"; else bad "the app answers after waking"; why_no_answer; fi
  fi
  after=$(as_hive 'systemctl --user is-active hive-starter hive-dash' | tr -d '\r' | tr '\n' ' ')
  [ "$before" = "$after" ] && pass "services as before: $after" || bad "services were: $before, now: $after"
  yes_no "Does the page still work, and did any running task carry on or get picked up?" "the app and agents after waking"

  say; say "--- starting at login"
  plist="$HOME/Library/LaunchAgents/hive.vm.plist"
  check "the login item is valid" plutil -lint "$plist"
  "$L" stop hive >/dev/null 2>&1
  launchctl bootstrap "gui/$(id -u)" "$plist" 2>/dev/null
  n=0; while [ "$n" -lt 60 ] && [ "$("$L" list hive --format '{{.Status}}' 2>/dev/null)" != Running ]; do n=$((n + 1)); sleep 5; done
  check "the login item starts the VM" sh -c "[ \"\$('$L' list hive --format '{{.Status}}')\" = Running ]"
  if answers_soon; then pass "the app answers after that, after $waited s"; else bad "the app answers after that"; why_no_answer; fi
  check "the hive command works from a stopped state" sh -c "'$HOME/.hive/bin/hive' web"

  say; say "--- update"
  if the_line >/dev/null 2>&1; then pass "running the line again works"; else bad "running the line again failed"; fi
  check "the app answers after the update" answers_soon
  check "hive update runs" hive_ update

  say; say "done, $fails failed."
  yes_no "Anything confusing or broken that this didn't catch? Answer n if something was, and type it." "nothing else to report"
  un="curl -fsSL $SITE/uninstall-mac.sh | sh"; [ -z "$LINE" ] || un="sh $(dirname "$LINE")/remove-mac.sh"
  say; say "To test uninstall: $un"
  say "Then: sh mac-check.sh --after-uninstall"
  echo; echo "Please send back this file: $R"
}

main "$@"
