#!/bin/sh
# Install the Hive on a Mac. One line:
#   curl -fsSL https://hivelab.ca/install-mac.sh | sh
# The Hive needs Linux with systemd, so this makes a small Linux VM (Lima, on Apple's Virtualization framework) and runs the normal
# Linux install inside it, as the Windows line does in WSL. The VM can't see your files: no folder of the Mac is shared with it.
# Run it again to update. Everything lives in ~/.hive. It uses only what every Mac has: no sudo, Homebrew, Python, or Apple's developer
# tools. Don't call git or python3 here: on a Mac without the tools they open Apple's install dialog.
# Everything is inside main, so a download cut short runs nothing.
set -eu

LIMA_VERSION=2.2.1
LIMA_SHA_ARM64=9e9eacce88f37e185c346bad73aa6136f738d8cdf8c3bb23cd42b071824bc66e
LIMA_SHA_X86_64=6b776e4bc41f358e6ee01fefcc78e2eb26f11635a0fcff632234af77a566c6c4
VM=hive
AGENT=hive.vm
MARK='# added by the Hive installer'
# Until a real Mac has passed mac-check.sh. Then this goes to yes, and get.sh hands over directly.
TESTED=no

die() { echo "hive: $*" >&2; exit 1; }

# An install that stops before Linux starts in the VM can be told to the Hive's makers, only with a yes: asked on the terminal, or HIVE_REPORT=1 where there's none (HIVE_REPORT=0 never asks). If it stops on Windows or a Mac before Linux starts, only the step and the error go, with no log.
# It holds only the stage that stopped, a fixed label for it, the exit code, the version and the OS family and chip, and no log, names, folders or printed text: this script keeps no log and has no scrub, so it sends nothing it didn't write itself.
# A stop inside the VM's own install is asked about by that install when it has a terminal, which the VM has not; then this asks, and sends the same fixed words. Wrong OS, an unknown option, sudo and the test-build gate are refusals, not failures: they offer nothing.
REPORT_URL="${HIVE_SHARE_URL:-https://hive-reports.ihsan.cc/v1/report}"
STOP=""; WHY=""
stage() { STOP=$1; WHY=$2; }
stop_report() {
  rc=$1
  case "$REPORT_URL" in http*) ;; *) return 0 ;; esac
  [ "${HIVE_REPORT:-}" != 0 ] || return 0
  # The VM's install was told to send (HIVE_REPORT=1) and sends its own.
  [ "$STOP" != vm-install ] || [ "${HIVE_REPORT:-}" != 1 ] || return 0
  if [ "${HIVE_REPORT:-}" != 1 ]; then
    ( : </dev/tty ) 2>/dev/null || return 0
    printf "Send this failure to the Hive's makers so they can fix it? It holds the installer version, the step that failed, a short error label, and the OS and chip type. No log, names or folders. Type y to send: " >/dev/tty
    read -r a </dev/tty || a=""
    case "$a" in y | Y | yes | Yes) ;; *) return 0 ;; esac
  fi
  case "$rc" in "" | *[!0-9]*) rc=1 ;; esac
  chip=$(uname -m 2>/dev/null | tr -cd 'a-z0-9_' | cut -c1-16); [ -n "$chip" ] || chip=unknown
  ref="i$(od -An -N8 -tx1 /dev/urandom | tr -d ' \n')"
  if curl -fsS -m 20 -H 'Content-Type: application/json' -d "{\"install\":\"$ref\",\"version\":\"0\",\"type\":\"failure\",\"kind\":\"install\",\"os\":\"mac/$chip\",\"message\":\"install stopped at $STOP: $WHY (exit $rc)\"}" "$REPORT_URL" >/dev/null 2>&1
  then echo "Sent, thank you. If you open an issue, this is its reference: $ref"
  else echo "Could not send it just now."; fi
}
on_exit() { rc=$?; trap - EXIT INT TERM HUP; [ -z "$STOP" ] || [ "$rc" = 0 ] || [ "$rc" -ge 128 ] || stop_report "$rc" || true; exit "$rc"; }
trap on_exit EXIT
trap 'exit 130' INT TERM HUP

