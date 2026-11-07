#!/usr/bin/env bash
# The Mac install and uninstall lines, with stubs: no VM, no network, nothing outside a throwaway folder. Runs on Linux and on a Mac.
# Real runs: test_mac_vm.sh (the VM side, under KVM) and mac-check.sh (a real Mac).
set -u
D="$(cd "$(dirname "$0")" && pwd)"; T=$(mktemp -d); trap 'rm -rf "$T"' EXIT; cd "$T" || exit 1
fail=0; ok() { echo "ok   $1"; }; bad() { echo "FAIL $1"; fail=1; }
yes_if() { if "${@:2}"; then ok "$1"; else bad "$1"; fi; }

B="$T/bin"; mkdir -p "$B"
stub() { printf '#!/bin/sh\n%s\n' "$2" > "$B/$1"; chmod +x "$B/$1"; }
stub uname 'case "${1:-}" in -m) echo "${FAKE_M:-arm64}" ;; *) echo "${FAKE_UNAME:-Darwin}" ;; esac'
stub id 'case "${1:-}" in -u) echo "${FAKE_UID:-501}" ;; *) /usr/bin/id "$@" ;; esac'
stub sw_vers 'echo "${FAKE_MACOS:-14.5}"'
stub sysctl 'case "$2" in hw.optional.arm64) [ "${FAKE_ARM:-1}" = none ] && { echo "unknown oid" >&2; exit 1; }; echo "${FAKE_ARM:-1}" ;; hw.memsize) echo "${FAKE_MEM:-17179869184}" ;; hw.ncpu) echo "${FAKE_CPU:-8}" ;; esac'
stub df 'printf "Filesystem 1024-blocks Used Available\n/dev/x 999 1 %s\n" "${FAKE_FREE_KB:-104857600}"'
stub shasum 'f=$3; [ -n "${FAKE_BADSUM:-}" ] && { echo "0000  $f"; exit 0; }; cat "$f.sum" | sed "s|\$|  $f|"'
stub open 'echo "open $*" >> "$TLOG"'
stub launchctl 'echo "launchctl $*" >> "$TLOG"'
stub sleep 'exit 0'
stub sips 'while [ $# -gt 0 ]; do [ "$1" = --out ] && : > "$2"; shift; done'
stub iconutil 'while [ $# -gt 0 ]; do [ "$1" = -o ] && : > "$2"; shift; done'
# These must never run: on a Mac without Apple's tools, python3 and git open an install dialog.
for n in python3 python git brew make cc; do stub "$n" 'echo "FORBIDDEN '"$n"' $*" >> "$TLOG"; exit 1'; done

# curl: a download writes a tarball holding a fake limactl and notes its URL. The site's check of the address answers unless told not to.
mkdir -p "$T/tar/bin"; cat > "$T/tar/bin/limactl" <<'L'
#!/bin/sh
echo "limactl LIMA_HOME=$LIMA_HOME $*" >> "$TLOG"
case "$1" in
  create) n=; for a in "$@"; do case "$a" in --name=*) n=${a#--name=} ;; esac; y=$a; done; mkdir -p "$LIMA_HOME/$n"; cp "$y" "$TLOG.yaml" ;;
  list) [ -e "$TLOG.running" ] && echo Running || echo Stopped ;;
  start) : > "$TLOG.running" ;;
  delete) rm -f "$TLOG.running"; rm -rf "${LIMA_HOME:?}/hive" ;;
  shell)
    for a in "$@"; do printf 'arg[%s]\n' "$a" >> "$TLOG.args"; done
    case "$*" in *" hive web"*) [ -n "${FAKE_NOADDR:-}" ] || printf 'http://%s/login#sekret\r\n' "${FAKE_ADDR:-localhost:8077}" ;; esac
    [ -z "${FAKE_INSTALL_FAILS:-}" ] || case "$*" in *install.sh*) exit 1 ;; esac
    # The copy out at uninstall: run its script in a fake VM home.
    case "$*" in *"tar -cf"*) [ -z "${FAKE_COPY_FAILS:-}" ] || exit 1; for a in "$@"; do s=$a; done; mkdir -p "$TLOG.vm"; (cd "$TLOG.vm" && HOME="$TLOG.vm" bash -c "$s") ;; esac
    ;;
