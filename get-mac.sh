#!/bin/sh
# Install the Hive on a Mac. One line:
#   curl -fsSL https://hive.ihsan.cc/install-mac.sh | sh
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
in_vm() { "$L" shell --workdir / hive sudo -iu hive "$@"; }
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
    case "$a" in http://127.0.0.1*) open "$a" ;; *) echo "hive: no address yet. Try: hive web" >&2; exit 1 ;; esac
    ;;
  *) up; in_vm hive "$@" ;;
esac
W
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
    || { rm -rf "$t"; die "couldn't download Lima. Check your connection and run this again"; }
  [ "$(shasum -a 256 "$t/lima.tgz" | cut -d' ' -f1)" = "$sha" ] || { rm -rf "$t"; die "the Lima download doesn't match its checksum, so I won't use it"; }
  mkdir "$t/x"; tar -xzf "$t/lima.tgz" -C "$t/x"
  echo "$LIMA_VERSION-$arch" > "$t/x/.version"
  # A running VM keeps its own copy in memory; the new one takes over at its next start.
  rm -rf "$D/lima-dist"; mv "$t/x" "$D/lima-dist"; rm -rf "$t"
}

main() {
  [ "${1:-}" != --print-yaml ] || { vm_yaml 4 4; exit 0; }
  [ $# = 0 ] || die "unknown option $1"
  [ "$(uname -s)" = Darwin ] || die "this is the Mac installer. On Linux: curl -fsSL https://hive.ihsan.cc/install.sh | sh"
  [ "$TESTED" = yes ] || [ -n "${HIVE_MAC_TEST:-}" ] || die "the Mac version is a test build that has never run on a real Mac, so it may not work. To try it anyway:
  curl -fsSL https://hive.ihsan.cc/install-mac.sh | HIVE_MAC_TEST=1 sh"
  [ "$(id -u)" != 0 ] || die "run this as yourself, not with sudo. It doesn't need sudo"
  major=$(sw_vers -productVersion | cut -d. -f1)
  case "$major" in "" | *[!0-9]*) die "couldn't read this Mac's macOS version" ;; esac
  [ "$major" -ge 13 ] || die "this needs macOS 13 (Ventura) or newer, for Apple's Virtualization framework. This Mac has $(sw_vers -productVersion)"

  SITE="${HIVE_SITE:-https://hive.ihsan.cc}"
  D="$HOME/.hive"
  mkdir -p "$D/bin"
  : > "$D/.hive-mac"
  export LIMA_HOME="$D/lima"
  L="$D/lima-dist/bin/limactl"
  get_lima

  if [ ! -d "$LIMA_HOME/$VM" ]; then
    free_gb=$(( $(df -k "$HOME" | awk 'NR==2 {print $4}') / 1048576 ))
    [ "$free_gb" -ge 15 ] || die "this needs about 15 GB free on your disk, and there's $free_gb GB"
    # Half the Mac, within limits: 4 to 8 GB, 2 to 8 CPUs.
    mem=$(( $(sysctl -n hw.memsize) / 2147483648 )); [ "$mem" -ge 4 ] || mem=4; [ "$mem" -le 8 ] || mem=8
    cpus=$(( $(sysctl -n hw.ncpu) / 2 )); [ "$cpus" -ge 2 ] || cpus=2; [ "$cpus" -le 8 ] || cpus=8
    vm_yaml "$cpus" "$mem" > "$D/vm.yaml"
    echo "Making the Linux VM: $cpus CPUs, up to $mem GB of memory. The first time downloads about 600 MB..."
    "$L" create --tty=false --name="$VM" "$D/vm.yaml"
  fi
  [ "$("$L" list "$VM" --format '{{.Status}}' 2>/dev/null)" = Running ] || "$L" start --tty=false "$VM" \
    || die "the VM didn't start. Its log is in $LIMA_HOME/$VM/ha.stderr.log"

  # The normal Linux install, as root in the VM. Run again, it updates.
  echo "Installing the Hive in the VM..."
  # shellcheck disable=SC2016  # these expand in the VM
  "$L" shell --workdir / "$VM" sudo env HIVE_BRANCH="${HIVE_BRANCH:-}" SITE="$SITE" sh -c \
    '[ -n "$HIVE_BRANCH" ] || unset HIVE_BRANCH; f=$(mktemp); curl -fsSL -o "$f" "$SITE/install.sh" && sh "$f"' </dev/null \
    || die "the install in the VM failed. Run this line again to retry"

  wrapper > "$D/bin/hive"; chmod +x "$D/bin/hive"
  add_to_path
  mkdir -p "$HOME/Library/LaunchAgents"
  agent_plist > "$HOME/Library/LaunchAgents/$AGENT.plist"

  address=$("$D/bin/hive" web 2>/dev/null | tr -d '\r' | sed -n 's/.*\(http:[^ ]*\).*/\1/p' | head -n 1)
  [ -n "$address" ] || die "the Hive installed, but it gave no address. Try, in a new Terminal window: hive web"
  echo
  echo "The Hive is running in its own Linux VM, which can't see your files."
  echo "It starts when you log in. It pauses while this Mac sleeps, and carries on when it wakes."
  echo "Open it at: $address"
  echo "To open it again, in a new Terminal window: hive open"
  case "$address" in
    http://127.0.0.1*)
      # Lima forwards the port a moment after the app listens. If it never does, something on the Mac holds that port.
      n=0; while [ "$n" -lt 30 ] && ! curl -fsS -o /dev/null -m 2 "${address%%#*}" 2>/dev/null; do n=$((n + 1)); sleep 2; done
      if [ "$n" -lt 30 ]; then open "$address" || true
      else echo "That address doesn't answer on this Mac yet. If it stays that way, another program may be using its port."; fi
      ;;
  esac
}

main "$@"
