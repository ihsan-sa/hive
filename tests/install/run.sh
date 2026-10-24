#!/bin/sh
# The installer on a clean machine: builds this checkout's hive, serves it as
# a release, and runs in-container.sh in a fresh debian:13 container with no
# network. Building the image fetches Debian's git and systemd; the run itself
# is offline. Needs docker and go:
#   sh tests/install/run.sh
set -eu
here=$(cd "$(dirname "$0")" && pwd)
root=$(cd "$here/../.." && pwd)
command -v docker >/dev/null || { echo "run.sh: needs docker" >&2; exit 2; }
arch=$(go env GOARCH)
stage=$(mktemp -d)
trap 'rm -rf "$stage"' EXIT
mkdir "$stage/release"
(cd "$root" && CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build -o "$stage/release/hive-linux-$arch" ./cmd/hive)
(cd "$stage/release" && sha256sum "hive-linux-$arch" >checksums.txt)
cp "$root/install/install.sh" "$root/install/uninstall.sh" "$here/in-container.sh" "$here/curl" "$stage/"
chmod -R a+rX "$stage"
docker build -q -t hive-install-test "$here" >/dev/null
docker run --rm --network none -e HIVE_INSTALL_TEST=container -v "$stage:/stage:ro" hive-install-test sh /stage/in-container.sh