# The VM. No mounts and no SSH keys from the Mac. Only the app's own ports reach the Mac, and only on its loopback.
# Provision runs at every boot. It stops ordinary users inside reaching Lima's addresses, since 192.168.5.2 is the Mac's own localhost.
# Replies stay open, because Lima's own connection comes in through that address; DNS stays open too, and root is left alone for Lima's agent.
vm_yaml() {
  cat <<Y
minimumLimaVersion: 2.0.0
base:
- template:_images/ubuntu-24.04
vmType: ${HIVE_VMTYPE:-vz}
cpus: $1
memory: "$2GiB"
disk: "60GiB"
mounts: []
# Lima's own DNS server panics ("recvfrom: socket is not connected", dns.go:379) and takes the host agent down, which hung the install on the Mac rig five runs in a row. The VM uses the Mac's network DNS instead.
hostResolver: {enabled: false}
containerd: {system: false, user: false}
ssh: {loadDotSSHPubKeys: false, forwardAgent: false, forwardX11: false}
portForwards:
- guestIP: 127.0.0.1
  guestPortRange: [8077, 8176]
  hostIP: 127.0.0.1
- guestIP: 0.0.0.0
  guestIPMustBeZero: false
  guestPortRange: [1, 65535]
  proto: any
  ignore: true
provision:
- mode: system
  script: |
    #!/bin/sh
    # Lima 2.2.1 on Ubuntu 24.04: ending the first login at boot can leave logind spinning. Logins then take
    # minutes and linger is lost, so the install's own linger fails too (lima-vm/lima#5730, reported on Apple silicon).
    # logind ignores SIGTERM in that state, and sessions survive the kill.
    if ! timeout 5 loginctl list-sessions >/dev/null 2>&1; then
      systemctl kill --signal=SIGKILL systemd-logind.service || true
      systemctl start systemd-logind || true
    fi
    loginctl enable-linger "\${LIMA_CIDATA_USER}" || true
- mode: system
  script: |
    #!/bin/bash
    set -eu
    command -v nft >/dev/null || { apt-get update -qq; DEBIAN_FRONTEND=noninteractive apt-get install -y -qq nftables; }
    nft -f - <<'N'
    table inet hive_mac
    delete table inet hive_mac
    table inet hive_mac {
      chain out {
        type filter hook output priority 0; policy accept;
        oifname "lo" accept
        ct state established,related accept
        meta skuid 0 accept
        ip daddr 192.168.5.0/24 th dport 53 accept
        ip daddr 192.168.5.0/24 reject
      }
    }
    N
    # Ubuntu 24.04 stops a normal user making a user namespace unless a profile allows it, and the sandbox around agents is made of them. This re-allows them for bubblewrap, with no capabilities for what runs inside. The same bytes as get.sh.
    if command -v apparmor_parser >/dev/null; then
      cat > /etc/apparmor.d/bwrap <<'A'
    # installed by the Hive
    abi <abi/4.0>,
    include <tunables/global>
    profile bwrap /usr/bin/bwrap flags=(attach_disconnected,mediate_deleted) {
      allow capability,
      allow file rwlkm /{**,},
      allow network,
      allow unix,
      allow ptrace,
      allow signal,
      allow mqueue,
      allow io_uring,
      allow userns,
      allow mount,
      allow umount,
      allow pivot_root,
      allow dbus,
      allow pix /** -> &bwrap//&unpriv_bwrap,
    }
    profile unpriv_bwrap flags=(attach_disconnected,mediate_deleted) {
      allow file rwlkm /{**,},
      allow network,
      allow unix,
      allow ptrace,
      allow signal,
      allow mqueue,
      allow io_uring,
      allow userns,
      allow mount,
      allow umount,
      allow pivot_root,
      allow dbus,
      allow pix /** -> &unpriv_bwrap,
      audit deny capability,
    }
    A
      apparmor_parser -r /etc/apparmor.d/bwrap || echo "couldn't load the bubblewrap profile: the sandbox check will say so" >&2
    fi
Y
}

# The command on the Mac: hive anything runs in the VM, starting it first if it's stopped.
wrapper() {
  cat <<'W'
#!/bin/sh
# The Hive runs in a Linux VM; this passes commands to it. Made by the Mac installer.
set -eu
export LIMA_HOME="$HOME/.hive/lima"
L="$HOME/.hive/lima-dist/bin/limactl"
up() { [ "$("$L" list hive --format '{{.Status}}' 2>/dev/null)" = Running ] || "$L" start --tty=false hive >&2; }
# Not sudo -i: it leaves each $ for the VM's login shell to expand, so a word like "costs $5" would change on the way.
# shellcheck disable=SC2016  # these expand in the VM
in_vm() { "$L" shell --workdir / hive sudo -u hive -H bash -lc 'cd; export XDG_RUNTIME_DIR=/run/user/$(id -u); exec "$@"' _ "$@"; }
address() { in_vm hive web 2>/dev/null | tr -d '\r' | sed -n 's/.*\(http:[^ ]*\).*/\1/p' | head -n 1; }
case "${1:-}" in
  vm)
    case "${2:-}" in
      start) up ;;
      stop) "$L" stop hive ;;
      status) "$L" list hive --format '{{.Status}}' ;;
      *) echo "hive vm start, stop or status: the Linux VM the Hive runs in." ;;
    esac
    ;;
  open)
    up; a=$(address)
    case "$a" in http://localhost*|http://127.0.0.1*) ;; *) echo "hive: no address yet. Try: hive web" >&2; exit 1 ;; esac
    # The VM can be up a while before the app answers, so wait for it rather than open a page that says it can't connect.
    base=$(printf '%s' "$a" | sed 's|^\(http://[^/]*\)/.*|\1/|'); n=0
    until curl -s -o /dev/null --max-time 2 "$base" || [ "$n" -ge 45 ]; do n=$((n + 1)); sleep 2; done
    open "$a"
    ;;
  *) up; in_vm hive "$@" ;;
