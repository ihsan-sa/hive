#!/bin/sh
# The installer's clean-machine test, inside the container run.sh starts. As
# root it creates users, plants links into /etc and rewrites system files, so
# it refuses to run anywhere but that container.
#
# It installs the Hive for a worker user as root, reruns the installer, takes
# one ask to a merge (fake harness, local forge), then checks what the
# installer's security reads asked for: root writes into the worker's home only
# as the worker and stops on a failed write, through links the worker plants;
# token files start empty, root's and 0600; a rerun naming another repo is
# refused, by a fixed-string match; units and records are keyed by the user
# name alone, so repo-like names can't collide; and the service unit is
# sandboxed.
set -eu
if ! { [ -f /.dockerenv ] || [ -f /run/.containerenv ]; } || [ "${HIVE_INSTALL_TEST:-}" != container ] || [ "$(id -u)" != 0 ]; then
  echo "in-container.sh: runs only as root in the container tests/install/run.sh starts" >&2
  exit 2
fi

fail() { echo "FAIL: $*" >&2; exit 1; }
ok() { echo "ok: $*"; }
W=/home/worker
ETC=/etc/hive/worker
UNIT=/etc/systemd/system/hive-worker.service

# install [VAR=value ...]: the installer as root for worker; its output goes to /tmp/out.
install_as_root() { env HIVE_USER=worker HIVE_SHARE=no "$@" sh /stage/install.sh >/tmp/out 2>&1; }
as_worker() { runuser -u worker -- env -i HOME=$W PATH=$W/.local/bin:/usr/bin:/bin HIVE_FORGE=local:$W/forge "$@"; }
mode() { stat -c '%U:%G %a %s' "$1"; }
# snap lists everything outside the worker's home with its owner, mode, link
# target and content hash, so two snaps differ if anything there changed.
snap() {
  prune='( -path /proc -o -path /sys -o -path /dev -o -path /run -o -path /tmp -o -path /var/tmp -o -path /stage -o -path /home/worker )'
  # shellcheck disable=SC2086 # prune is split into find's words on purpose
  {
    find / -xdev $prune -prune -o -printf '%p %U %G %m %l\n'
    find / -xdev $prune -prune -o -type f -print0 | xargs -0 sha256sum
  } | LC_ALL=C sort >"$1"
}

install -m 755 /stage/curl /usr/local/bin/curl
useradd -m -s /bin/sh worker

# 1. Install, then rerun.
install_as_root || { cat /tmp/out; fail "the first install"; }
[ "$(stat -c %U $W/.local/bin/hive)" = worker ] || fail "the binary isn't the worker's"
as_worker hive --version >/dev/null || fail "the worker can't run its hive"
ok "installed for worker"
[ "$(mode $ETC/tokens/ihsan-sa.token)" = "root:root 600 0" ] || fail "token file: $(mode $ETC/tokens/ihsan-sa.token)"
[ "$(stat -c "%U:%G %a" $ETC)" = "root:root 700" ] || fail "records folder: $(mode $ETC)"
if as_worker cat $ETC/tokens/ihsan-sa.token >/dev/null 2>&1; then fail "the worker reads root's token file"; fi
[ "$(cat $ETC/repo)" = ihsan-sa/hive ] || fail "repo record: $(cat $ETC/repo)"
ok "token file starts empty, root's and 0600; the repo is recorded"
for want in User=worker "ExecStart=$W/.local/bin/hive daemon" ProtectSystem=strict ProtectHome=tmpfs "BindPaths=$W" PrivateTmp=yes NoNewPrivileges=yes \
  "LoadCredential=ihsan-sa.token:$ETC/tokens/ihsan-sa.token"; do
  grep -qxF "$want" $UNIT || fail "the unit lacks $want"
done
mkdir -p /run/systemd # systemd-analyze wants it; nothing boots systemd here
systemd-analyze verify $UNIT || fail "systemd-analyze verify"
ok "the unit runs as worker, sandboxed, and verifies"
[ "$(ls $W/.local/bin)" = hive ] || fail "the installer put more than hive in the worker's bin: $(ls $W/.local/bin)"
if command -v claude >/dev/null; then fail "a claude is installed"; fi
ok "the installer copies no binary of its own (claude or other) to the worker"

echo "the person's token" >$ETC/tokens/ihsan-sa.token
chmod 644 $ETC/tokens/ihsan-sa.token
install_as_root || { cat /tmp/out; fail "the rerun"; }
[ "$(cat $ETC/tokens/ihsan-sa.token)" = "the person's token" ] || fail "the rerun changed the token"
[ "$(stat -c '%U:%G %a' $ETC/tokens/ihsan-sa.token)" = "root:root 600" ] || fail "the rerun left the token $(mode $ETC/tokens/ihsan-sa.token)"
as_worker hive --version >/dev/null || fail "hive after the rerun"
ok "rerun keeps the token and makes it 0600 again"

# 2. Another repo is refused, matched as a fixed string.
snap /tmp/before
for repo in other/hive ihsan-sa/hiv. ihsan-sa/hive. IHSAN-SA/hive 'ihsan-sa/*'; do
  if install_as_root HIVE_REPO="$repo"; then fail "a rerun naming $repo was taken"; fi
  grep -q "refusing\|must be owner/name" /tmp/out || { cat /tmp/out; fail "the refusal of $repo doesn't say why"; }
