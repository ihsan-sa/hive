#!/bin/sh
# On the Mac runner, after mac-check.sh has installed the first Hive (user hive in the Lima VM): two more beside it, installed at once,
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
B1=$(vm sudo stat -c %s /home/hive/board.jsonl); S1=$(vm sudo sha256sum /home/hive/board.jsonl | cut -c1-64)

# Two more at once, as a person running the line in two Terminal windows would.
# Bounded, and loud: a bare wait printed nothing for 8 minutes when the step's timeout killed it (run 46028297245),
# so every minute says where each install is, and past the deadline the logs and the VM's processes are shown before both are stopped.
mask() { sed -E 's/login#[^ ]*/login#.../g; s/#[A-Za-z0-9_-]{6,}/#SECRET/g'; }
vmt() { vm "$@" & vp=$!; ( sleep 60; kill "$vp" 2>/dev/null ) >/dev/null 2>&1 & vk=$!; wait "$vp"; vr=$?; pkill -P "$vk" 2>/dev/null; kill "$vk" 2>/dev/null; return $vr; }  # its sleep must not hold the step's output open  # macOS has no timeout(1)
stuck() {
  echo "--   the two installs, at $1:"
  for f in many-1.log many-1b.log; do echo "=== $f (last 40 lines)"; tail -n 40 "$f" | mask; done
  echo "=== on the Mac"; ps -axo pid,ppid,etime,args | grep -E 'limactl|line\.sh|ssh' | grep -v grep | cut -c1-220 | mask
  echo "=== in the VM"
  vmt sudo sh -c 'ps -eo pid,ppid,etime,stat,wchan:20,args --forest | grep -v "\[" | cut -c1-220; echo "--- locks"; ls -l /proc/[0-9]*/fd 2>/dev/null | grep -E "lock|dpkg" | head; for l in /var/lib/dpkg/lock /var/lib/dpkg/lock-frontend /var/lib/apt/lists/lock /var/cache/apt/archives/lock; do fuser -v "$l" 2>&1 | tail -n +2; done; echo "--- logind"; timeout 5 loginctl list-users; timeout 5 loginctl list-sessions; echo "--- journal"; journalctl -n 40 --no-pager | cut -c1-200; echo "--- homes"; ls -la /home; for h in /home/hive-work /home/hive-two; do echo "$h:"; ls -la "$h" 2>/dev/null | head -n 30; done' 2>&1 | mask
}
HIVE_USER=hive-work sh line.sh > many-1.log 2>&1 & a=$!
HIVE_USER=hive-two sh line.sh > many-1b.log 2>&1 & b=$!
t0=$(date +%s); limit=${MANY_LIMIT:-780}; ra=""; rb=""; ta=""; tb=""
while [ -z "$ra" ] || [ -z "$rb" ]; do
  sleep 10; t=$(( $(date +%s) - t0 ))
  if [ -z "$ra" ] && ! kill -0 "$a" 2>/dev/null; then wait "$a"; ra=$?; ta=$t; note "hive-work's line ended after $t s (exit $ra)"; fi
  if [ -z "$rb" ] && ! kill -0 "$b" 2>/dev/null; then wait "$b"; rb=$?; tb=$t; note "hive-two's line ended after $t s (exit $rb)"; fi
  [ $((t % 60)) -lt 10 ] && [ $t -ge 60 ] && note "$((t / 60)) min: hive-work | $(tail -n 1 many-1.log | cut -c1-120 | mask)  //  hive-two | $(tail -n 1 many-1b.log | cut -c1-120 | mask)"
  if [ $t -ge "$limit" ]; then
    bad "the two installs at once didn't finish in $limit s"; stuck "$t s"
    for p in $a $b; do pkill -P "$p" 2>/dev/null; kill "$p" 2>/dev/null; done
    pkill -f 'HIVE_USER=hive-(work|two) HIVE_INSIDE' 2>/dev/null
    echo FAILED; exit 1
  fi
done
[ "$ra" = 0 ] && ok "install hive-work (at the same time as hive-two), $ta s" || { bad "install hive-work"; tail -n 30 many-1.log | mask; }
[ "$rb" = 0 ] && ok "install hive-two (at the same time as hive-work), $tb s" || { bad "install hive-two"; tail -n 30 many-1b.log | mask; }
tail -n 6 many-1.log | sed -E 's/#[A-Za-z0-9_-]{20,}/#SECRET/; s/^/--   hive-work | /'
tail -n 6 many-1b.log | sed -E 's/#[A-Za-z0-9_-]{20,}/#SECRET/; s/^/--   hive-two | /'
[ -x "$HOME/.hive/bin/hive-work" ] && ok "hive-work command on the Mac" || bad "hive-work command on the Mac"
[ -x "$HOME/.hive/bin/hive-two" ] && ok "hive-two command on the Mac" || bad "hive-two command on the Mac"
answers_soon hive-work && ok "hive-work answers on the Mac" || bad "hive-work answers on the Mac"
answers_soon hive-two && ok "hive-two answers on the Mac" || bad "hive-two answers on the Mac"
P2=$(port hive-work); P3=$(port hive-two); note "ports: hive $P1, hive-work $P2, hive-two $P3"
[ -n "$P2" ] && [ -n "$P3" ] && [ "$P2" != "$P3" ] && ok "two installs at once got different ports" || bad "two installs at once got different ports"
[ "$P2" != "$P1" ] && [ "$P3" != "$P1" ] && ok "neither took the first Hive's port" || bad "neither took the first Hive's port"
AL=$(vm sudo sh -c 'A=$(command -v hive-admin || command -v h2-admin) && "$A" list' 2>&1 | sed -E 's/#[A-Za-z0-9_-]{20,}/#SECRET/')
printf '%s\n' "$AL" | sed 's/^/--   admin: /'
printf '%s' "$AL" | grep -q 'hive-work' && printf '%s' "$AL" | grep -q 'hive-two' && ok "hive-admin list shows both" || bad "h2-admin list shows both"
[ "$(port hive)" = "$P1" ] && ok "first Hive's port unchanged" || bad "first Hive's port changed"
# The first Hive is live and may post on its own board meanwhile: what it had stays as it was, and anything new is shown and must not be hive-work's.
[ "$(vm sudo sh -c "head -c $B1 /home/hive/board.jsonl | sha256sum" | cut -c1-64)" = "$S1" ] && ok "first Hive's board unchanged" || bad "first Hive's board changed"
NEW=$(vm sudo tail -c +"$((B1 + 1))" /home/hive/board.jsonl | python3 -c 'import json,sys
for l in sys.stdin:
    try: d=json.loads(l)
    except Exception: print("? not json"); continue
    print(d.get("from","?"), d.get("thread","?"), str(d.get("text",""))[:60].replace("\n"," "))' | sed -E 's/#[A-Za-z0-9_-]{20,}/#SECRET/')
[ -n "$NEW" ] && printf '%s\n' "$NEW" | sed 's/^/--   new on hive board: /'
printf '%s' "$NEW" | grep -qE '^hive-(work|two) ' && bad "a new Hive wrote on the first Hive's board" || ok "nothing of the new Hives' on the first board"
answers hive && ok "hive still answers" || bad "hive still answers"
cat many-1.log many-1b.log | grep -q 'Making the Linux VM' && bad "a second VM was made" || ok "the same VM, no second one"

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