esac
W
}

# A Hive icon in ~/Applications that runs hive open, so nobody needs Terminal to come back. Made here, on this Mac, so Gatekeeper has nothing to quarantine.
# The icon is taken from the VM and made into an .icns with the Mac's own sips and iconutil; if either fails, the app just has the plain icon.
make_app() {
  A="$HOME/Applications/Hive.app"
  [ ! -e "$A" ] || grep -qs 'made by the Hive install' "$A/Contents/MacOS/Hive" || { echo "$A is something else, so I left it."; return 0; }
  rm -rf "$A"; mkdir -p "$A/Contents/MacOS" "$A/Contents/Resources" || return 0
  printf '#!/bin/sh\n# made by the Hive install\nexec "%s/bin/hive" open\n' "$D" > "$A/Contents/MacOS/Hive"; chmod +x "$A/Contents/MacOS/Hive"
  cat > "$A/Contents/Info.plist" <<'PL'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleName</key><string>Hive</string>
  <key>CFBundleDisplayName</key><string>Hive</string>
  <key>CFBundleIdentifier</key><string>local.hive.launcher</string>
  <key>CFBundleExecutable</key><string>Hive</string>
  <key>CFBundleIconFile</key><string>Hive</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleVersion</key><string>1</string>
  <key>NSHighResolutionCapable</key><true/>
</dict>
</plist>
PL
  t=$(mktemp -d) && "$L" shell --workdir / "$VM" sudo -u hive -H bash -lc 'cat ~/modules/dash/web/icons/hive-1024.png' </dev/null > "$t/h.png" 2>/dev/null \
    && mkdir "$t/Hive.iconset" && for z in 16 32 64 128 256 512; do
      sips -z "$z" "$z" "$t/h.png" --out "$t/Hive.iconset/icon_${z}x${z}.png" >/dev/null 2>&1 || break
      sips -z $((z * 2)) $((z * 2)) "$t/h.png" --out "$t/Hive.iconset/icon_${z}x${z}@2x.png" >/dev/null 2>&1 || break
    done && iconutil -c icns "$t/Hive.iconset" -o "$A/Contents/Resources/Hive.icns" >/dev/null 2>&1 || true
  rm -rf "${t:-}"
  echo "There's now a Hive app in your Applications folder (in your home folder). Open it, or drag it to the Dock."
}

# Starts the VM when you log in, as the Startup link does on Windows. launchd reads it at the next login. Not "Background": that would slow the agents.
agent_plist() {
  cat <<P
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>$AGENT</string>
  <key>ProgramArguments</key>
  <array>
    <string>$D/lima-dist/bin/limactl</string>
    <string>start</string>
    <string>--tty=false</string>
    <string>--foreground</string>
    <string>$VM</string>
  </array>
  <key>EnvironmentVariables</key>
  <dict><key>LIMA_HOME</key><string>$D/lima</string></dict>
  <key>RunAtLoad</key><true/>
  <key>ProcessType</key><string>Standard</string>
  <key>StandardOutPath</key><string>$D/vm.log</string>
  <key>StandardErrorPath</key><string>$D/vm.log</string>
</dict>
</plist>
P
}

