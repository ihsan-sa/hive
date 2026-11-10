# Hive, the desktop app

A download you double-click, for Windows, macOS and Linux. It is a thin shell around the Hive's web app:

1. It looks for a Hive on the computer. If there's none, it runs the same installer as the one-line install
   (`install.ps1`, `install-mac.sh`, `install.sh --this-account` from the site), in its own window, with the
   installer's own warning shown before anything changes.
2. It asks the Hive for its address (`hive open --print`) and shows the web app in a window.

There is one version of the product: the web app. The shell holds no Hive logic, so a Hive update never needs a new
download, and the terminal install and the browser keep working beside it.

| System | Download | Under it |
|---|---|---|
| Windows 10/11 | `Hive-Setup.exe` | WSL 2 system called Hive (may need one restart) |
| macOS 13+ | `Hive-mac-arm64` / `Hive-mac-x64` (.dmg from the workflow, .zip from here) | a Linux VM (test build) |
| Linux | `Hive-linux-amd64.deb`, or the `.AppImage` | `~/hive`, in your own account, sandboxed |

## Files
- `lib.js`: everything that differs by system, with no Electron in it. `node test/logic.test.js`.
- `main.js`: the window. Only the setup screen and the local Hive ever load in it; other links go to the browser.
- `setup.html`, `preload.js`: the setup screen and its three buttons' bridge (the web app gets no bridge).
- `test/e2e.js`: the real app in a hidden window against a stand-in site and Hive (`xvfb-run -a node test/e2e.js`).
- `test/smoke.js`: a packaged app starts and shows the setup screen; the workflow runs it on all three systems.
- `.github/workflows/build.yml`: builds on real Windows, Mac and Linux machines. `build-all.sh`: all of them from here.

## Not done
See `goals.md`. In short: never run on a real Windows or Mac; not signed, so both warn on first open.
