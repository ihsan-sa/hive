#!/usr/bin/env bash
# Runs as root on a clean machine: a person's install, a stub agent through the real starter in the real sandbox, removal.
set -u
SITE="${HIVE_SITE:?}"; fail=0
ok() { echo "ok   $1"; }; bad() { echo "FAIL $1"; fail=1; }
note() { echo "--   $1"; }
as() { su - hive -c "$1"; }
. /etc/os-release; note "$PRETTY_NAME, kernel $(uname -r), restrict_userns=$(cat /proc/sys/kernel/apparmor_restrict_unprivileged_userns 2>/dev/null || echo none), bwrap before: $(command -v bwrap || echo none)"
[ -d /run/systemd/system ] && ok "real systemd" || bad "real systemd"

# A stub in place of Claude Code: what it does is run the probes from where a real agent runs.
cat > /usr/local/bin/claude <<'STUB'
#!/bin/bash
case "${1:-}" in auth) echo '{"loggedIn": false}'; exit 0;; --version) echo 0; exit 0;; esac
th=$(printf '%s' "${2:-}" | sed -n 's/.*\(hive\|h2\) read --thread \([^` ]*\).*/\2/p' | head -1)
R=$HOME/projects/probe.txt; : > "$R"
p() { if eval "$2" >/dev/null 2>&1; then echo "yes $1" >> "$R"; else echo "no $1" >> "$R"; fi; }
p unshare 'unshare -Ur true'
p nested_bwrap 'bwrap --ro-bind / / --unshare-user true'
p mount 'mount -t tmpfs t /mnt'
p dummy_link 'ip link add zz0 type dummy'
p projects_write 'touch $HOME/projects/w'
p etc_write 'touch /etc/zz'
p sudo 'sudo -n true'
grep CapEff /proc/self/status | tr -d '\t' >> "$R"; grep Seccomp: /proc/self/status | tr -d '\t' >> "$R"
echo "home $HOME broker ${H2_BROKER:+set}" >> "$R"
hive post --thread "${th:-probe}" "stub ran" >> "$R" 2>&1
echo '{"type":"result","is_error":false,"session_id":"x"}'
STUB
chmod +x /usr/local/bin/claude

if curl -fsSL "$SITE/install.sh" | HIVE_ACCEPT=1 sh > /tmp/install.out 2>&1; then ok "install from the site"; else bad "install from the site"; fi
tail -n 25 /tmp/install.out | sed -E 's/#[A-Za-z0-9_-]{20,}/#SECRET/'
id hive >/dev/null 2>&1 && HH=$(getent passwd hive | cut -d: -f6) || { echo "FAIL no hive user"; exit 1; }
sleep 5
U=$(id -u hive)
for s in hive-starter hive-dash; do st=$(as "XDG_RUNTIME_DIR=/run/user/$U systemctl --user is-active $s" 2>/dev/null); [ "$st" = active ] && ok "$s active" || { bad "$s: $st"; as "XDG_RUNTIME_DIR=/run/user/$U journalctl --user -u $s -n 15 --no-pager" 2>&1 | tail -n 15; }; done
command -v aa-status >/dev/null && aa-status 2>/dev/null | grep -E 'bwrap' | sed 's/^/--   aa: /'
c=$(as 'hive sandbox check --json' 2>&1); note "sandbox check: $(printf '%s' "$c" | cut -c1-300)"
printf '%s' "$c" | grep -q '"works": true' && ok "sandbox works" || bad "sandbox works"

# Another local user, after the install: what the profile added for them.
useradd -m zz-other 2>/dev/null
su - zz-other -c 'unshare -Urm true' >/dev/null 2>&1 && note "other user: unshare -Urm succeeded" || note "other user: unshare -Urm refused"
su - zz-other -c 'bwrap --unshare-user --cap-add ALL --ro-bind / / ip link add zz1 type dummy' >/dev/null 2>&1 && note "other user: dummy link through bwrap worked" || note "other user: dummy link through bwrap refused"

# A stub agent through the real starter
as 'hive post --thread vmprobe "@new run the probes"' >/dev/null 2>&1 && ok "post" || bad "post"
for i in $(seq 40); do P=$(find "$HH" -name probe.txt 2>/dev/null | head -1); [ -n "$P" ] && grep -q '^home' "$P" && break; sleep 3; done
if [ -n "${P:-}" ]; then ok "stub agent ran"; sed 's/^/--   probe: /' "$P"
  w() { grep -q "^$2 $1\$" "$P" && ok "probe $1: $2" || bad "probe $1: wanted $2"; }
  w projects_write yes; w unshare no; w nested_bwrap no; w mount no; w dummy_link no; w etc_write no; w sudo no
  grep -q 'CapEff:0000000000000000' "$P" && ok "no capabilities" || bad "capabilities"
else bad "stub agent ran"; as 'tail -n 20 ~/log.jsonl' 2>&1 | cut -c1-300; as "XDG_RUNTIME_DIR=/run/user/$U journalctl --user -u hive-starter -n 25 --no-pager" 2>&1 | cut -c1-200; fi
as 'hive read --thread vmprobe' 2>&1 | cut -c1-200 | tail -n 8

# The app answers, and only with the secret
WP=$(python3 -c "import json;print(json.load(open('$HH/.hive/web.json'))['port'])" 2>/dev/null)
c1=$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$WP/api/board"); [ "$c1" = 401 ] && ok "app refuses without the secret" || bad "app without secret: $c1"
c2=$(curl -s -o /dev/null -w '%{http_code}' -H 'Host: evil.example' "http://127.0.0.1:$WP/api/board"); note "app with foreign Host: $c2"
ss -ltnH 2>/dev/null | grep -q "0.0.0.0:$WP" && bad "app open on all addresses" || ok "app on loopback only"

# Removal, typing delete as a person would
curl -fsSL "$SITE/uninstall.sh" > /tmp/remove.sh
printf 'delete\n' | script -qec 'sh /tmp/remove.sh' /dev/null > /tmp/remove.out 2>&1; tail -n 6 /tmp/remove.out | cut -c1-220
id hive >/dev/null 2>&1 && bad "hive user gone" || ok "hive user gone"
[ -e "$HH" ] && bad "home gone" || ok "home gone"
pgrep -u "$U" >/dev/null 2>&1 && bad "nothing left running" || ok "nothing left running"
id zz-other >/dev/null 2>&1 && ok "other user left alone" || bad "other user left alone"
[ "$fail" = 0 ] && echo "PASS" || echo "FAILED"
exit $fail