add_to_path() {
  line="export PATH=\"\$HOME/.hive/bin:\$PATH\" $MARK"
  files="$HOME/.zprofile"
  # bash reads the first of these it finds, so a new .bash_profile would hide someone's .profile.
  if [ -e "$HOME/.bash_profile" ] || [ ! -e "$HOME/.profile" ]; then files="$files $HOME/.bash_profile"; else files="$files $HOME/.profile"; fi
  for f in $files; do
    grep -qF "$MARK" "$f" 2>/dev/null || printf '%s\n' "$line" >> "$f"
  done
}

get_lima() {
  # Not uname -m: under Rosetta an Apple silicon Mac says x86_64.
  if [ "$(sysctl -n hw.optional.arm64 2>/dev/null || echo 0)" = 1 ]; then arch=arm64 sha=$LIMA_SHA_ARM64; else arch=x86_64 sha=$LIMA_SHA_X86_64; fi
  [ "$(cat "$D/lima-dist/.version" 2>/dev/null || true)" != "$LIMA_VERSION-$arch" ] || return 0
  echo "Getting Lima $LIMA_VERSION, which runs the VM..."
  t=$(mktemp -d "$D/get.XXXXXX")
  curl -fsSL -o "$t/lima.tgz" "https://github.com/lima-vm/lima/releases/download/v$LIMA_VERSION/lima-$LIMA_VERSION-Darwin-$arch.tar.gz" \
    || { rm -rf "$t"; stage lima-download "Lima download failed"; die "couldn't download Lima. Check your connection and run this again"; }
  [ "$(shasum -a 256 "$t/lima.tgz" | cut -d' ' -f1)" = "$sha" ] || { rm -rf "$t"; stage lima-checksum "Lima checksum mismatch"; die "the Lima download doesn't match its checksum, so I won't use it"; }
  mkdir "$t/x"; tar -xzf "$t/lima.tgz" -C "$t/x"
  echo "$LIMA_VERSION-$arch" > "$t/x/.version"
  # A running VM keeps its own copy in memory; the new one takes over at its next start.
  rm -rf "$D/lima-dist"; mv "$t/x" "$D/lima-dist"; rm -rf "$t"
}

