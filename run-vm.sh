#!/usr/bin/env bash
# Boots a stock cloud image under QEMU/KVM, copies inside.sh in, runs it as root. Throwaway keys, no logins, public log.
set -eu
W=$(mktemp -d); cd "$W"; R="$OLDPWD"
curl -fsSL -o base.img "$IMAGE_URL"
qemu-img create -q -f qcow2 -F "$(qemu-img info --output=json base.img | python3 -c 'import json,sys;print(json.load(sys.stdin)["format"])')" -b base.img vm.qcow2 20G
ssh-keygen -q -t ed25519 -N '' -f key
cat > user-data <<UD
#cloud-config
users:
  - {name: tester, sudo: "ALL=(ALL) NOPASSWD:ALL", shell: /bin/bash, ssh_authorized_keys: ["$(cat key.pub)"]}
UD
printf 'instance-id: clean\nlocal-hostname: clean\n' > meta-data
cloud-localds seed.iso user-data meta-data
qemu-system-x86_64 -enable-kvm -cpu host -smp 3 -m 6144 -nographic -serial file:serial.log -monitor none \
  -drive file=vm.qcow2,if=virtio -drive file=seed.iso,if=virtio,format=raw \
  -netdev user,id=n,hostfwd=tcp:127.0.0.1:2222-:22 -device virtio-net-pci,netdev=n &
Q=$!; trap 'kill $Q 2>/dev/null || true; [ -f serial.log ] && [ "${SHOW:-}" = 1 ] && tail -n 40 serial.log' EXIT
S=(ssh -i key -p 2222 -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR -o ConnectTimeout=5 tester@127.0.0.1)
for i in $(seq 90); do "${S[@]}" true 2>/dev/null && break; sleep 5; [ "$i" = 90 ] && { SHOW=1; echo "FAIL the VM did not answer"; exit 1; }; done
"${S[@]}" 'sudo cloud-init status --wait >/dev/null 2>&1 || true; systemctl is-system-running --wait >/dev/null 2>&1 || true'
"${S[@]}" 'cat > /tmp/inside.sh' < "$R/inside.sh"
"${S[@]}" 'sudo env HIVE_SITE='"${HIVE_SITE:-https://hive.ihsan.cc}"' HIVE_BRANCH='"${HIVE_BRANCH:-}"' bash /tmp/inside.sh'
