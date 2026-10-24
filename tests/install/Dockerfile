# A fresh Debian 13 for tests/install/run.sh. git is what the Hive needs to
# work; systemd only lends systemd-analyze, to check the unit the installer
# writes (nothing boots it). curl is left out on purpose: the test's own curl
# stands in for the network, which the run doesn't have.
FROM debian:13
RUN apt-get update && apt-get install -y --no-install-recommends git systemd && rm -rf /var/lib/apt/lists/*