main() {
  [ "${1:-}" != --print-yaml ] || { vm_yaml 4 4; exit 0; }
  [ $# = 0 ] || die "unknown option $1"
  [ "$(uname -s)" = Darwin ] || die "this is the Mac installer. On Linux: curl -fsSL https://hivelab.ca/install.sh | sh"
  [ "$TESTED" = yes ] || [ -n "${HIVE_MAC_TEST:-}" ] || die "the Mac version is a test build that has never run on a real Mac, so it may not work. To try it anyway:
  curl -fsSL https://hivelab.ca/install-mac.sh | HIVE_MAC_TEST=1 sh"
  [ "$(id -u)" != 0 ] || die "run this as yourself, not with sudo. It doesn't need sudo"
  major=$(sw_vers -productVersion | cut -d. -f1)
  case "$major" in "" | *[!0-9]*) die "couldn't read this Mac's macOS version" ;; esac
  [ "$major" -ge 13 ] || { stage macos-old "macOS older than 13"; die "this needs macOS 13 (Ventura) or newer, for Apple's Virtualization framework. This Mac has $(sw_vers -productVersion)"; }

  SITE="${HIVE_SITE:-https://hivelab.ca}"
  D="$HOME/.hive"
  mkdir -p "$D/bin"
  : > "$D/.hive-mac"
  export LIMA_HOME="$D/lima"
  L="$D/lima-dist/bin/limactl"
  stage lima "getting Lima failed"
  get_lima

  if [ ! -d "$LIMA_HOME/$VM" ]; then
    free_gb=$(( $(df -k "$HOME" | awk 'NR==2 {print $4}') / 1048576 ))
    [ "$free_gb" -ge 15 ] || { stage disk "not enough free disk"; die "this needs about 15 GB free on your disk, and there's $free_gb GB"; }
    # Half the Mac, within limits: 4 to 8 GB, 2 to 8 CPUs.
    mem=$(( $(sysctl -n hw.memsize) / 2147483648 )); [ "$mem" -ge 4 ] || mem=4; [ "$mem" -le 8 ] || mem=8
    cpus=$(( $(sysctl -n hw.ncpu) / 2 )); [ "$cpus" -ge 2 ] || cpus=2; [ "$cpus" -le 8 ] || cpus=8
    vm_yaml "$cpus" "$mem" > "$D/vm.yaml"
    echo "Making the Linux VM: $cpus CPUs, up to $mem GB of memory. The first time downloads about 600 MB..."
    stage vm-create "creating the VM failed"
    "$L" create --tty=false --name="$VM" "$D/vm.yaml"
  fi
  stage vm-start "starting the VM failed"
  [ "$("$L" list "$VM" --format '{{.Status}}' 2>/dev/null)" = Running ] || "$L" start --tty=false "$VM" \
    || die "the VM didn't start. Its log is in $LIMA_HOME/$VM/ha.stderr.log"

  # The normal Linux install, as root in the VM. Run again, it updates.
  stage vm-install "the install in the VM failed"
  echo "Installing the Hive in the VM. This can sit quiet for several minutes, so please leave it running..."
  # A VM made before its provisioning recovered logind, or one whose logind spun later: a rerun has to fix this itself
  # or it fails the same way, with "Failed to connect to bus" after the linger note (lima-vm/lima#5730).
  "$L" shell --workdir / "$VM" sudo sh -c 'timeout 5 loginctl list-sessions >/dev/null 2>&1 || { systemctl kill --signal=SIGKILL systemd-logind.service; systemctl start systemd-logind; }' </dev/null || true
  # shellcheck disable=SC2016  # these expand in the VM
  "$L" shell --workdir / "$VM" sudo env HIVE_INSIDE=1 HIVE_HOST=mac HIVE_REPORT="${HIVE_REPORT:-}" HIVE_BRANCH="${HIVE_BRANCH:-}" SITE="$SITE" sh -c \
    '[ -n "$HIVE_BRANCH" ] || unset HIVE_BRANCH; f=$(mktemp); curl -fsSL -o "$f" "$SITE/install.sh" && sh "$f"' </dev/null \
    || die "the install in the VM failed. Run this line again to retry"

  STOP=""
  wrapper > "$D/bin/hive"; chmod +x "$D/bin/hive"
  add_to_path
  mkdir -p "$HOME/Library/LaunchAgents"
  agent_plist > "$HOME/Library/LaunchAgents/$AGENT.plist"
  make_app

  # The web app can still be starting right after the install (the rig hit "no address" here), so ask again for a minute.
  address=""; n=0
  while [ -z "$address" ] && [ "$n" -lt 20 ]; do
    out=$("$D/bin/hive" web 2>&1 | tr -d '\r' || true)
    address=$(printf '%s\n' "$out" | sed -n 's/.*\(http:[^ ]*\).*/\1/p' | head -n 1)
    [ -n "$address" ] || { n=$((n + 1)); sleep 3; }
    # The VM's ssh can stay down after a heavy install ("Connection refused", rig run 42885823260). A restart of the
    # VM brings sshd back, and the install is on its disk, so do that once, after 30 s of it.
    if [ -z "$address" ] && [ "$n" -eq 10 ] && printf '%s' "$out" | grep -q 'Connection refused'; then
      echo "The VM isn't answering. Restarting it..." >&2
      "$L" stop "$VM" >/dev/null 2>&1 || true
      "$L" start --tty=false "$VM" >&2 || true
    fi
  done
  [ -n "$address" ] || echo "hive web said: $out" >&2
  [ -n "$address" ] || die "the Hive installed, but it gave no address. Try, in a new Terminal window: hive web"
  echo
  echo "The Hive is running in its own Linux VM, which can't see your files."
  echo "It starts when you log in. It pauses while this Mac sleeps, and carries on when it wakes."
  echo "Open it at: $address"
  echo "To open it again, click Hive in your Applications folder, or in a new Terminal window: hive open"
  case "$address" in
    http://localhost*|http://127.0.0.1*)
      # Lima forwards the port a moment after the app listens. If it never does, something on the Mac holds that port.
      n=0; while [ "$n" -lt 30 ] && ! curl -fsS -o /dev/null -m 2 "${address%%#*}" 2>/dev/null; do n=$((n + 1)); sleep 2; done
      if [ "$n" -lt 30 ]; then open "$address" || true
      else echo "That address doesn't answer on this Mac yet. If it stays that way, another program may be using its port."; fi
      ;;
  esac
}

main "$@"
