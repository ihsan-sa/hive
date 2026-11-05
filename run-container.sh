#!/usr/bin/env bash
# Exploratory: can the Hive run in a container? Docker on the runner, systemd inside, as a normal (unprivileged) container and as the minimum that works.
SITE=${HIVE_SITE:-https://hivelab.ca}
cat > Dockerfile <<D
FROM ubuntu:24.04
ENV DEBIAN_FRONTEND=noninteractive container=docker
RUN apt-get update && apt-get install -y --no-install-recommends systemd systemd-sysv dbus curl ca-certificates sudo iproute2 && rm -rf /var/lib/apt/lists/*
STOPSIGNAL SIGRTMIN+3
CMD ["/sbin/init"]
D
docker build -q -t hivetest .
try() { # name, run flags
  n=$1; shift; echo "=== $n: docker run $*"
  docker rm -f h >/dev/null 2>&1
  docker run -d --name h "$@" hivetest >/dev/null || { echo "RESULT $n: could not start"; return; }
  sleep 6; docker exec h systemctl is-system-running 2>&1 | head -1
  docker exec h sh -c "curl -fsSL $SITE/install.sh | HIVE_ACCEPT=1 sh" > out.$n.log 2>&1; rc=$?
  tail -n 12 out.$n.log | cut -c1-200
  echo "RESULT $n: install rc=$rc"
  docker exec h sh -c 'sudo -iu hive hive web 2>&1 | head -3; ss -ltnH | head' 2>&1 | cut -c1-160
}
try plain
try cgroupns --privileged --cgroupns=host
try narrow --cap-add SYS_ADMIN --security-opt apparmor=unconfined --security-opt seccomp=unconfined --tmpfs /run --tmpfs /run/lock -v /sys/fs/cgroup:/sys/fs/cgroup:rw --cgroupns=host
