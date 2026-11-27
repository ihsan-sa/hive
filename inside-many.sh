#!/usr/bin/env bash
# Runs as root on a clean machine: several Hives side by side (step 7 of hive-app DESIGN-many-hives.md).
# A first Hive, two more installed at once, a rerun that changes nothing, hive-admin, a project moved with its history,
# hive shell into another Hive, and removing the first while the others keep answering. Stub Claude, no logins, public log.
set -u
fail=0
ok() { echo "ok   $1"; }; bad() { echo "FAIL $1"; fail=1; }; note() { echo "--   $1"; }
. /etc/os-release; note "$PRETTY_NAME, kernel $(uname -r)"
RAW="${INSTALL_URL%/get.sh}"
cat > /usr/local/bin/claude <<'STUB'
#!/bin/bash
case "${1:-}" in auth) echo '{"loggedIn": false}'; exit 0;; --version) echo 0; exit 0;; esac
echo '{"type":"result","is_error":false,"session_id":"x"}'
STUB
chmod +x /usr/local/bin/claude
[ -n "${HIVE_BRANCH:-}" ] && export HIVE_BRANCH || unset HIVE_BRANCH
get() { curl -fsSL "$INSTALL_URL" | HIVE_ACCEPT=1 HIVE_USER="$1" sh > "/tmp/install-$1.out" 2>&1; }
home() { getent passwd "$1" | cut -d: -f6; }
port() { python3 -c "import json,sys;print(json.load(open(sys.argv[1]))['port'])" "$(home "$1")/.hive/web.json" 2>/dev/null; }
as() { su - "$1" -c "$2"; }
up() {  # user: both services active and the app answering on its port (401 without the secret)
  local u=$1 id st c; id=$(id -u "$u")
  for s in hive-starter hive-dash; do st=$(as "$u" "XDG_RUNTIME_DIR=/run/user/$id systemctl --user is-active $s" 2>/dev/null); [ "$st" = active ] || { echo "$u $s: $st"; return 1; }; done
  for i in $(seq 20); do c=$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$(port "$u")/api/board"); [ "$c" = 401 ] && return 0; sleep 3; done
  echo "$u app on $(port "$u"): $c"; return 1
}
show() { tail -n "${2:-15}" "/tmp/install-$1.out" | sed -E 's/#[A-Za-z0-9_-]{20,}/#SECRET/; s/^/--   | /'; }

# The installers name no user of their own: every Hive's user comes from HIVE_USER.
for f in get.sh install.sh remove.sh get-mac.sh remove-mac.sh get.ps1 remove.ps1; do curl -fsSL "$RAW/$f" -o "/tmp/src-$f" || bad "fetch $f"; done
n=$(grep -nE -- '-i?u hive([^-a-z0-9_]|$)' /tmp/src-* | tee /tmp/u-hive.txt | wc -l)
[ "$n" = 0 ] && ok "no literal -u hive in the installers" || { bad "literal -u hive in the installers: $n"; sed 's/^/--   /' /tmp/u-hive.txt; }

# 1. The first Hive, as anyone installs it.
get hive && ok "install hive" || { bad "install hive"; show hive 30; }
up hive && ok "hive answers" || bad "hive answers"
P1=$(port hive); H1=$(home hive); note "hive on port $P1"
as hive 'mkdir -p ~/projects/demo && cd ~/projects/demo && git init -q -b main && echo one > a.txt && mkdir -p sub && echo two > sub/b.txt && git add -A && git -c user.name=t -c user.email=t@t commit -qm first && echo three >> a.txt && git -c user.name=t -c user.email=t@t commit -qam second'
as hive 'hive post --thread many "a line on the first board"' >/dev/null 2>&1
B1=$(wc -c < "$H1/board.jsonl"); S1=$(sha256sum "$H1/board.jsonl" | cut -c1-64); note "hive board: $B1 bytes"

# 2. Two more at once.
get hive-work & a=$!; get hive-two & b=$!
wait $a && ok "install hive-work (at the same time as hive-two)" || { bad "install hive-work"; show hive-work 30; }
wait $b && ok "install hive-two (at the same time as hive-work)" || { bad "install hive-two"; show hive-two 30; }
up hive-work && ok "hive-work answers" || bad "hive-work answers"
up hive-two && ok "hive-two answers" || bad "hive-two answers"
P2=$(port hive-work); P3=$(port hive-two); note "ports: hive $P1, hive-work $P2, hive-two $P3"
[ -n "$P2" ] && [ -n "$P3" ] && [ "$P2" != "$P3" ] && ok "two installs at once got different ports" || bad "two installs at once got different ports"
[ "$P1" != "$P2" ] && [ "$P1" != "$P3" ] && ok "neither took the first Hive's port" || bad "neither took the first Hive's port"
[ "$(port hive)" = "$P1" ] && ok "first Hive's port unchanged" || bad "first Hive's port changed: $(port hive)"
[ "$(head -c "$B1" "$H1/board.jsonl" | sha256sum | cut -c1-64)" = "$S1" ] && ok "first Hive's board unchanged" || bad "first Hive's board changed"
up hive && ok "first Hive still answers" || bad "first Hive still answers"
note "ports list: $(tr '\n' ' ' < /var/lib/hive/ports)"
[ "$(sort -u /var/lib/hive/ports | wc -l)" = 3 ] && [ "$(awk '{print $2}' /var/lib/hive/ports | sort -u | wc -l)" = 3 ] && ok "ports list: three Hives, three ports" || bad "ports list"
grep -h -v '^[[:space:]]*$' /tmp/install-hive-work.out | grep -iE 'port|user|folder' | head -n 4 | sed -E 's/#[A-Za-z0-9_-]{20,}/#SECRET/; s/^/--   said: /'

