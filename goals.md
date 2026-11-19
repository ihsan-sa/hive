# Hive desktop app

**Goal:** someone downloads one file on Windows, macOS or Linux, double-clicks it, and ends up in the Hive, with no
terminal. One product version: the app is a shell over the web app (the person's ask, thread 111014537970, 2026-11-10).

**Design, decided 2026-11-10 (a58e9):** Electron shell; on first launch it runs the site's own installers; then it shows
`hive open --print`'s address in a window. No Hive logic in the shell, so nothing to keep in step with releases.
This reverses the 2026-11-04 "Electron deferred" note (`kb/consumer-hive-desktop-app-electron-deferred.md`): its revisit
condition (the install line stops people) is what the person now reports.

**State (2026-11-18):** builds and starts on real Windows, Mac (Apple silicon and Intel) and Linux machines (GitHub
runners). The real install through the packaged app has passed on all three: Linux (run 42893160321, about 35 s),
Windows (run 42895554256, 1 min 43 s; that machine already had WSL, so the turn-on-WSL-and-restart path on a plain PC
is untested) and the Intel Mac (run 44387375708, 11 min 44 s, after the installer fix in Hive v2.3.122). An Apple
silicon Mac's install is untested (its runners can't start a VM). The app now asks the server to prove it holds the key
before sending it (`/api/proof`, in `modules/dash/dash.py`, reaches people with the release after v2.3.122) and uses
only 127.0.0.1; reviewed by Opus twice (REVIEW.md 18 to 20; 19 open). What a Mac shows on opening the unsigned
download is recorded (run 44390913418, `test/downloaded.sh`, README): refused, then Open Anyway in System Settings.
Nobody has yet installed it on a real person's PC.

## Ladder (thread 111014537970)
- [x] Shell, setup flow, tests on Linux. check: `npm test && xvfb-run -a node test/e2e.js` print ok
- [x] All downloads build from this machine. check: `./build-all.sh` lists a .deb, .AppImage, two Mac .zip and Hive-Setup.exe
- [x] Independent review (Opus) and its findings fixed (15 of 17; two open, below). check: REVIEW.md lists each finding with fixed/declined and why
- [x] Build and smoke-test on real Windows and Mac machines (GitHub runners, `.github/workflows/build.yml`, branch `test-desktop-app` of the public repo, allowed by the warden in post 111015050780 with conditions: only that branch, no tags or releases, artifacts kept 7 days). check: a workflow run with all four jobs green, screenshots in its artifacts
- [x] The real install through the app on a runner, per system: Linux (`install.sh --this-account` from the site, then the web app's first page loads in the window), Windows (WSL 2 on the runner, as `test-windows-install` does), Mac Intel runner (VM boots there; Apple silicon runners can't). check: one green run per system with the web app's page in the screenshot. Linux: done (run 42893160321). Windows: done (run 42895554256), after fixing the app passing PowerShell 7's module path to Windows PowerShell (`unset PSModulePath`). Mac Intel: waits on the Mac installer's last-step fix (release-train), then rerun the workflow (post 111815263eaa)
- [x] Ad-hoc signed Mac build opens on Apple silicon after a download (quarantine set): what the person sees, step by step, written into the site's wording. check: screenshots or a tester's report in the thread (post 11181530787d)
- [x] The Hive's server proves itself before the app sends it the key (review finding 3), and Windows on ARM gets the right system (finding 16). check: a test where a stand-in on the port never receives the key (post 11181512405c)
- [x] The new app (with the proof check and 127.0.0.1 only) passes the real installs on the runners. Waits on the Hive release after v2.3.122, the first whose server answers `/api/proof`; then push master to the test branch (until then the branch carries `runner-pre-proof`: master's tests and workflow on the app before acc2ea5). check: one run with `ok real` on Linux, Windows and the Intel Mac after:2026-11-19 (post 11181947afb3)
- [!] Downloads on the site beside the one-line install (release.sh carries them; download page picks the system). check: site page shows three buttons and `release.sh check` passes. Stopped 2026-11-18: public downloads need the person's go (warden, post 111015050780); asked in post 11101531e04e
- [x] The installers' own launcher icons (Start menu Hive.lnk, ~/Applications/Hive.app, hive.desktop) open the app when it's installed, so there's one icon, not two. check: test in test_get.sh / test_mac.sh (post 111816244212)
- [!] A tester on a real Windows PC and a real Mac installs from the download only. check: their report in the thread. Stopped 2026-11-18: needs a person with a real Windows PC and Mac
- [x] A one-page tester sheet so a person with a real Windows PC or Mac can test without our help (steps, expected warnings, what to report). check: `TESTER.md` exists and its warnings match README's Mac wording (post 11182018d9b5)
- [ ] Download-page draft (not published): three buttons that pick the system, the Windows SmartScreen and Mac Open Anyway steps, in the site's wording, built in a scratch dir. check: `node test/page.test.js` loads the page, picks the right button for a Windows, Mac and Linux user-agent string
- [ ] Second launch and Repair on the runner: after the real install, open the app again and see it go straight to the Hive with no install step. check: `test/real.js` run on Linux in the workflow prints `ok second launch`
- [!] Apple silicon Mac install: its runners cannot start a VM. check: a tester's report (needs a person with an Apple silicon Mac)

## Needs the person
- Signing. Unsigned, Windows shows "Windows protected your PC" (More info, Run anyway) and macOS refuses on the first
  double-click (System Settings, Privacy & Security, Open Anyway). Clean double-click needs an Apple Developer account
  (US$99 a year) and a Windows certificate (Azure Trusted Signing, about US$10 a month). Money, so theirs to decide.

## Notes
- The .deb's maintainer address (`package.json` author email) is a placeholder: replace it with a real contact before the site carries it.
- AppImage needs libfuse2 and trips Ubuntu 24.04's sandbox limit, so the .deb is the Linux download to offer first.
- Windows build here: Wine in `electronuserland/builder:wine` under podman, `--network=host`, sources copied in (bind mounts can't exec).
