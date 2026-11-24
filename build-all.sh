#!/bin/sh
# Builds every download from this Linux machine into dist/. The real builds are the GitHub workflow's (real Windows and
# Mac machines, Mac signed); this is for trying a change here.
#   Linux .deb and .AppImage, Mac .zip (not signed: Apple silicon may refuse it), Windows Hive-Setup.exe.
# The Windows installer needs Wine, which is in a container: binaries can't run from a folder mounted into it, so it copies the sources in.
set -eu
cd "$(dirname "$0")"
# The site's version file says the version these downloads carry (the installed app reads it to offer a newer one).
node -e 'require("fs").writeFileSync("page/app-version.json", JSON.stringify({ version: require("./package.json").version }) + "\n")'
npm ci --no-audit --no-fund
npm test
npx electron-builder --linux --mac --publish never
podman run --rm --network=host -v "$PWD":/src -w /root docker.io/electronuserland/builder:wine sh -c \
  'mkdir /b && cd /src && cp -r main.js lib.js preload.js setup.html package.json package-lock.json build /b/ && cd /b && npm ci --no-audit --no-fund >/dev/null 2>&1 && npx electron-builder --win --publish never && cp dist/Hive-Setup.exe /src/dist/'
ls -la dist/*.deb dist/*.AppImage dist/*.zip dist/*.exe