esac
L
chmod +x "$T/tar/bin/limactl"; tar -czf "$T/lima.tgz" -C "$T/tar" bin
stub curl 'echo "curl $*" >> "$TLOG"
o=; prev=; for a in "$@"; do [ "$prev" = -o ] && o=$a; prev=$a; done
case "$*" in *github.com*) [ -z "${FAKE_NONET:-}" ] || exit 22; cp "'"$T"'/lima.tgz" "$o"; cp "'"$T"'/sum" "$o.sum"; exit 0 ;; esac
[ -z "${FAKE_NOANSWER:-}" ] || exit 7
exit 0'
# The hash the fake download should match: the one pinned for the chosen arch. shasum echoes it, unless FAKE_BADSUM.
pinned() { sed -n "s/^LIMA_SHA_$1=//p" "$D/get-mac.sh"; }

fresh() { H="$T/home$RANDOM$RANDOM"; mkdir -p "$H"; : > "$T/mark"; rm -f "$T/mark.args" "$T/mark.running" "$T/mark.yaml"; pinned "${1:-ARM64}" > "$T/sum"; }
run() { env TLOG="$T/mark" HOME="$H" PATH="$B:$PATH" HIVE_MAC_TEST=1 "$@" sh < "$D/get-mac.sh"; }
mark_has() { grep -q -- "$1" "$T/mark"; }
mark_lacks() { ! grep -q -- "$1" "$T/mark"; }

# Refusals: each stops before anything happens.
refuses() { # name, what the message must say, env...
  local name=$1 want=$2; shift 2; fresh
  local out; out=$(run env "$@" 2>&1); local rc=$?
  if [ "$rc" != 0 ] && echo "$out" | grep -q "$want" && mark_lacks '^limactl'; then ok "$name"; else bad "$name"; echo "$out"; fi
}
refuses "a test build: needs the opt-in, and says it isn't tested" 'never run on a real Mac' -u HIVE_MAC_TEST
refuses "Linux: points to the Linux line" 'install.sh' FAKE_UNAME=Linux
refuses "root: refuses" 'not with sudo' FAKE_UID=0
refuses "macOS 12: refuses and says 13" 'macOS 13' FAKE_MACOS=12.7.1
refuses "strange macOS version: refuses" 'version' FAKE_MACOS=abc
refuses "too little disk: says so" '15 GB' FAKE_FREE_KB=1048576

fresh; out=$(head -c 1500 "$D/get-mac.sh" | env TLOG="$T/mark" HOME="$H" PATH="$B:$PATH" sh 2>&1); yes_if "cut short: runs nothing" test ! -s "$T/mark" -a ! -e "$H/.hive"

# Which Lima.
fresh ARM64; run env FAKE_M=x86_64 >/dev/null 2>&1; yes_if "Apple silicon under Rosetta gets the arm64 Lima" mark_has 'Darwin-arm64.tar.gz'
fresh X86_64; run env FAKE_ARM=none FAKE_M=x86_64 >/dev/null 2>&1; yes_if "Intel gets the x86_64 Lima" mark_has 'Darwin-x86_64.tar.gz'
fresh X86_64; run env FAKE_ARM=0 FAKE_M=x86_64 >/dev/null 2>&1; yes_if "Intel, where the setting exists and is 0" mark_has 'Darwin-x86_64.tar.gz'
fresh; out=$(run env FAKE_BADSUM=1 2>&1); rc=$?
if [ "$rc" != 0 ] && echo "$out" | grep -q checksum && [ ! -e "$H/.hive/lima-dist" ] && mark_lacks '^limactl' && [ -z "$(find "$H/.hive" -name "get.*")" ]; then ok "bad checksum: not used, nothing left"; else bad "bad checksum"; echo "$out"; fi
fresh; out=$(run env FAKE_NONET=1 2>&1); rc=$?; if [ "$rc" != 0 ] && echo "$out" | grep -q connection && [ -z "$(find "$H/.hive" -name "get.*")" ]; then ok "no network: says so, nothing left"; else bad "no network"; echo "$out"; fi

