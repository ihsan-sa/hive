#!/usr/bin/env bash
# Runs as root. A real old Hive (the real v0 release, its real installer and daemon, seeded data), then the candidate's installer over it.
# CASE: own = the old Hive in a lived-in account (.ssh), new one goes in its own user.
#       sys = the old Hive in a dedicated user under its real system service, new one in the same user; the first run fails halfway, the rerun finishes.
#       this = --this-account in the same lived-in account as a plain user without sudo for the install.
set -u
: "${CASE:?}" "${CAND:?}"
SITE=https://hive.ihsan.cc; RAW="https://raw.githubusercontent.com/ihsan-sa/hive/$CAND"; fail=0
ok() { echo "ok   $1"; }; bad() { echo "FAIL $1"; fail=1; }
note() { echo "--   $1"; }
hide() { sed -E 's/#[A-Za-z0-9_-]{20,}/#SECRET/'; }
yes_if() { if eval "$2" >/dev/null 2>&1; then ok "$1"; else bad "$1"; fi; }
no_if() { if eval "$2" >/dev/null 2>&1; then bad "$1"; else ok "$1"; fi; }

printf '#!/bin/sh\ncase "${1:-}" in auth) echo "{\\"loggedIn\\": false}";; --version) echo 0;; esac\nexit 0\n' > /usr/local/bin/claude; chmod +x /usr/local/bin/claude
apt-get update -qq >/dev/null 2>&1; apt-get install -y -qq git python3 curl >/dev/null 2>&1
curl -fsSL "$SITE/v1/install.sh" -o /tmp/v0-install.sh; chmod 644 /tmp/v0-install.sh

seed() {  # $1 user, $2 its home: what an old Hive leaves. The daemon writes its own files on top.
  su - "$1" -c 'set -e; r=$HOME/hive; mkdir -p "$r/projects/one" "$r/projects/two" "$r/.hive/kb/isolated/one" "$r/.hive/chats" "$r/.hive/logins" "$r/.hive/private" "$HOME/.config/hive"
    printf "%s\n" "$r" > "$HOME/.config/hive/workspace"
    echo SEEDSECRET-LOGIN > "$r/.hive/logins/x"; echo SEEDSECRET-KEY > "$r/.hive/private/key"; echo a note > "$r/.hive/kb/a.md"; echo walled > "$r/.hive/kb/isolated/one/n.md"
    printf "%s\n" "{\"id\":\"1\",\"from\":\"p\",\"name\":\"Pat\",\"text\":\"hello\",\"at\":\"2026-01-01T00:00:00Z\"}" > "$r/.hive/chats/main.jsonl"
    printf "%s\n" "{\"seq\":1,\"at\":\"2026-01-01T00:00:00Z\",\"kind\":\"project.created\",\"data\":{\"name\":\"one\",\"goals\":\"do one\"}}" "{\"seq\":2,\"at\":\"2026-01-01T00:00:00Z\",\"kind\":\"project.created\",\"data\":{\"name\":\"two\",\"goals\":\"do two\"}}" >> "$r/.hive/events.jsonl"
    cd "$r/projects/one"; git init -q -b main; echo work > f; git add f; git -c user.name=t -c user.email=t@t commit -qm one; git remote add origin https://x:SEEDSECRET-TOK@example.com/a/one.git
    echo two > "$r/projects/two/f"'
}
up() { pgrep -u "$1" -f '\.local/bin/hive(-v0)? daemon|hive-v0 daemon' >/dev/null; }

case "$CASE" in
  own|this) A=tester; H=/home/tester
    su - $A -c 'HIVE_SHARE=no sh /tmp/v0-install.sh' 2>&1 | tail -n 2
    seed $A $H
    U=$(id -u $A); loginctl enable-linger $A; for i in 1 2 3 4 5 6 7 8 9 10; do [ -d /run/user/$U ] && break; sleep 1; done
    su - $A -c 'mkdir -p ~/.config/systemd/user; printf "[Unit]\nDescription=The Hive\n\n[Service]\nEnvironment=HIVE_HOME=%s/hive\nExecStart=%s/.local/bin/hive daemon\nKillMode=process\nRestart=on-failure\nRestartSec=10\n\n[Install]\nWantedBy=default.target\n" "$HOME" "$HOME" > ~/.config/systemd/user/hive.service; XDG_RUNTIME_DIR=/run/user/$(id -u) systemctl --user daemon-reload; XDG_RUNTIME_DIR=/run/user/$(id -u) systemctl --user enable --now hive.service' 2>&1 | tail -n 2
    ;;
  sys) A=worker; H=/home/worker; useradd -m -s /bin/bash $A
    (cd /tmp && HIVE_USER=$A HIVE_SHARE=no sh /tmp/v0-install.sh) 2>&1 | tail -n 3
    systemctl stop hive-$A.service; seed $A $H; systemctl start hive-$A.service ;;
