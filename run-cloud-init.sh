#!/usr/bin/env bash
# Boots a stock Ubuntu 24.04 cloud image with cloud-init.yaml as its user data (plus a throwaway login for the test),
# then checks what a person sees: the welcome message, the app reachable through an SSH forward and only that way.
set -u
SITE=${HIVE_SITE:-https://hivelab.ca}; fail=0
ok() { echo "ok   $1"; }; bad() { echo "FAIL $1"; fail=1; }
W=$(mktemp -d); cd "$W"; R="$OLDPWD"
curl -fsSL -o base.img https://cloud-images.ubuntu.com/noble/current/noble-server-cloudimg-amd64.img
qemu-img create -q -f qcow2 -F qcow2 -b base.img vm.qcow2 20G
ssh-keygen -q -t ed25519 -N '' -f key
sed "s|<THIS-SITE>|$SITE|g" "$R/cloud-init.yaml" > ci.yaml
python3 - <<PY
import yaml
d = yaml.safe_load(open("ci.yaml"))
d["users"] = [{"name": "tester", "sudo": "ALL=(ALL) NOPASSWD:ALL", "shell": "/bin/bash", "ssh_authorized_keys": [open("key.pub").read().strip()]}]
open("user-data", "w").write("#cloud-config\n" + yaml.safe_dump(d))
PY
printf 'instance-id: deploy\nlocal-hostname: deploy\n' > meta-data
cloud-localds seed.iso user-data meta-data
qemu-system-x86_64 -enable-kvm -cpu host -smp 2 -m 4096 -nographic -serial file:serial.log -monitor none \
  -drive file=vm.qcow2,if=virtio -drive file=seed.iso,if=virtio,format=raw \
  -netdev user,id=n,hostfwd=tcp:127.0.0.1:2222-:22 -device virtio-net-pci,netdev=n &
Q=$!; trap 'kill $Q 2>/dev/null || true' EXIT
S=(ssh -i key -p 2222 -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR -o ConnectTimeout=5 tester@127.0.0.1)
for i in $(seq 90); do "${S[@]}" true 2>/dev/null && break; sleep 5; [ "$i" = 90 ] && { tail -n 40 serial.log; echo "FAIL the VM did not answer"; exit 1; }; done
"${S[@]}" 'sudo cloud-init status --wait' | tail -n 2
"${S[@]}" 'sudo cloud-init status --long | head -n 8'
for i in $(seq 60); do "${S[@]}" 'test -s /etc/hive-ready' 2>/dev/null && break; sleep 5; done
r=$("${S[@]}" 'cat /etc/hive-ready' 2>&1); echo "ready file: $r"
case "$r" in ok\ *) ok "install finished, port ${r#ok }";; *) bad "install: $r"; "${S[@]}" 'sudo tail -n 40 /var/log/hive-install.log' | cut -c1-200;; esac
P=${r#ok }
m=$("${S[@]}" 'run-parts /etc/update-motd.d 2>/dev/null | sed -n "/Hive/,\$p"'); echo "$m"
echo "$m" | grep -q "ssh -L $P:127.0.0.1:$P" && ok "welcome message has the forward command" || bad "welcome message"
echo "$m" | grep -qE '/login#' && bad "welcome message shows the key" || ok "welcome message does not show the key"
"${S[@]}" 'ls -l /var/log/hive-install.log; swapon --show --noheadings; free -m | head -2'
U=$("${S[@]}" 'sudo -iu hive hive web 2>/dev/null' | grep -o 'http://[^ ]*' | head -1); [ -n "$U" ] && ok "sudo -iu hive hive web prints the address" || bad "no address"
for s in hive-starter hive-dash; do st=$("${S[@]}" "sudo -u hive XDG_RUNTIME_DIR=/run/user/\$(id -u hive) systemctl --user is-active $s"); [ "$st" = active ] && ok "$s active" || bad "$s: $st"; done
"${S[@]}" 'sudo ss -ltnp | grep -v "^State"' | cut -c1-120
"${S[@]}" 'sudo ss -ltnH' | awk '{print $4}' | grep -E '^(0\.0\.0\.0|\[::\]|\*):' | grep -vE ':22$' && bad "something besides SSH listens on all addresses" || ok "only SSH listens on all addresses"
# From "the laptop": the forward, then the page, as the welcome message says.
ssh -i key -p 2222 -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR -N -L "$P:127.0.0.1:$P" tester@127.0.0.1 &
F=$!; trap 'kill $Q $F 2>/dev/null || true' EXIT; sleep 3
c=$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$P/api/board"); [ "$c" = 401 ] && ok "through the forward, no key: refused" || bad "no key through the forward: $c"
c=$(curl -s -o /dev/null -w '%{http_code}' "${U%%#*}"); [ "$c" = 200 ] && ok "through the forward, the login page loads" || bad "login page: $c"
curl -s "${U%%#*}" | grep -qi "hive" && ok "the page is the Hive's" || bad "page content"
# Reaching the server's own address from outside the loopback must fail: the app is not on the network.
c=$(curl -s -m 5 -o /dev/null -w '%{http_code}' "http://127.0.0.1:$P/api/board" -H 'Host: evil.example'); echo "--   foreign Host header: $c"
[ "$fail" = 0 ] && echo PASS || echo FAILED
exit $fail