# A first install.
fresh; out=$(run env 2>&1); rc=$?
if [ "$rc" = 0 ]; then ok "first install succeeds"; else bad "first install: $rc"; echo "$out"; fi
Y="$T/mark.yaml"
yes_if "uses its own Lima home" mark_has "LIMA_HOME=$H/.hive/lima "
yes_if "VM: Apple's framework" grep -q '^vmType: vz$' "$Y"
yes_if "VM: no folder of the Mac" grep -q '^mounts: \[\]$' "$Y"
yes_if "VM: no SSH keys or agent from the Mac" grep -q 'loadDotSSHPubKeys: false, forwardAgent: false' "$Y"
yes_if "VM: only the app's ports, then nothing" grep -q 'guestPortRange: \[8077, 8176\]' "$Y"
yes_if "VM: everything else is ignored" grep -q 'ignore: true' "$Y"
yes_if "VM: blocks the way to the Mac's localhost" grep -q 'ip daddr 192.168.5.0/24 reject' "$Y"
yes_if "VM: half of 16 GB" grep -q 'memory: "8GiB"' "$Y"
yes_if "runs the normal install as root in the VM" grep -q 'arg\[sudo\]' "$T/mark.args"
yes_if "the install comes from the site" grep -q 'install.sh' "$T/mark.args"
yes_if "opens the Mac's browser at the address" mark_has 'open http://localhost:8077/login#sekret'
yes_if "checks the address without its secret" mark_has 'curl.*localhost:8077/login$'
yes_if "prints the address" grep -q 'http://localhost:8077/login#sekret' <<<"$out"
yes_if "never runs python, git, brew or a compiler" mark_lacks FORBIDDEN
yes_if "no sudo on the Mac itself" test ! -e "$B/sudo"
yes_if "the command is there" test -x "$H/.hive/bin/hive"
A="$H/Applications/Hive.app"
yes_if "the Hive app is made in ~/Applications" test -x "$A/Contents/MacOS/Hive"
yes_if "the app runs hive open" grep -qF "exec \"$H/.hive/bin/hive\" open" "$A/Contents/MacOS/Hive"
yes_if "the app names itself Hive, with an icon file" sh -c "grep -q '<string>Hive</string>' '$A/Contents/Info.plist' && grep -q CFBundleIconFile '$A/Contents/Info.plist'"
yes_if "the app has its icon" test -e "$A/Contents/Resources/Hive.icns"
yes_if "it tells the person where the app is" grep -q 'Hive app in your Applications' <<<"$out"
yes_if "PATH in zsh's file" grep -qF '.hive/bin' "$H/.zprofile"
yes_if "PATH in bash's file" grep -qF '.hive/bin' "$H/.bash_profile"
P="$H/Library/LaunchAgents/hive.vm.plist"
yes_if "starts at login" grep -q 'RunAtLoad' "$P"
yes_if "login start finds its own Lima" grep -q "<string>$H/.hive/lima</string>" "$P"
yes_if "login start isn't slowed as a background job" grep -q '<string>Standard</string>' "$P"
if command -v plutil >/dev/null 2>&1; then yes_if "plist is valid" plutil -lint "$P"; fi

# The same line again: updates, doesn't remake.
: > "$T/mark"; out=$(run env 2>&1); rc=$?
yes_if "again: succeeds" test "$rc" = 0
yes_if "again: same VM" mark_lacks ' create '
yes_if "again: no new Lima" mark_lacks 'github.com'
yes_if "again: runs the install in the VM, which updates" grep -q 'install.sh' "$T/mark.args"
yes_if "again: PATH added once" test "$(grep -cF '.hive/bin' "$H/.zprofile")" = 1
: > "$T/mark"; rm -f "$H/.hive/lima-dist/.version"; run env >/dev/null 2>&1; yes_if "a new pinned Lima is fetched on the next run" mark_has 'github.com'

# The address, and failures after the VM is up.
fresh; run env FAKE_ADDR=example.com >/dev/null 2>&1; yes_if "doesn't open an address that isn't on this Mac" mark_lacks '^open'
fresh; out=$(run env FAKE_NOANSWER=1 2>&1); if mark_lacks '^open' && grep -q 'using its port' <<<"$out"; then ok "address doesn't answer: says so"; else bad "address doesn't answer"; echo "$out"; fi
fresh; out=$(run env FAKE_NOADDR=1 2>&1); rc=$?; if [ "$rc" != 0 ] && grep -q 'no address' <<<"$out"; then ok "no address: says so"; else bad "no address"; echo "$out"; fi
fresh; out=$(run env FAKE_INSTALL_FAILS=1 2>&1); rc=$?; if [ "$rc" != 0 ] && grep -q 'install in the VM failed' <<<"$out"; then ok "install fails in the VM: says so"; else bad "install fails"; echo "$out"; fi