esac
sleep 5
if up $A; then ok "the real old Hive is running"; else note "old daemon not running: $(tail -n 3 $H/hive/.hive/*.log 2>/dev/null | cut -c1-200)"; fi
note "old folder: $(ls -A $H/hive/.hive | tr '\n' ' ')"
grep -aqs hive-standalone-build $H/.local/bin/hive && ok "old program carries the marker" || bad "old program marker"
sums() { (cd "$1" && find projects .hive/kb -type f -not -path '*/.git/*' -print0 | sort -z | xargs -0 sha256sum); }
sums $H/hive > /tmp/sums0

run() { # $1 log; the rest is the environment and arguments
  log=$1; shift; "$@" > "$log" 2>&1; rc=$?; hide < "$log" | tail -n 30 | sed 's/^/   | /'; return $rc; }

# What's in the old account isn't believed, and root does nothing in there.
repo=$(ls -d $H/hive/projects/*/.git 2>/dev/null | head -n 1)
if [ -n "$repo" ]; then
  printf '#!/bin/sh\ntouch /tmp/hook-ran\n' > $repo/hooks/post-checkout; chmod 755 $repo/hooks/post-checkout
  git --git-dir=$repo config core.fsmonitor /tmp/evil-monitor; git --git-dir=$repo config http.extraheader 'AUTHORIZATION: SEEDSECRET-PLAIN'
  chown -R $A: $repo; note "planted a hook in $repo"
else bad "no repo to plant a hook in"; fi
case "$CASE" in
  sys)
    echo keep > /etc/hive-victim; chmod 644 /etc/hive-victim
    mkdir -p /root/secretdir; echo ROOTSECRET > /root/secretdir/f; chmod 755 /root; chmod 755 /root/secretdir
    su -s /bin/sh $A -c "ln -sf /etc/hive-victim $H/hive/.hive/v2-import; ln -s /root/secretdir $H/hive/projects/linked"
    # Only root can read this one, so it stops first and nothing changes.
    F=$H/hive/projects/rootonly; echo x > $F; chown root: $F; chmod 600 $F
    curl -fsSL "$RAW/get.sh" -o /tmp/get-pre.sh; chmod 644 /tmp/get-pre.sh
    if run /tmp/outp env HIVE_LOG=/tmp/op.log HIVE_ACCEPT=1 HIVE_BRANCH=$CAND HIVE_USER=$A sh /tmp/get-pre.sh; then bad "a file only root can read should stop it"; else
      grep -q "can't read" /tmp/outp && grep -q 'Nothing changed' /tmp/outp && ok "file only root can read: stops first" || { bad "unreadable message"; tail -n 5 /tmp/outp; }
      up $A && ok "still running after that stop" || bad "old Hive stopped by the preflight"
    fi
    rm -f $F ;;
  own)
    # A half-done v2.3.0 run left this as root's.
    printf pending > $H/hive/.hive/v2-import; chown root: $H/hive/.hive/v2-import ;;
esac

curl -fsSL "$RAW/get.sh" -o /tmp/get.sh; chmod 644 /tmp/get.sh

case "$CASE" in
  own)
    if run /tmp/out1 su - $A -c "HIVE_LOG=/tmp/o.log HIVE_ACCEPT=1 HIVE_BRANCH=$CAND sh /tmp/get.sh"; then ok "install over it"; else bad "install over it"; hide < /tmp/o.log | tail -n 20; fi
    N=hive; NH=/home/hive; OLDF=$H/hive ;;
  this)
    if run /tmp/out1 su - $A -c "HIVE_LOG=/tmp/o.log HIVE_ACCEPT=1 HIVE_BRANCH=$CAND sh /tmp/get.sh --this-account"; then ok "install over it, this account"; else bad "install over it, this account"; hide < /tmp/o.log | tail -n 20; fi
    N=$A; NH=$H/hive; OLDF=$(ls -d $H/hive-old-* 2>/dev/null | head -n 1); [ -n "$OLDF" ] && ok "old folder renamed" || bad "old folder renamed" ;;
  sys)
    # A failure partway: reading the old things breaks after the old Hive has been stopped.
    # At the real path, not on PATH: get.sh reads the old account through su and env -i.
    mv /usr/bin/tar /usr/bin/tar.real; cat > /usr/bin/tar <<'S'
#!/bin/sh
case "$*" in *.checkouts*) echo "tar: pretend disk trouble" >&2; touch /tmp/tar-failed; chmod 666 /tmp/tar-failed; exit 2;; esac
exec /usr/bin/tar.real "$@"
S
    chmod 755 /usr/bin/tar
    if run /tmp/out0 env HIVE_LOG=/tmp/o0.log HIVE_ACCEPT=1 HIVE_BRANCH=$CAND HIVE_USER=$A sh /tmp/get.sh; then bad "a tar failure should fail the run"; else ok "the run fails"; fi
    mv -f /usr/bin/tar.real /usr/bin/tar
    [ -e /tmp/tar-failed ] && ok "the failure really fired" || bad "the fake tar never fired"
    grep -q 'installed\|Hive is installed' /tmp/out0 && ! grep -q 'to keep it to yourself' /tmp/out0 || true
    grep -q '/tmp/o0.log' /tmp/out0 && ok "failure names the log" || bad "failure names the log"
    no_if "old Hive stopped after the failure" "up $A"
    note "after failure: $(ls -A $H | tr '\n' ' ')"
    OLDF=$(ls -d $H/hive-old-* 2>/dev/null | head -n 1); [ -n "$OLDF" ] || OLDF=$H/hive
    note "state: $(cat "$OLDF/.hive/v2-import" 2>/dev/null)"; [ "$(cat "$OLDF/.hive/v2-import" 2>/dev/null)" = pending ] && ok "marked pending" || bad "marked pending"
    if run /tmp/out1 env HIVE_LOG=/tmp/o.log HIVE_ACCEPT=1 HIVE_BRANCH=$CAND HIVE_USER=$A sh /tmp/get.sh; then ok "rerun finishes"; else bad "rerun finishes"; hide < /tmp/o.log | tail -n 20; fi
    grep -q 'Carrying on with the old Hive' /tmp/out1 && ok "rerun says it is carrying on" || bad "rerun says it is carrying on"
    N=$A; NH=$H; OLDF=$(ls -d $H/hive-old-* 2>/dev/null | head -n 1); [ -n "$OLDF" ] && ok "old folder renamed" || bad "old folder renamed" ;;
esac

grep -q 'Brought across from the old Hive' /tmp/out1 && ok "summary shown" || bad "summary shown"
[ "$(grep -c 'localhost:[0-9]*/login#' /tmp/out1)" = 1 ] && ok "one address, on localhost" || bad "one address on localhost: $(grep -c 'localhost:[0-9]*/login#' /tmp/out1)"
for f in /tmp/o.log /tmp/o0.log; do [ -e "$f" ] || continue; no_if "address not in $f" "grep -q '/login#' $f"; [ "$(stat -c %a $f)" = 600 ] && ok "$f is private" || bad "$f mode $(stat -c %a $f)"; done
no_if "no secrets in the output" "grep -l SEEDSECRET /tmp/out1 /tmp/o.log /tmp/o0.log /tmp/out0"

sleep 5; no_if "old Hive stopped" "up $A"
no_if "old program gone" "grep -aqs hive-standalone-build $H/.local/bin/hive"
[ ! -e $H/.config/systemd/user/hive.service ] && ok "old user unit gone" || bad "old user unit gone"
[ ! -e /etc/systemd/system/hive-$A.service ] && ok "old system unit gone" || bad "old system unit gone"
# The running old Hive may write its own files after the snapshot, so: what's different is shown, and nothing that was there may be lost, and what was seeded stays the same.
sums "$OLDF" > /tmp/sums1; note "backup differences: $(diff /tmp/sums0 /tmp/sums1 | grep '^[<>]' | cut -c1-2,68- | tr '\n' ' ')"
lost=$(awk '{print $2}' /tmp/sums0 | while read -r f; do [ -e "$OLDF/$f" ] || echo "$f"; done)
[ -z "$lost" ] && ok "backup lost nothing" || bad "backup lost: $lost"
seeded=$(grep -E ' (projects/one/f|projects/two/.*|\.hive/kb/a\.md|\.hive/kb/isolated/one/n\.md)$' /tmp/sums0)
[ -n "$seeded" ] && (cd "$OLDF" && printf '%s\n' "$seeded" | sha256sum -c --quiet) && ok "seeded files unchanged in the backup" || bad "seeded files changed in the backup"
[ "$(cat "$OLDF/.hive/v2-import" 2>/dev/null)" = done ] && ok "marked done" || bad "marked done"

yes_if "project one moved" "[ -f $NH/projects/one/f ]"
yes_if "project two moved" "[ -f $NH/projects/two/f ]"
yes_if "goals kept" "grep -rqs 'do one' $NH/projects/one"
yes_if "commit kept" "[ \"\$(git -c safe.directory='*' -C $NH/projects/one log --format=%s)\" = one ]"
no_if "token out of the remote" "git -c safe.directory='*' -C $NH/projects/one config --get remote.origin.url | grep -q SEEDSECRET"
yes_if "notes moved" "[ -f $NH/kb/old-hive/a.md ] || grep -rqs 'a note' $NH/kb"
yes_if "chats kept" "[ -d $NH/kb/old-hive/chats ]"
no_if "no secret came across" "grep -rqs SEEDSECRET --exclude-dir=hive-old-* --exclude-dir=.git $NH/kb $NH/projects $NH/.hive"
[ "$(stat -c %U $NH/projects/one/f)" = "$N" ] && ok "files belong to $N" || bad "files belong to $(stat -c %U $NH/projects/one/f)"
[ "$(stat -c %a $(getent passwd $N | cut -d: -f6))" = 700 ] && note "home is 700" || note "home is $(stat -c %a $(getent passwd $N | cut -d: -f6))"

# The new Hive works
NU=$(id -u $N); for s in hive-starter hive-dash; do st=$(su - $N -c "XDG_RUNTIME_DIR=/run/user/$NU systemctl --user is-active $s"); [ "$st" = active ] && ok "$s active" || { bad "$s: $st"; su - $N -c "XDG_RUNTIME_DIR=/run/user/$NU journalctl --user -u $s -n 12 --no-pager" 2>&1 | tail -n 12 | hide; }; done
ha=$(su - $N -c 'command -v hive'); note "hive is $ha"; su - $N -c 'head -c 200 "$(command -v hive)" | grep -aq hive-standalone-build' && bad "hive still the old one" || ok "hive is the new one"
su - $N -c 'hive post --thread vmprobe hello' >/dev/null 2>&1 && ok "post" || bad "post"
su - $N -c 'hive web' 2>/dev/null | grep -q 'http://localhost:[0-9]*/login#' && ok "hive web prints localhost" || bad "hive web prints localhost"
wp=$(su - $N -c 'hive web' 2>/dev/null | sed -E 's#.*localhost:([0-9]+)/.*#\1#')
for host in localhost 127.0.0.1; do c=$(curl -s -o /dev/null -w '%{http_code}' "http://$host:$wp/api/board"); [ "$c" = 401 ] && ok "app answers on $host" || bad "app on $host: $c"; done

# Again: nothing moves twice, nothing breaks
before=$(find $NH/projects -type f -not -path '*/.git/*' | wc -l)
case "$CASE" in
  own) run /tmp/out2 su - $A -c "HIVE_LOG=/tmp/o.log HIVE_ACCEPT=1 HIVE_BRANCH=$CAND sh /tmp/get.sh" ;;
  this) run /tmp/out2 su - $A -c "HIVE_LOG=/tmp/o.log HIVE_ACCEPT=1 HIVE_BRANCH=$CAND sh /tmp/get.sh --this-account" ;;
  sys) run /tmp/out2 env HIVE_LOG=/tmp/o.log HIVE_ACCEPT=1 HIVE_BRANCH=$CAND HIVE_USER=$A sh /tmp/get.sh ;;
