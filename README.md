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

## Opening the Mac download the first time (wording for the download page)
Seen on macOS 15.8 on Apple silicon, runner run 44390913418 (`test/downloaded.sh`; screenshots in `shots/mac-download-*.png`).
Until the app is signed with a paid Apple certificate, the Mac refuses the first double-click. The page should say:

1. Double-click Hive. The Mac says **"Hive" Not Opened**: "Apple could not verify "Hive" is free of malware…". Press **Done** (not Move to Trash).
2. Open **System Settings**, **Privacy & Security**, and scroll down to **Security**. It says *"Hive" was blocked to protect your Mac.* Press **Open Anyway**, then confirm with your password or Touch ID.
3. Hive opens. The Mac asks this once only.

The older shortcut (right-click, Open) no longer works on macOS 15. For someone at ease in Terminal,
`xattr -dr com.apple.quarantine /Applications/Hive.app` does the same as steps 1 and 2 (checked in the same run: the app then opens straight to "Set up the Hive").

## Remove it
Delete the app first (the usual way for the system), then take the Hive itself off with the site's remover. Each remover asks before deleting, and keeps your projects, notes and board (copied to a folder of their own) unless you say to delete them.
- **Linux** (the app installs the Hive inside your own account): the folder `~/hive`, its background services and the `hive` command. Run `curl -fsSL https://hivelab.ca/uninstall.sh | sh -s -- --this-account`; it never deletes your account. Then `sudo apt remove hive-app` for the .deb (or delete the AppImage). The app's own settings are in `~/.config/Hive` (window size only; nothing from the Hive).
- **Windows**: the Hive is a WSL system called `Hive` (WSL itself and your other Linux systems stay), a `Hive` entry in the Start menu, on the Desktop and in Startup (it keeps WSL awake), and `%LOCALAPPDATA%\Hive`. Run `irm https://hivelab.ca/uninstall.ps1 | iex` in PowerShell, then uninstall "Hive" in Settings, Apps. Your kept files go to `Hive-kept-DATE` in your home folder.
- **Mac**: the Hive's own small Linux machine (a Lima VM named `hive.vm`) and `~/.hive`, plus `~/Applications/Hive.app` (the launcher icon). A Lima of your own in `~/.lima` is not touched. Run `curl -fsSL https://hivelab.ca/uninstall-mac.sh | sh`, then drag `/Applications/Hive.app` to the Bin. Your kept files go to `~/hive-kept-DATE`.

## Files
- `lib.js`: everything that differs by system, with no Electron in it. `node test/logic.test.js`.
- `main.js`: the window. Only the setup screen and the local Hive ever load in it; other links go to the browser.
- `setup.html`, `preload.js`: the setup screen and its three buttons' bridge (the web app gets no bridge).
- `test/e2e.js`: the real app in a hidden window against a stand-in site and Hive (`xvfb-run -a node test/e2e.js`).
- `test/smoke.js`: a packaged app starts and shows the setup screen; the workflow runs it on all three systems.
- `.github/workflows/build.yml`: builds on real Windows, Mac and Linux machines. `build-all.sh`: all of them from here.

## Not done
See `goals.md`. In short: run on GitHub's Windows and Mac machines, never on a person's own; not signed, so both warn on first open.