# Limits on the VM's size.
fresh; run env FAKE_MEM=4294967296 FAKE_CPU=2 >/dev/null 2>&1; yes_if "small Mac: 4 GB" grep -q 'memory: "4GiB"' "$T/mark.yaml"; yes_if "small Mac: 2 CPUs" grep -q 'cpus: 2$' "$T/mark.yaml"
fresh; run env FAKE_MEM=137438953472 FAKE_CPU=32 >/dev/null 2>&1; yes_if "big Mac: 8 GB" grep -q 'memory: "8GiB"' "$T/mark.yaml"; yes_if "big Mac: 8 CPUs" grep -q 'cpus: 8$' "$T/mark.yaml"

# Someone's own shell files.
fresh; printf 'mine\n' > "$H/.profile"; run env >/dev/null 2>&1
yes_if "a .profile isn't hidden behind a new .bash_profile" test ! -e "$H/.bash_profile"
yes_if "a .profile gets the PATH line" grep -qF '.hive/bin' "$H/.profile"
yes_if "and keeps what was there" grep -qx mine "$H/.profile"

# The command.
fresh; run env >/dev/null 2>&1; : > "$T/mark"; rm -f "$T/mark.args"
hv() { env TLOG="$T/mark" HOME="$H" PATH="$B:$PATH" "$H/.hive/bin/hive" "$@"; }
hv ask "two words" '$(boom)' >/dev/null
yes_if "hive: runs in the VM as the hive user" grep -q 'arg\[hive\]' "$T/mark.args"
yes_if "hive: a spaced word stays one word" grep -qF 'arg[two words]' "$T/mark.args"
yes_if "hive: nothing is expanded on the Mac" grep -qF 'arg[$(boom)]' "$T/mark.args"
yes_if "hive: the VM's login shell gets no word to expand" grep -qF 'sudo -u hive -H bash -lc' "$H/.hive/bin/hive"
yes_if "mac-check: the report never gets the path of the home folder" sh -c "! grep -E 'say \\"[^\\"\\$]*\\\$(R|HOME)' '$D/mac-check.sh'"
yes_if "mac-check: a check keeps its label" sh -c 'eval "$(grep -E "^(say|pass|bad|check)\(\)" "$0")"; R=/dev/null; fails=0; f() { n=5; }; check "my label" f | grep -q "ok    my label"' "$D/mac-check.sh"
# The app wakes a stopped Hive and waits for it to answer, then opens the address.
: > "$T/mark"; env TLOG="$T/mark" HOME="$H" PATH="$B:$PATH" "$H/Applications/Hive.app/Contents/MacOS/Hive" >/dev/null 2>&1; yes_if "the app opens the Hive" mark_has 'open http://localhost:8077/login#sekret'
yes_if "hive open waits for the app to answer first" sh -c "grep -q 'curl -s -o /dev/null --max-time 2' '$H/.hive/bin/hive'"
printf '#!/bin/sh\necho mine\n' > "$T/other"; mkdir -p "$T/ho/Applications/Hive.app/Contents/MacOS"; cp "$T/other" "$T/ho/Applications/Hive.app/Contents/MacOS/Hive"
H0="$H"; H="$T/ho"; run env >/dev/null 2>&1; yes_if "a Hive.app that isn't ours is left alone" grep -qx 'echo mine' "$H/Applications/Hive.app/Contents/MacOS/Hive"; H="$H0"
rm -f "$T/mark.running"; : > "$T/mark"; hv web >/dev/null 2>&1; yes_if "hive: starts a stopped VM first" mark_has ' start '
: > "$T/mark"; hv open >/dev/null 2>&1; yes_if "hive open: the Mac's browser" mark_has 'open http://localhost:8077/login#sekret'

# Uninstall. It reads the word from a terminal, so give it one, in each system's way.
if script -qc true /dev/null >/dev/null 2>&1; then pty() { script -qec "$1" /dev/null; }; else pty() { script -q /dev/null sh -c "$1"; }; fi
# The word comes after a pause and the pipe stays open: a word typed before the terminal is ready can be lost, and BSD script passes the end of the pipe on as an end of input.
rm_it() { { sleep 1; printf '%s\n' "$@"; sleep 1; } | env TLOG="$T/mark" HOME="$H" PATH="$B:$PATH" bash -c "$(declare -f pty); pty 'sh $D/remove-mac.sh'" 2>&1; }

fresh; out=$(env TLOG="$T/mark" HOME="$H" PATH="$B:$PATH" sh "$D/remove-mac.sh" 2>&1); rc=$?
if [ "$rc" != 0 ] && grep -q 'nothing changed' <<<"$out"; then ok "uninstall with no Hive here: nothing changed"; else bad "uninstall with no Hive"; echo "$out"; fi
out=$(env TLOG="$T/mark" HOME="$H" PATH="$B:$PATH" FAKE_UNAME=Linux sh "$D/remove-mac.sh" 2>&1); [ $? != 0 ] && grep -q 'uninstall.sh' <<<"$out" && ok "uninstall on Linux: points to the Linux line" || bad "uninstall on Linux"

fresh; printf 'mine\n' > "$H/.zprofile"; run env >/dev/null 2>&1; mkdir -p "$H/.lima/own" "$H/Documents"; echo keep > "$H/Documents/f"; : > "$T/mark"
if out=$(rm_it '' '' '' no); [ -d "$H/.hive" ] && mark_lacks ' delete ' && grep -q 'nothing changed' <<<"$out"; then ok "uninstall: any other word changes nothing"; else bad "uninstall: other word"; echo "$out"; fi
rm -rf "$T/mark.vm"; mkdir -p "$T/mark.vm/projects/one" "$T/mark.vm/kb" "$T/mark.vm/core"; echo work > "$T/mark.vm/projects/one/f"; echo note > "$T/mark.vm/kb/n.md"; echo '{}' > "$T/mark.vm/board.jsonl"
if out=$(export FAKE_COPY_FAILS=1; rm_it '' '' '' delete); [ -d "$H/.hive" ] && mark_lacks ' delete ' && grep -q 'nothing was deleted' <<<"$out"; then ok "uninstall: the copy out fails: nothing deleted"; else bad "uninstall: copy fails"; echo "$out"; fi
rm -rf "$H"/hive-kept-*
out=$(rm_it '' y '' delete); rc=$?
K="$H/hive-kept-$(date +%Y%m%d)"
yes_if "uninstall: keeps projects and board by default, out of the VM" test "$(cat "$K/projects/one/f" 2>/dev/null)" = work -a -e "$K/board.jsonl"
yes_if "uninstall: y deletes the notes; the Hive's own code isn't kept" test ! -e "$K/kb" -a ! -e "$K/core" -a ! -e "$K/.copy.tar"
yes_if "uninstall: says where they are" grep -q "Kept in $K: projects and board" <<<"$out"
yes_if "uninstall: copies before the VM goes" sh -c "grep -n 'shell\\|delete --force' '$T/mark' | tail -2 | head -1 | grep -q shell"
grep -q 'gone from this Mac' <<<"$out" || { bad "uninstall: says it's done"; echo "$out"; }
yes_if "uninstall: deletes the VM" mark_has "limactl LIMA_HOME=$H/.hive/lima delete --force hive"
yes_if "uninstall: stops the login start" mark_has 'launchctl bootout gui/501/hive.vm'
yes_if "uninstall: the Hive app gone" test ! -e "$H/Applications/Hive.app"
yes_if "uninstall: login start gone" test ! -e "$H/Library/LaunchAgents/hive.vm.plist"
yes_if "uninstall: its folder gone" test ! -e "$H/.hive"
if grep -qF '.hive/bin' "$H/.zprofile" "$H/.bash_profile"; then bad "uninstall: PATH line left"; else ok "uninstall: PATH lines gone"; fi
yes_if "uninstall: the rest of the shell file stays" grep -qx mine "$H/.zprofile"
yes_if "uninstall: your own Lima stays" test -d "$H/.lima/own"
yes_if "uninstall: your files stay" test -e "$H/Documents/f"

# A stop before Linux starts: reported only on a yes, with fixed words and no log.
posts() { grep -c -- "-d .*\"kind\":\"install\"" "$T/mark"; }
U=http://reports.test/r
fresh; run env HIVE_SHARE_URL=$U FAKE_FREE_KB=1048576 setsid </dev/null >/dev/null 2>&1; yes_if "stop, no terminal, no HIVE_REPORT: sends nothing" test "$(posts)" = 0
fresh; run env HIVE_SHARE_URL=$U FAKE_FREE_KB=1048576 HIVE_REPORT=1 >/dev/null 2>&1; yes_if "stop with HIVE_REPORT=1: sends once" test "$(posts)" = 1
yes_if "report: kind install, os mac/chip, the step, an exit code" mark_has '"kind":"install","os":"mac/arm64","message":"install stopped at disk: not enough free disk (exit 1)"'
yes_if "report: no log key, no install id of the Hive" bash -c "! grep -q '\"log\"' '$T/mark' && grep -q '\"install\":\"i[0-9a-f]\\{16\\}\"' '$T/mark'"
fresh; run env HIVE_SHARE_URL=$U FAKE_FREE_KB=1048576 HIVE_REPORT=0 >/dev/null 2>&1; yes_if "HIVE_REPORT=0: sends nothing" test "$(posts)" = 0
fresh; run env FAKE_FREE_KB=1048576 HIVE_REPORT=1 >/dev/null 2>&1; yes_if "address not filled in: sends nothing" test "$(posts)" = 0
for r in 'FAKE_UNAME=Linux' 'FAKE_UID=0' 'FAKE_MACOS=abc' '-u HIVE_MAC_TEST'; do
  fresh; run env HIVE_SHARE_URL=$U HIVE_REPORT=1 $r >/dev/null 2>&1; yes_if "refusal ($r): offers and sends nothing" test "$(posts)" = 0
done
fresh; run env HIVE_SHARE_URL=$U FAKE_INSTALL_FAILS=1 HIVE_REPORT=1 >/dev/null 2>&1; yes_if "VM install fails with HIVE_REPORT=1: this wrapper sends nothing (the install sends its own)" test "$(posts)" = 0
fresh; run env HIVE_SHARE_URL=$U FAKE_INSTALL_FAILS=1 >/dev/null 2>&1; yes_if "VM install fails, no terminal: sends nothing" test "$(posts)" = 0
# GNU script and timeout only: BSD script (macOS) has other flags, so this check is skipped there
if command -v timeout >/dev/null 2>&1 && script -qec true /dev/null >/dev/null 2>&1; then
  for ans in y n ''; do
    fresh; printf '%s\n' "$ans" | env TLOG="$T/mark" HOME="$H" PATH="$B:$PATH" HIVE_MAC_TEST=1 HIVE_SHARE_URL=$U FAKE_FREE_KB=1048576 timeout 15 script -qec "sh $D/get-mac.sh" /dev/null >"$T/tty.out" 2>&1
    want=0; [ "$ans" = y ] && want=1
    yes_if "terminal answer '$ans': $want report(s), one question" test "$(posts)" = "$want" -a "$(grep -c 'Send this failure' "$T/tty.out")" = 1
  done
fi
fresh; run env >/dev/null 2>&1; yes_if "HIVE_HOST=mac goes into the VM's install" grep -q 'HIVE_HOST=mac' "$T/mark.args"

# The syntax Macs run, and lint where it's installed.
yes_if "mac-check: the wake step reports tries, and the why-dump goes to the report" sh -c 'grep -q "tries" "$0" && grep -q "why_no_answer()" "$0" && grep -q "say \"      in the VM" "$0"' "$D/mac-check.sh"
yes_if "mac-check: answers_soon counts tries" sh -c 'eval "$(grep -E "^answers_soon\(\)" "$0")"; n=0; answers() { n=$((n+1)); [ $n -ge 3 ]; }; sleep() { :; }; answers_soon && [ "$tries" = 3 ]' "$D/mac-check.sh"
yes_if "mac-check: the forced fault runs only with AUTO and FAULT, after the pause, before the app check" sh -c 'grep -n "break_forward ||" "$0" | grep -q "AUTO.*FAULT" && [ "$(grep -n "break_forward ||" "$0" | cut -d: -f1)" -lt "$(grep -n "answers_soon 30; then pass" "$0" | cut -d: -f1)" ]' "$D/mac-check.sh"
for f in get-mac.sh remove-mac.sh mac-check.sh; do yes_if "$f parses" sh -n "$D/$f"; done
if command -v shellcheck >/dev/null 2>&1; then yes_if "shellcheck" shellcheck -S warning -s sh "$D/get-mac.sh" "$D/remove-mac.sh" "$D/mac-check.sh"; fi
exit "$fail"