esac && ok "third run" || bad "third run"
no_if "third run brings nothing" "grep -q 'Found an old Hive' /tmp/out2"
[ "$(find $NH/projects -type f -not -path '*/.git/*' | wc -l)" = "$before" ] && ok "same files" || bad "files changed"
case "$CASE" in
  sys)
    [ "$(cat /etc/hive-victim)" = keep ] && ok "a link as the state file didn't write through" || bad "system file overwritten"
    ! grep -rqs ROOTSECRET $NH/projects $NH/kb && ok "nothing from elsewhere came over a link" || bad "link followed"
    systemctl cat hive-$A.service >/dev/null 2>&1 || true ;;
  this)
    [ "$(stat -c %a $NH)" = 700 ] && ok "folder is 0700" || bad "folder mode $(stat -c %a $NH)" ;;
esac
r=$(ls -d $NH/projects/*/.git 2>/dev/null | head -n 1)
[ -n "$r" ] && [ ! -e $r/hooks/post-checkout ] && ok "hook stayed behind" || bad "hook came over"
[ ! -e /tmp/hook-ran ] && ok "hook never ran" || bad "hook ran"
[ -n "$r" ] && [ -z "$(git --git-dir=$r config core.fsmonitor)" ] && ok "command setting stayed behind" || bad "fsmonitor came over"
! grep -rqs SEEDSECRET-PLAIN $NH/projects && ok "plain extraheader stayed behind" || bad "plain extraheader came over"
[ "$fail" = 0 ] && echo PASS || echo FAILED
exit $fail
