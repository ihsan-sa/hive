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
p threads 'python3 -I -c "import threading;t=threading.Thread(target=int);t.start();t.join()"'
p git 'git --version'
p node_like 'sh -c "sleep 0 & wait"'
grep CapEff /proc/self/status | tr -d '\t' >> "$R"; grep Seccomp: /proc/self/status | tr -d '\t' >> "$R"
echo "home $HOME broker ${H2_BROKER:+set}" >> "$R"
hive post --thread "${th:-probe}" "stub ran" >> "$R" 2>&1
echo '{"type":"result","is_error":false,"session_id":"x"}'
STUB
chmod +x /usr/local/bin/claude

[ -n "${HIVE_BRANCH:-}" ] && export HIVE_BRANCH || unset HIVE_BRANCH  # a candidate branch; empty means the released tag
if curl -fsSL "$SITE/install.sh" | HIVE_ACCEPT=1 sh > /tmp/install.out 2>&1; then ok "install from the site"; else bad "install from the site"; fi
tail -n 25 /tmp/install.out | sed -E 's/#[A-Za-z0-9_-]{20,}/#SECRET/'
note "$(bwrap --version 2>&1 | head -1)"
id hive >/dev/null 2>&1 && HH=$(getent passwd hive | cut -d: -f6) || { echo "FAIL no hive user"; exit 1; }
sleep 5
U=$(id -u hive)
# This run tests a core commit not yet released: the sandbox change, on the released tree.
printf %s 'Y29tbWl0IDZkMjdmNGE0OTNmMjlkNGE3M2RhYTEzMzUwMzY1MWNhNDUxNDRmNjkKQXV0aG9yOiBIaXZlIEgyIDw3NzQwNjUxNitpaHNhbi1zYUB1c2Vycy5ub3JlcGx5LmdpdGh1Yi5jb20+CkRhdGU6ICAgVHVlIE5vdiAzIDE0OjE2OjUyIDIwMjYgLTA1MDAKCiAgICBTYW5kYm94OiByZWZ1c2UgbmVzdGVkIHVzZXIgbmFtZXNwYWNlcyB3aGVyZSBid3JhcCBjYW4ndCwgYW5kIHNheSBzbyBpbiB0aGUgY2hlY2sKICAgIAogICAgVWJ1bnR1IDIyLjA0J3MgYndyYXAgMC42LjEgaGFzIG5vIC0tZGlzYWJsZS11c2VybnMsIHNvIGFuIGFnZW50IGNvdWxkIG1ha2UgYQogICAgbmVzdGVkIHVzZXIgbmFtZXNwYWNlLiBUaGUgc2VjY29tcCBmaWx0ZXIgbm93IHJlZnVzZXMgdW5zaGFyZSBhbmQgY2xvbmUgd2l0aAogICAgQ0xPTkVfTkVXVVNFUiwgYW5kIGNsb25lMywgd2hlbiB0aGUgZmxhZyBpcyBtaXNzaW5nIG9yIGRvZXNuJ3Qgd29yay4gV2l0aCB0aGUKICAgIGZsYWcgdGhlIHByb2dyYW0gaXMgYnl0ZSBmb3IgYnl0ZSB3aGF0IGl0IHdhcy4gVGhlIGNoZWNrIG5vdyB0cmllcyBpdC4KICAgIGBoMiBzYW5kYm94IGNoZWNrYCBleGl0cyAxIG9uIGZhaWx1cmU7IC0tanNvbiBzdGF5cyAwIGJlY2F1c2UgZGFzaC5weSByZWFkcyBhbnkKICAgIG90aGVyIGV4aXQgYXMgbm8gYW5zd2VyLgogICAgCiAgICBDby1BdXRob3JlZC1CeTogQ2xhdWRlIE9wdXMgNS41IDxub3JlcGx5QGFudGhyb3BpYy5jb20+CgpkaWZmIC0tZ2l0IGEvc2FuZGJveC5weSBiL3NhbmRib3gucHkKaW5kZXggOGVlY2E2MS4uYjlmNDFlOSAxMDA2NDQKLS0tIGEvc2FuZGJveC5weQorKysgYi9zYW5kYm94LnB5CkBAIC02NywxMyArNjcsMjMgQEAgZGVmIF9weXRob24oKToKIGRlZiBfZmlsdGVyKCk6CiAgICAgIiIiQSBzZWNjb21wIHByb2dyYW0sIGFzIGEgZmlsZSBid3JhcCByZWFkcywgb3IgTm9uZSBvbiBhIG1hY2hpbmUgd2UgZG9uJ3Qgc2hpcCBmb3IuIEEgbmV0d29yayBuYW1lc3BhY2UgZG9lc24ndCBjb3ZlciB2c29jaywgYW5kIHVuZGVyIFdTTCBvciBhIFZNIHRoYXQgcmVhY2hlcyB0aGUgaG9zdAogICAgIG91dHNpZGUgYW55IHByb3h5LCBzbyBzb2NrZXQoQUZfVlNPQ0spIGlzIHJlZnVzZWQuIGlvX3VyaW5nIGNhbiBtYWtlIHNvY2tldHMgd2l0aG91dCB0aGF0IGNhbGwsIGFuZCBvdGhlciBBQklzIHJlYWNoIHNvY2tldCBieSBhbm90aGVyIG51bWJlcjogYm90aCByZWZ1c2VkLCBhbmQgcHJvZ3JhbXMgZmFsbCBiYWNrLiIiIgotICAgIGFyY2gsIHNvY2sgPSB7Ing4Nl82NCI6ICgweEMwMDAwMDNFLCA0MSksICJhYXJjaDY0IjogKDB4QzAwMDAwQjcsIDE5OCl9LmdldChvcy51bmFtZSgpLm1hY2hpbmUsIChOb25lLCBOb25lKSkKKyAgICBhcmNoLCBzb2NrLCB1bnNoYXJlLCBjbG9uZSA9IHsieDg2XzY0IjogKDB4QzAwMDAwM0UsIDQxLCAyNzIsIDU2KSwgImFhcmNoNjQiOiAoMHhDMDAwMDBCNywgMTk4LCA5NywgMjIwKX0uZ2V0KG9zLnVuYW1lKCkubWFjaGluZSwgKE5vbmUsKSAqIDQpCiAgICAgaWYgbm90IGFyY2g6IHJldHVybiBOb25lCi0gICAgTEQsIEpFUSwgSkdFLCBSRVQsIEVSUiA9IDB4MjAsIDB4MTUsIDB4MzUsIDB4MDYsIDB4MDAwNTAwMDAKLSAgICBwcm9nID0gWyhMRCwgMCwgMCwgNCksIChKRVEsIDEsIDAsIGFyY2gpLCAoUkVULCAwLCAwLCBFUlIgfCBlcnJuby5FTk9TWVMpLAotICAgICAgICAgICAgKExELCAwLCAwLCAwKSwgKEpHRSwgNywgMCwgMHg0MDAwMDAwMCksIChKRVEsIDYsIDAsIDQyNSksIChKRVEsIDUsIDAsIDQyNiksIChKRVEsIDQsIDAsIDQyNyksCi0gICAgICAgICAgICAoSkVRLCAwLCAyLCBzb2NrKSwgKExELCAwLCAwLCAxNiksIChKRVEsIDIsIDAsIHNvY2tldC5BRl9WU09DSyBpZiBoYXNhdHRyKHNvY2tldCwgIkFGX1ZTT0NLIikgZWxzZSA0MCksCi0gICAgICAgICAgICAoUkVULCAwLCAwLCAweDdGRkYwMDAwKSwgKFJFVCwgMCwgMCwgRVJSIHwgZXJybm8uRU5PU1lTKSwgKFJFVCwgMCwgMCwgRVJSIHwgZXJybm8uRUFGTk9TVVBQT1JUKV0KKyAgICBMRCwgSkVRLCBKR0UsIEpTRVQsIFJFVCwgRVJSID0gMHgyMCwgMHgxNSwgMHgzNSwgMHg0NSwgMHgwNiwgMHgwMDA1MDAwMAorICAgICMgQSBid3JhcCB3aXRob3V0IGEgd29ya2luZyAtLWRpc2FibGUtdXNlcm5zIChVYnVudHUgMjIuMDQncyAwLjYuMSkgbGVhdmVzIG5vdGhpbmcgZWxzZSBzdG9wcGluZyBhbiBhZ2VudCBtYWtpbmcgYSBuZXN0ZWQgdXNlciBuYW1lc3BhY2UsIGFuZCB3aXRoIGl0IG1vcmUgb2YgdGhlIGtlcm5lbCB0byBhdHRhY2suCisgICAgIyBjbG9uZTMga2VlcHMgaXRzIGZsYWdzIGJlaGluZCBhIHBvaW50ZXIgYSBmaWx0ZXIgY2FuJ3QgcmVhZCwgc28gaXQncyByZWZ1c2VkIHdob2xlIGFuZCBsaWJjIGZhbGxzIGJhY2sgdG8gY2xvbmUuIGJ3cmFwIG1ha2VzIGl0cyBvd24gbmFtZXNwYWNlcyBiZWZvcmUgdGhpcyBmaWx0ZXIgbG9hZHMuCisgICAgb2xkID0gbm90IEZMQUdTLmdldCgibm91c2VybnMiLCBUcnVlKQorICAgIGNvZGUgPSBbKExELCAwLCAwLCA0KSwgKEpFUSwgIm5hdGl2ZSIsIDAsIGFyY2gpLCAoUkVULCAwLCAwLCBFUlIgfCBlcnJuby5FTk9TWVMpLCAibmF0aXZlIiwKKyAgICAgICAgICAgIChMRCwgMCwgMCwgMCksIChKR0UsICJub3N5cyIsIDAsIDB4NDAwMDAwMDApLCAoSkVRLCAibm9zeXMiLCAwLCA0MjUpLCAoSkVRLCAibm9zeXMiLCAwLCA0MjYpLCAoSkVRLCAibm9zeXMiLCAwLCA0MjcpLAorICAgICAgICAgICAgKihbKEpFUSwgIm5vc3lzIiwgMCwgNDM1KSwgKEpFUSwgImZsYWdzIiwgMCwgY2xvbmUpLCAoSkVRLCAiZmxhZ3MiLCAwLCB1bnNoYXJlKV0gaWYgb2xkIGVsc2UgW10pLAorICAgICAgICAgICAgKEpFUSwgMCwgImFsbG93Iiwgc29jayksIChMRCwgMCwgMCwgMTYpLCAoSkVRLCAiZmFtaWx5IiwgImFsbG93Iiwgc29ja2V0LkFGX1ZTT0NLIGlmIGhhc2F0dHIoc29ja2V0LCAiQUZfVlNPQ0siKSBlbHNlIDQwKSwKKyAgICAgICAgICAgICooWyJmbGFncyIsIChMRCwgMCwgMCwgMTYpLCAoSlNFVCwgInBlcm0iLCAiYWxsb3ciLCAweDEwMDAwMDAwKSwgInBlcm0iLCAoUkVULCAwLCAwLCBFUlIgfCBlcnJuby5FUEVSTSldIGlmIG9sZCBlbHNlIFtdKSwKKyAgICAgICAgICAgICJhbGxvdyIsIChSRVQsIDAsIDAsIDB4N0ZGRjAwMDApLCAibm9zeXMiLCAoUkVULCAwLCAwLCBFUlIgfCBlcnJuby5FTk9TWVMpLCAiZmFtaWx5IiwgKFJFVCwgMCwgMCwgRVJSIHwgZXJybm8uRUFGTk9TVVBQT1JUKV0KKyAgICBwcm9nLCBhdCA9IFtdLCB7fQorICAgIGZvciBpIGluIGNvZGU6CisgICAgICAgIGlmIGlzaW5zdGFuY2UoaSwgc3RyKTogYXRbaV0gPSBsZW4ocHJvZykKKyAgICAgICAgZWxzZTogcHJvZy5hcHBlbmQoaSkKKyAgICBwcm9nID0gWyhjLCBhdFt0XSAtIG4gLSAxIGlmIHQgZWxzZSAwLCBhdFtmXSAtIG4gLSAxIGlmIGYgZWxzZSAwLCBrKSBmb3IgbiwgKGMsIHQsIGYsIGspIGluIGVudW1lcmF0ZShwcm9nKV0KICAgICBkYXRhID0gYiIiLmpvaW4oc3RydWN0LnBhY2soIj1IQkJJIiwgKmkpIGZvciBpIGluIHByb2cpCiAgICAgcCA9IFNUQVRFIC8gInNhbmRib3guYnBmIgogICAgIHRyeToKQEAgLTI5NSw2ICszMDUsOSBAQCBkZWYgY2hlY2soKToKICAgICAgICAgICAgICAgICAgInRyeTogcy5jb25uZWN0KCgnMS4xLjEuMScsNTMpKVxuIgogICAgICAgICAgICAgICAgICAiZXhjZXB0IE9TRXJyb3I6IHBhc3NcbiIKICAgICAgICAgICAgICAgICAgImVsc2U6IHN5cy5leGl0KCdpdCByZWFjaGVzIHRoZSBuZXR3b3JrIGRpcmVjdGx5JylcbiIKKyAgICAgICAgICAgICAgICAgInRyeTogaW1wb3J0IGN0eXBlczsgbWFkZT1jdHlwZXMuQ0RMTChOb25lKS51bnNoYXJlKDB4MTAwMDAwMDApPT0wXG4iCisgICAgICAgICAgICAgICAgICJleGNlcHQgKEltcG9ydEVycm9yLE9TRXJyb3IpOiBtYWRlPUZhbHNlXG4iCisgICAgICAgICAgICAgICAgICJpZiBtYWRlOiBzeXMuZXhpdCgnaXQgY2FuIG1ha2UgYSB1c2VyIG5hbWVzcGFjZScpXG4iCiAgICAgICAgICAgICAgICAgICJvcGVuKG9zLnBhdGguZXhwYW5kdXNlcignfi9vaycpLCd3JylcbiIpCiAgICAgICAgIGVudiA9IHsiUEFUSCI6IG9zLmVudmlyb24uZ2V0KCJQQVRIIiwgIi91c3IvYmluOi9iaW4iKX0KICAgICAgICAgYSwgZSA9IHdyYXAoW19weXRob24oKSwgIi1JIiwgIi1jIiwgcHJvYmVdLCBlbnYsICJjaGVjayIsIGQgLyAiaG9tZSIsIGQgLyAic29jayIpCg==' | base64 -d > /tmp/sandbox.diff
if patch -p1 --dry-run -d "$HH/core" < /tmp/sandbox.diff >/dev/null; then patch -p1 -d "$HH/core" < /tmp/sandbox.diff >/dev/null; chown hive: "$HH/core/sandbox.py"; ok "patched sandbox.py"; else bad "patch did not apply"; fi
as "XDG_RUNTIME_DIR=/run/user/$U systemctl --user restart hive-starter"; sleep 3
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