done
snap /tmp/after
cmp -s /tmp/before /tmp/after || { diff /tmp/before /tmp/after | head -20; fail "a refused rerun changed something"; }
ok "a rerun naming another repo (other/hive, ihsan-sa/hiv., ...) is refused and changes nothing"

# 3. Names can't collide: foo and w-foo each get their own unit and records.
useradd -m -s /bin/sh foo
useradd -m -s /bin/sh w-foo
for u in foo w-foo; do
  env HIVE_USER=$u HIVE_SHARE=no sh /stage/install.sh >/tmp/out 2>&1 || { cat /tmp/out; fail "install for $u"; }
done
{ grep -qxF User=foo /etc/systemd/system/hive-foo.service && grep -qxF User=w-foo /etc/systemd/system/hive-w-foo.service; } || fail "units for foo and w-foo"
{ [ -d /etc/hive/foo ] && [ -d /etc/hive/w-foo ]; } || fail "records for foo and w-foo"
for u in foo w-foo; do HIVE_USER=$u sh /stage/uninstall.sh >/dev/null; done
{ [ ! -e /etc/hive/foo ] && [ ! -e /etc/systemd/system/hive-w-foo.service ]; } || fail "uninstall of foo and w-foo"
ok "users foo and w-foo get separate units and records; uninstall removes them"

# 4. One ask to a merge, as the worker, against the fake harness and a local forge.
as_worker sh -c 'mkdir -p ~/hive/.hive && cat >~/hive/.hive/config.json' <<'JSON'
{"harness": "fake", "sandbox": "bare", "bare_accepted": "2026-10-24T00:00:00Z", "auto_update": false,
 "mem_floor_mb": 0, "mem_pressure_max": 0, "cpu_pressure_max": 0, "load_max": 0, "plateau_merges": 0,
 "schedules": {"improve.sweep": "off", "improve.stepback": "off", "kb.file": "off", "modules.update": "off", "hive.update": "off"}}
JSON
as_worker hive daemon >/tmp/daemon.log 2>&1 &
i=0
until as_worker hive status >/dev/null 2>&1; do
  i=$((i + 1)); [ $i -lt 60 ] || { cat /tmp/daemon.log; fail "the daemon didn't start"; }
  sleep 1
done
as_worker hive ask --wait "create project alpha for experiments" | grep -q "Created project alpha" || fail "the lead didn't create alpha"
as_worker hive ask --project alpha "add a first feature" >/dev/null
i=0
until grep -q '"MERGED"' $W/forge/state.json 2>/dev/null; do
  i=$((i + 1)); [ $i -lt 120 ] || { as_worker hive status; tail -50 /tmp/daemon.log; fail "the ask didn't merge in 10 minutes"; }
  sleep 5
done
as_worker git -C $W/forge/repos/local__alpha.git log --oneline main | grep -q . || fail "no commit on alpha's main"
as_worker hive shutdown >/dev/null
wait
ok "one ask went to a merge"

# 5. Links the worker plants lead no write outside its home: the rerun stops
# at the first write the worker can't make, or writes nothing through them.
echo "root's own" >/etc/hive-test-root-file
chmod 644 /etc/hive-test-root-file
as_worker sh -c 'mv ~/.local/bin ~/.local/bin.real && ln -s /etc/cron.hourly ~/.local/bin && ln -s /etc ~/work && ln -s /etc/hive-test-root-file ~/.gitconfig'
check_links() { # name, whether the rerun should succeed
  snap /tmp/before
  if install_as_root; then got=0; else got=1; fi
  snap /tmp/after
  cmp -s /tmp/before /tmp/after || { cat /tmp/out; diff /tmp/before /tmp/after | head -20; fail "$1: the rerun changed something outside the home"; }
  [ "$got" = "$2" ] || { cat /tmp/out; fail "$1: the rerun exited $got, want $2"; }
  ok "$1: nothing outside the home changed (rerun exit $got)"
}
check_links "home/.local/bin -> /etc/cron.hourly, home/work -> /etc, home/.gitconfig -> a root file" 1
grep -q "stopped" /tmp/out || { cat /tmp/out; fail "the stopped rerun doesn't say so"; }
as_worker sh -c 'rm ~/.local/bin && mv ~/.local/bin.real ~/.local/bin && mv ~/.profile ~/.profile.real && ln -s /etc/profile ~/.profile'
check_links "home/.profile -> /etc/profile, home/work -> /etc, home/.gitconfig -> a root file" 1
as_worker sh -c 'rm ~/.profile && mv ~/.profile.real ~/.profile'
check_links "home/work -> /etc, home/.gitconfig -> a root file" 0
[ "$(cat /etc/hive-test-root-file)" = "root's own" ] || fail "the root file changed"

# 6. Uninstall.
HIVE_USER=worker sh /stage/uninstall.sh >/dev/null
{ [ ! -e $UNIT ] && [ ! -e $ETC ] && [ ! -e $W/.local/bin/hive ]; } || fail "uninstall left the unit, records or binary"
ok "uninstall removes the unit, root's records and the binary"
echo PASS