# 3. The same install again: nothing to do, nothing changed.
W=$(home hive-work)
state() { as hive-work 'cat ~/.hive/web.json; git -C ~/core rev-parse HEAD; git -C ~/core status --porcelain'; cat "$W"/.config/systemd/user/*.service 2>/dev/null; }  # git as the Hive's user: root's git refuses another user's repo and prints nothing
echo 'IyBSdW4gYXMgYSBIaXZlJ3Mgb3duIHVzZXIgYmVmb3JlIHRoZSByZXJ1biBjaGVja3MuIEEgcmlnIGluc3RhbGxzIHRoZSB0ZXN0IGNhbmRpZGF0ZSAoSElWRV9CUkFOQ0gpLCB3aGljaCBpc24ndCBidWlsdCBvbiB0aGUKIyBwdWJsaXNoZWQgcmVsZWFzZSwgc28gdGhlIHJlcnVuJ3MgYGhpdmUgdXBkYXRlYCB3b3VsZCBzd2FwIGl0IGZvciB0aGF0IG9sZGVyIHJlbGVhc2UgKCJ0aGUgcmVsZWFzZSB3aW5zIikgYW5kIHRoZSBjaGVja3MgYWZ0ZXIKIyB3b3VsZCB0ZXN0IHRoZSByZWxlYXNlLCBub3QgdGhlIGNhbmRpZGF0ZS4gUmVjb3JkaW5nIHRoZSBuZXdlc3QgcHVibGlzaGVkIHRhZyBhcyBpbnN0YWxsZWQgcHV0cyB0aGUgSGl2ZSB3aGVyZSBhIHJlYWwgaW5zdGFsbCBvZgojIHRoZSBuZXdlc3QgcmVsZWFzZSBpczogdGhlIHJlcnVuIHRoZW4gZmluZHMgbm90aGluZyBuZXdlciwgd2hpY2ggaXMgdGhlIGNhc2UgdGhlIGNoZWNrIGlzIGFib3V0LgpzZXQgLWUKdD0kKGdpdCAtQyAiJEhPTUUvY29yZSIgbHMtcmVtb3RlIC0tdGFncyAtLXJlZnMgb3JpZ2luICd2Mi4qJyB8IHNlZCAncyMuKnJlZnMvdGFncy8jIycgfCBncmVwIC1FeCAndjJcLlswLTldK1wuWzAtOV0rJyB8IHNvcnQgLVYgfCB0YWlsIC1uIDEpClsgLW4gIiR0IiBdCm1rZGlyIC1wICIkSE9NRS8uaDIiCnB5dGhvbjMgLWMgImltcG9ydCBqc29uLHN5cyxwYXRobGliIGFzIFA7cD1QLlBhdGguaG9tZSgpLycuaDIvdXBkYXRlLmpzb24nO2Q9anNvbi5sb2FkcyhwLnJlYWRfdGV4dCgpKSBpZiBwLmV4aXN0cygpIGVsc2Uge307ZFsndGFnJ109c3lzLmFyZ3ZbMV07cC53cml0ZV90ZXh0KGpzb24uZHVtcHMoZCkpIiAiJHQiCmVjaG8gIiR0Igo=' | base64 -d > /tmp/pin-release.sh; chmod 644 /tmp/pin-release.sh
t=$(as hive-work 'sh /tmp/pin-release.sh') && note "hive-work recorded as on $t, the newest release (rig/pin-release.sh says why)" || bad "record the newest release in hive-work"
note "hive-work core before the rerun: $(as hive-work 'git -C ~/core rev-parse --short HEAD')"
before=$(state | sha256sum)
get hive-work && ok "second run of the same install" || { bad "second run of the same install"; show hive-work 20; }
show hive-work 6
grep -q 'already here' /tmp/install-hive-work.out && ok "second run says it was already here" || bad "second run says it was already here"
grep -q 'Setting up the Hive' /tmp/install-hive-work.out && bad "second run set it up again" || ok "second run didn't set it up again"
after=$(state | sha256sum); note "hive-work core after the rerun: $(as hive-work 'git -C ~/core rev-parse --short HEAD')"
[ "$before" = "$after" ] && ok "second run changed nothing (port, core, services)" || bad "second run changed something"
up hive-work && ok "hive-work answers after the rerun" || bad "hive-work answers after the rerun"

# 4. The list root gives the app's manager.
A=$(command -v hive-admin || command -v h2-admin); note "admin tool: ${A:-none}"
if [ -n "$A" ]; then "$A" list > /tmp/admin.txt 2>&1; "$A" list --json > /tmp/admin.json 2>&1; sed 's/^/--   admin: /' /tmp/admin.txt
  python3 - "$P1" "$P2" "$P3" <<'PY' && ok "admin list --json: all three, with their ports" || bad "admin list --json"
import json, sys
d = json.load(open("/tmp/admin.json")); hs = d if isinstance(d, list) else d.get("hives", d)
got = {h.get("user") or h.get("name"): str(h.get("port")) for h in hs}
want = dict(zip(["hive", "hive-work", "hive-two"], sys.argv[1:4]))
print("--   admin json:", json.dumps(hs)[:400]); sys.exit(0 if all(got.get(k) == v for k, v in want.items()) else 1)
PY
else bad "admin tool"; fi

# 5. A project from one Hive into another, with its history.
Z=$(as hive 'hive export demo' 2>/tmp/export.err | tail -n 1); note "export: $Z $(head -c 200 /tmp/export.err)"
if [ -f "$Z" ]; then install -o hive-work -g hive-work -m 600 "$Z" "$W/demo.zip"
  as hive-work 'hive import ~/demo.zip' > /tmp/import.out 2>&1 && ok "import in hive-work" || bad "import in hive-work"; sed 's/^/--   import: /' /tmp/import.out
  l1=$(as hive 'git -C ~/projects/demo log --format="%H %s"'); l2=$(as hive-work 'git -C ~/projects/demo log --format="%H %s"')
  [ -n "$l1" ] && [ "$l1" = "$l2" ] && ok "same git log in both" || bad "same git log in both"
  t1=$(as hive 'cd ~/projects/demo && git ls-files -s && cat a.txt sub/b.txt'); t2=$(as hive-work 'cd ~/projects/demo && git status --porcelain && git ls-files -s && cat a.txt sub/b.txt')
  [ "$t1" = "$t2" ] && ok "same project, clean in hive-work" || bad "same project in hive-work"
  as hive-work 'hive import ~/demo.zip' > /tmp/import2.out 2>&1 && bad "a second import over it" || ok "a second import over it is refused"
else bad "export from hive"; fi

# 6. hive shell: into another Hive's home as its user (root here, as sudo is), and never into a system user.
o=$(printf 'pwd; id -un\n' | "$H1/.local/bin/hive" shell hive-work 2>&1 | tail -n 2 | tr '\n' ' '); note "hive shell hive-work: $o"
[ "$o" = "$W hive-work " ] && ok "hive shell hive-work lands in its home as hive-work" || bad "hive shell hive-work"
o=$(printf 'id -un\n' | "$H1/.local/bin/hive" shell daemon 2>&1); c=$?
[ "$c" != 0 ] && ! printf '%s' "$o" | grep -qx daemon && ok "hive shell daemon refused" || bad "hive shell daemon: $c $o"
o=$(printf 'pwd\n' | as hive 'hive shell' 2>&1 | tail -n 1); [ "$o" = "$H1" ] && ok "hive shell with no name: its own home" || bad "hive shell with no name: $o"

# 7. Remove the first Hive. The others keep their ports and answer.
curl -fsSL "$RAW/remove.sh" > /tmp/remove.sh
printf 'delete\n' | HIVE_USER=hive HIVE_KEEP=none timeout 120 script -qec 'sh /tmp/remove.sh' /dev/null > /tmp/remove.out 2>&1; tail -n 4 /tmp/remove.out | cut -c1-220 | sed 's/^/--   remove: /'
id hive >/dev/null 2>&1 && bad "hive removed" || ok "hive removed"
[ -e "$H1" ] && bad "hive's home gone" || ok "hive's home gone"
grep -q '^hive ' /var/lib/hive/ports && bad "hive's port given back" || ok "hive's port given back"
for u in hive-work hive-two; do id "$u" >/dev/null 2>&1 && ok "$u still there" || bad "$u still there"; done
[ "$(port hive-work)" = "$P2" ] && [ "$(port hive-two)" = "$P3" ] && ok "their ports unchanged" || bad "their ports changed"
sleep 10
up hive-work && ok "hive-work answers after hive is removed" || bad "hive-work answers after hive is removed"
up hive-two && ok "hive-two answers after hive is removed" || bad "hive-two answers after hive is removed"
as hive-work 'hive post --thread many "still here"' >/dev/null 2>&1 && ok "hive-work posts" || bad "hive-work posts"
[ "$fail" = 0 ] && echo "PASS" || echo "FAILED"
exit $fail
