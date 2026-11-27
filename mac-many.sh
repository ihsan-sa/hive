#!/bin/sh
# On the Mac runner, after mac-check.sh has installed the first Hive (user hive in the Lima VM): a second Hive beside it,
# the same line again, a project moved with its history, hive shell, and removing the first. Stub-free: the real line. Public log.
set -u
fail=0
ok() { echo "ok   $1"; }; bad() { echo "FAIL $1"; fail=1; }; note() { echo "--   $1"; }
export LIMA_HOME="$HOME/.hive/lima" HIVE_MAC_TEST=1 HIVE_VOICE=0 H2_VOICE=0
L="$HOME/.hive/lima-dist/bin/limactl"
vm() { "$L" shell --workdir / hive "$@" </dev/null; }
port() { vm sudo cat "/home/$1/.hive/web.json" 2>/dev/null | python3 -c 'import json,sys;print(json.load(sys.stdin)["port"])' 2>/dev/null; }
as() { vm sudo -iu "$1" sh -c "$2"; }
answers() { a=$("$HOME/.hive/bin/$1" web 2>/dev/null | tr -d '\r' | sed -n 's/.*\(http:[^ ]*\).*/\1/p' | head -n 1); [ -n "$a" ] && curl -fsS -o /dev/null -m 5 "${a%%#*}"; }
answers_soon() { i=0; while [ $i -lt 40 ]; do answers "$1" && return 0; i=$((i + 1)); sleep 3; done; return 1; }

answers_soon hive && ok "hive answers" || bad "hive answers"
P1=$(port hive); note "hive on port $P1"
as hive 'mkdir -p ~/projects/demo && cd ~/projects/demo && git init -q -b main && echo one > a.txt && git add -A && git -c user.name=t -c user.email=t@t commit -qm first && echo two >> a.txt && git -c user.name=t -c user.email=t@t commit -qam second'
S1=$(vm sudo sha256sum /home/hive/board.jsonl | cut -c1-64)

HIVE_USER=hive-work sh line.sh > many-1.log 2>&1 && ok "install hive-work" || { bad "install hive-work"; tail -n 30 many-1.log | sed -E 's/#[A-Za-z0-9_-]{20,}/#SECRET/'; }
tail -n 6 many-1.log | sed -E 's/#[A-Za-z0-9_-]{20,}/#SECRET/; s/^/--   | /'
[ -x "$HOME/.hive/bin/hive-work" ] && ok "hive-work command on the Mac" || bad "hive-work command on the Mac"
answers_soon hive-work && ok "hive-work answers on the Mac" || bad "hive-work answers on the Mac"
P2=$(port hive-work); note "ports: hive $P1, hive-work $P2"
[ -n "$P2" ] && [ "$P2" != "$P1" ] && ok "hive-work has its own port" || bad "hive-work has its own port"
[ "$(port hive)" = "$P1" ] && ok "first Hive's port unchanged" || bad "first Hive's port changed"
[ "$(vm sudo sha256sum /home/hive/board.jsonl | cut -c1-64)" = "$S1" ] && ok "first Hive's board unchanged" || bad "first Hive's board changed"
answers hive && ok "hive still answers" || bad "hive still answers"
grep -q 'Making the Linux VM' many-1.log && bad "a second VM was made" || ok "the same VM, no second one"

state() { as hive-work 'cat ~/.hive/web.json; git -C ~/core rev-parse HEAD; git -C ~/core status --porcelain'; }  # git as the Hive's user: root's git refuses another user's repo
t=$("$L" shell --workdir / hive sudo -iu hive-work sh < rig/pin-release.sh) && note "hive-work recorded as on $t, the newest release (rig/pin-release.sh says why)" || bad "record the newest release in hive-work"
note "hive-work core before: $(as hive-work 'git -C ~/core rev-parse --short HEAD')"
h0=$(state | shasum)
HIVE_USER=hive-work sh line.sh > many-2.log 2>&1 && ok "the same line again" || bad "the same line again"
tail -n 4 many-2.log | sed -E 's/#[A-Za-z0-9_-]{20,}/#SECRET/; s/^/--   | /'
grep -q 'Setting up the Hive' many-2.log && bad "the second run set it up again" || ok "the second run didn't set it up again"
grep -q 'already here' many-2.log && ok "the second run says the Hive was already here" || bad "the second run says the Hive was already here"
h1=$(state | shasum); note "hive-work core after: $(as hive-work 'git -C ~/core rev-parse --short HEAD')"
[ "$h0" = "$h1" ] && ok "the second run changed nothing (port, core)" || bad "the second run changed something"

Z=$(as hive 'hive export demo 2>/dev/null' | tail -n 1 | tr -d '\r'); note "export: $Z"
vm sudo install -o hive-work -g hive-work -m 600 "$Z" /home/hive-work/demo.zip && as hive-work 'hive import ~/demo.zip' && ok "import in hive-work" || bad "import in hive-work"
l1=$(as hive 'git -C ~/projects/demo log --format="%H %s"'); l2=$(as hive-work 'git -C ~/projects/demo log --format="%H %s"')
[ -n "$l1" ] && [ "$l1" = "$l2" ] && ok "same git log in both" || bad "same git log in both"

o=$(printf 'pwd; id -un\n' | "$HOME/.hive/bin/hive" shell hive-work 2>&1 | tr -d '\r' | tail -n 2 | tr '\n' ' '); note "hive shell hive-work: $o"
[ "$o" = "/home/hive-work hive-work " ] && ok "hive shell hive-work lands in its home" || bad "hive shell hive-work"
o=$(printf 'id -un\n' | "$HOME/.hive/bin/hive" shell daemon 2>&1); c=$?
[ "$c" != 0 ] && ! printf '%s' "$o" | tr -d '\r' | grep -qx daemon && ok "hive shell daemon refused" || bad "hive shell daemon: $c $o"

{ sleep 5; echo delete; sleep 5; } | HIVE_USER=hive HIVE_KEEP=none script -q /dev/null sh remove-mac.sh > many-rm.log 2>&1
tail -n 4 many-rm.log | cut -c1-220 | sed 's/^/--   remove: /'
vm id hive >/dev/null 2>&1 && bad "hive removed from the VM" || ok "hive removed from the VM"
[ -e "$HOME/.hive/bin/hive" ] && bad "the hive command gone" || ok "the hive command gone"
[ "$(port hive-work)" = "$P2" ] && ok "hive-work's port unchanged" || bad "hive-work's port changed"
answers_soon hive-work && ok "hive-work answers after hive is removed" || bad "hive-work answers after hive is removed"
[ "$fail" = 0 ] && echo "PASS" || echo "FAILED"
exit $fail
