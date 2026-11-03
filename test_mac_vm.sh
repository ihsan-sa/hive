#!/usr/bin/env bash
# The inside of the Mac version, for real: the same VM setup as get-mac.sh, under QEMU with KVM on Linux, with Lima for Linux.
# What this can't show is anything of the Mac: Apple's framework, launchd, its sleep. Those are in mac-check.sh.
# Needs: limactl on PATH, qemu, /dev/kvm. Takes about 10 minutes. Runs on a CI machine, as it starts a VM. HIVE_SITE as in get-mac.sh.
set -u
D="$(cd "$(dirname "$0")" && pwd)"
export LIMA_HOME="${LIMA_HOME:-$HOME/.hive-test-lima}"
SITE="${HIVE_SITE:?set HIVE_SITE to the site that serves install.sh}"
VM=hive
fail=0; ok() { echo "ok   $1"; }; bad() { echo "FAIL $1"; fail=1; }
yes_if() { if "${@:2}" >/dev/null 2>&1; then ok "$1"; else bad "$1"; fi; }
no_if() { if "${@:2}" >/dev/null 2>&1; then bad "$1"; else ok "$1"; fi; }
vm() { limactl shell --workdir / "$VM" "$@"; }
as_hive() { vm sudo -iu hive bash -c "$1"; }

HIVE_VMTYPE=qemu sh "$D/get-mac.sh" --print-yaml > "$LIMA_HOME.yaml"
# Under QEMU, Lima's host resolver redirects DNS to a high port on 192.168.5.2, which the block rejects. On Apple's framework DNS goes to 192.168.5.3:53 directly, so use QEMU's own DNS at that address to match it.
printf 'hostResolver:\n  enabled: false\ndns: [192.168.5.3]\n' >> "$LIMA_HOME.yaml"
limactl create --tty=false --name="$VM" "$LIMA_HOME.yaml" || exit 1
limactl start --tty=false "$VM" || { tail -n 40 "$LIMA_HOME/$VM/ha.stderr.log"; exit 1; }
ok "the VM starts"
yes_if "Ubuntu 24.04 with systemd" vm sh -c '. /etc/os-release; [ "$VERSION_ID" = 24.04 ] && [ -d /run/systemd/system ]'

# Before anything is installed: what the setup alone gives.
vm sudo useradd -m zz-probe
yes_if "a non-root user: the Mac's localhost is blocked" sh -c "! limactl shell --workdir / $VM sudo -u zz-probe timeout 5 bash -c 'exec 3<>/dev/tcp/192.168.5.2/22'"
yes_if "a non-root user: DNS still works" vm sudo -u zz-probe getent hosts github.com
yes_if "a non-root user: the internet still works" vm sudo -u zz-probe curl -fsS -m 20 -o /dev/null https://github.com
no_if "no folder of the host" vm sh -c "mount | grep -E '$HOME|virtiofs|9p|sshfs'"
limactl stop "$VM" >/dev/null && limactl start --tty=false "$VM" >/dev/null
yes_if "after a restart: the block is back" sh -c "! limactl shell --workdir / $VM sudo -u zz-probe timeout 5 bash -c 'exec 3<>/dev/tcp/192.168.5.2/22'"
yes_if "after a restart: Lima still reaches the VM" vm true

# Forwarding: the app's range comes through; nothing else does.
listen() { limactl shell --workdir / "$VM" sudo -u zz-probe sh -c "nohup timeout 40 python3 -m http.server $1 --bind $2 >/dev/null 2>&1 &"; }
listen 8090 127.0.0.1; listen 18090 127.0.0.1; listen 18091 0.0.0.0; sleep 15
yes_if "an app port reaches the host" curl -fsS -m 5 -o /dev/null http://127.0.0.1:8090/
no_if "another port doesn't, from the VM's localhost" curl -fsS -m 5 -o /dev/null http://127.0.0.1:18090/
no_if "another port doesn't, from all addresses" curl -fsS -m 5 -o /dev/null http://127.0.0.1:18091/

# The normal install, as get-mac.sh runs it.
# shellcheck disable=SC2016
vm sudo env HIVE_BRANCH="${HIVE_BRANCH:-}" SITE="$SITE" sh -c '[ -n "$HIVE_BRANCH" ] || unset HIVE_BRANCH; f=$(mktemp); curl -fsSL -o "$f" "$SITE/install.sh" && sh "$f"' </dev/null
rc=$?; yes_if "the normal Linux install works in the VM" test "$rc" = 0
yes_if "starter is up" as_hive 'systemctl --user is-active hive-starter'
yes_if "app is up" as_hive 'systemctl --user is-active hive-dash'
yes_if "the updater is on" as_hive 'systemctl --user is-active hive-update.timer'
no_if "the hive user has no sudo" as_hive 'sudo -n true'
no_if "the hive user can't reach the Mac's localhost" as_hive 'timeout 5 bash -c "exec 3<>/dev/tcp/192.168.5.2/22"'
address=$(as_hive 'hive web' 2>/dev/null | tr -d '\r' | sed -n 's/.*\(http:[^ ]*\).*/\1/p' | head -n 1)
echo "address: ${address%%#*}"
sleep 10
yes_if "the app answers on the host" curl -fsS -m 10 -o /dev/null "${address%%#*}"
as_hive 'cd ~/core && python3 -c "import sandbox, json; print(json.dumps(sandbox.check()))"' 2>/dev/null || echo "sandbox: this release has no sandbox check"
# A word with a space, through the same quoting the Mac's command uses.
yes_if "a spaced word survives to the VM" sh -c "limactl shell --workdir / $VM sudo -iu hive bash -c '[ \"\$1\" = \"two words\" ]' _ 'two words'"

# A sleep, as near as this gets: every VCPU stopped for 90 s. Then the guest agent should set the clock, and the boot clock shouldn't have moved.
pid=$(cat "$LIMA_HOME/$VM/qemu.pid")
gaps() { vm python3 -c 'import time; m=time.clock_gettime(time.CLOCK_MONOTONIC); print(round(time.clock_gettime(time.CLOCK_BOOTTIME)-m), round(time.time()-m))' | tr -d '\r'; }
read -r boot0 real0 <<<"$(gaps)"
kill -STOP "$pid"; sleep 90; kill -CONT "$pid"; sleep 30
read -r boot1 real1 <<<"$(gaps)"
echo "paused 90 s: boot clock moved $((boot1 - boot0)) s, wall clock moved $((real1 - real0)) s, VM vs host $(( $(vm date +%s | tr -d '\r') - $(date +%s) )) s"
yes_if "after a pause: the clock is right" test "$(( $(vm date +%s | tr -d '\r') - $(date +%s) ))" -le 3 -a "$(( $(date +%s) - $(vm date +%s | tr -d '\r') ))" -le 3
yes_if "after a pause: services still up" as_hive 'systemctl --user is-active hive-starter hive-dash'
yes_if "after a pause: the app answers on the host" curl -fsS -m 10 -o /dev/null "${address%%#*}"

limactl delete --force "$VM" >/dev/null
exit "$fail"
