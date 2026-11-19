# Review, 2026-11-10

Reviewer: an Opus agent that didn't build it, reading the shell against the real installers (`core/dist/get.sh`, `get.ps1`, `get-mac.sh`) and `h2 open`.

| # | Finding | Outcome |
|---|---------|---------|
| 1 | Mac: `hive open --print` prints nothing there (the Mac command opens the browser), so the app could never open the Hive | Fixed: the app asks with `hive web`, then waits for the address to answer. Also core 4426d8b: the Mac `hive open` honours `--print` |
| 2 | Fresh Ubuntu has no git or curl: the app said "check the internet connection" | Fixed: the app names what's missing and the `sudo apt install` line; the .deb depends on curl, git, python3, so installing it brings them |
| 3 | Another program on the Hive's port would be given the key | Fixed: the app asks whatever answers on the port for a mark only the key can make (`GET /api/proof?n=...`, HMAC of the key over the port and a fresh question; server side in `modules/dash/dash.py`, tests in `test_app.py`), and loads the address only when it fits. Test in e2e (a stand-in on the port is never sent the sign-in address; fails without the check). A Hive older than the release that carries `/api/proof` reads as not the Hive: the screen says Repair brings it up to date. Not covered: an older install under a separate user (finding 10), where the app holds no key to check with |
| 4 | A redirect from the Hive's address could load an outside site in the window; windows the web app opens had no rules | Fixed: `will-redirect` and child windows follow the same rule. Test in e2e (fails without the fix) |
| 5 | Windows: the app didn't start the keep-awake process, so the Hive could stop under the window | Fixed: same hidden process the installer's icon starts |
| 6 | Windows and Mac installers also open the browser, so a second Hive tab and the key in browser history | Fixed in core 4426d8b (`HIVE_FROM_APP=1` skips it). Reaches people with the next release; until then a browser tab also opens once, at install |
| 7 | Welcome text promised an accept step that only Linux has | Fixed: said on Linux only |
| 8 | Windows: a repair that stopped looked done | Fixed: on Windows only the installer's last line ("The Hive is installed") counts as done |
| 9 | Linux: the Hive's command ran with the person's XDG settings | Fixed: same surroundings as the installer's `in_hive` |
| 10 | Linux, older install under a separate user: the window shows the sign-in page | Declined for now: the sign-in is kept after the first time; that kind of install is the terminal one and its address is in the terminal |
| 11 | Apple silicon refuses a Mac build with no signature at all | Fixed in the workflow: built on Mac runners, ad-hoc signed, `codesign --verify` passes. The zips built on Linux are not for people |
| 12 | No `desktopName` | Fixed |
| 13 | A dead Hive made the window retry for ever | Fixed: three tries, then "The Hive didn't start" with Try again and Repair |
| 14 | No way out of "Opening the Hive"; Try again skipped the warning | Fixed: Close button; Try again and Repair no longer accept for the person |
| 15 | Download progress bars flooded the log | Fixed: a redrawn line keeps its last drawing |
| 16 | Windows on ARM may get the x64 system | Fixed in the app, not yet run on an ARM PC: the installer is handed the machine's own chip from the registry (a translated app is told AMD64). Logic test; the x64 Windows runner shows it changes nothing there |
| 17 | Any `file:` page counted as the setup page; DevTools on | Fixed: only `setup.html`; DevTools off in packaged builds; unused message removed |
| 18 | (second review, 2026-11-18, of the fix for 3) `localhost` is also `::1`, where the Hive doesn't listen: a program there would get the window and could pass the proof question on to the real Hive | Fixed: the app turns the address into `127.0.0.1` for the probe, the proof and the window. Logic and e2e tests |
| 19 | (second review) The sign-in cookie is as good as the key and still goes to whatever holds the port if the Hive restarts under an open window and another program takes the port in the gap; an older install under a separate user (no key in the address) isn't checked at all | Open. Same gap as in any browser. A later start is covered (the proof runs every time the app opens the Hive, also after a failed load) |
| 20 | (second review) A proof answer that drips in slowly stalled the start | Fixed: 8 s limit |
