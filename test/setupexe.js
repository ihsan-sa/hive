// node test/setupexe.js dist/Hive-Setup.exe : on a throwaway Windows machine (a CI runner), the file a Windows person
// downloads, used the way they use it. The installer gets the browser's download mark (Zone.Identifier, internet zone),
// installs silently for this account (it is one-click, per user), and must leave the app, its Start menu and desktop
// shortcuts and its Apps & features entry. The app is then started by its Start menu shortcut and must reach the Hive
// (or, on a machine with no Hive, its setup screen, said so). Last, the uninstaller from Apps & features must take the
// app and its shortcuts away again. The SmartScreen prompt a double-click shows is outside a runner's reach (README).
// With a second installer and its version (node test/setupexe.js OLD.exe NEW.exe 0.1.1), the update a person does: the
// window is moved and sized while the old version runs, the newer installer (also download-marked) goes over it, and
// there must then be one Apps & features entry, at the new version, in the same folder; opened from the Start menu the
// new version must keep the window where it was and reach the Hive (or setup).
// With a third (... NEWEST.exe 0.1.2), the update the app's notice leads to: the newest installer runs while the new
// version is still open, and must close it, leave one entry at the newest version, and a Start menu shortcut that opens
// it at the Hive. The uninstall then runs on the last one installed.
// Before it, the update notice's toast: the installed app against a stand-in site with a newer app ('ok toast').
const fs = require('fs');
const os = require('os');
const path = require('path');
const { execFileSync, spawn } = require('child_process');
const { _electron } = require('playwright-core');
const quit = require('./quit');
const exe = process.argv[2];
if (process.platform !== 'win32' || !exe || !fs.existsSync(exe)) { console.error('usage (Windows only): node test/setupexe.js PATH-TO-Hive-Setup.exe'); process.exit(2); }
// HIVE_EXPECT_VERSION: the version the installer must put in (test/livewin.sh: the one the live site names).
const version = process.env.HIVE_EXPECT_VERSION || require('../package.json').version;
const [newer, newVersion, newest, newestVersion] = process.argv.slice(3, 7);
if ((newer && (!fs.existsSync(newer) || !newVersion)) || (newest && (!newer || !fs.existsSync(newest) || !newestVersion))) { console.error('usage: node test/setupexe.js OLD.exe [NEW.exe NEW-VERSION [NEWEST.exe NEWEST-VERSION]]'); process.exit(2); }
// A fresh account may have no Uninstall key at all, and an unreadable subkey makes Get-ItemProperty complain: neither is a
// failure here, so errors are silenced and the exit is 0; anything PowerShell itself cannot run still shows its stderr.
const ps = cmd => {
  try {
    return execFileSync('powershell.exe', ['-NoProfile', '-NonInteractive', '-Command', `$ErrorActionPreference = 'SilentlyContinue'; ${cmd}; exit 0`], { encoding: 'utf8', timeout: 180000, stdio: ['ignore', 'pipe', 'pipe'] }).trim();
  } catch (err) { throw new Error(`PowerShell failed (${err.status}): ${(err.stderr || '').toString().trim().slice(0, 800) || err.message.slice(0, 300)}`); }
};
const sleep = ms => new Promise(r => setTimeout(r, ms));
// The installer run without blocking this process, so the open app's connection keeps being served while it works.
const runInstaller = (file, args, label) => new Promise((resolve, reject) => {
  // No pipes: a process the installer starts must not keep this waiting after the installer itself has ended. While it
  // runs, every 30 s says which installer-side processes and windows are up; if it is still running after 6 min, it
  // saves a picture of the screen (a dialog waiting on nobody?) and the process list, ends the tree and fails.
  const t0 = Date.now();
  const child = spawn(file, args, { stdio: 'ignore', windowsHide: false });
  const look = () => {
    try {
      return ps(`Get-CimInstance Win32_Process | Where-Object { $_.Name -notmatch '^(node|bash|powershell|pwsh|conhost|svchost)\\.exe$' -and ($_.Name -match '^(Hive|Au_|Un_A|Uninstall|taskkill|tasklist|find|cmd|ns)' -or $_.CommandLine -match 'hive') } | ForEach-Object { "$($_.ProcessId)<$($_.ParentProcessId) $($_.Name) $($_.CommandLine)" }; Get-Process | Where-Object { $_.MainWindowTitle } | ForEach-Object { "window: $($_.Name) [$($_.Id)] '$($_.MainWindowTitle)'" }`).split(/\r?\n/).map(l => l.slice(0, 260)).join('\n    ');
    } catch (err) { return String(err.message || err); }
  };
  const beat = setInterval(() => console.log(`  ${label}: still running after ${Math.round((Date.now() - t0) / 1000)} s:\n    ${look()}`), 30000);
  const stuck = setTimeout(() => {
    clearInterval(beat);
    const shot = path.resolve(`test/shots/${label}-stuck.png`);
    fs.mkdirSync(path.dirname(shot), { recursive: true });
    try { ps(`Add-Type -AssemblyName System.Windows.Forms,System.Drawing; $b=[System.Windows.Forms.Screen]::PrimaryScreen.Bounds; $p=New-Object System.Drawing.Bitmap $b.Width,$b.Height; [System.Drawing.Graphics]::FromImage($p).CopyFromScreen($b.Location,[System.Drawing.Point]::Empty,$b.Size); $p.Save('${shot}')`); } catch { }
    const seen = look();
    try { execFileSync('taskkill', ['/T', '/F', '/PID', String(child.pid)], { stdio: 'ignore', timeout: 30000 }); } catch { }
    reject(new Error(`${label}: ${path.basename(file)} ${args.join(' ')} still running after 6 min (screen saved to ${shot}); processes then:\n    ${seen}`));
  }, 6 * 60000);
  child.on('error', err => { clearInterval(beat); clearTimeout(stuck); reject(err); });
  child.on('exit', code => { clearInterval(beat); clearTimeout(stuck); console.log(`  ${label}: the installer ended (exit ${code}) after ${Math.round((Date.now() - t0) / 1000)} s`); code === 0 ? resolve() : reject(new Error(`${label}: ${path.basename(file)} ${args.join(' ')} ended with exit ${code}`)); });
});
const alive = pid => { try { process.kill(pid, 0); return true; } catch { return false; } };
const hiveProcs = () => { const out = ps(`Get-Process -Name Hive | Select-Object Id,Path | ConvertTo-Json -Compress`); if (!out) return []; const p = JSON.parse(out); return Array.isArray(p) ? p : [p]; };
// A step that has not ended in s seconds fails, saying where it was and what was running (the Hive processes and the
// app's two state files), instead of the job's silent 30-minute limit.
const within = (what, p, s) => {
  let t;
  const late = new Promise((_, no) => { t = setTimeout(() => {
    let procs, files = '';
    try { procs = JSON.stringify(hiveProcs()); } catch (err) { procs = String(err.message || err); }
    for (const f of ['hives.json', 'window-state.json']) { try { files += ` ${f}: ${fs.readFileSync(path.join(process.env.APPDATA || '', 'Hive', f), 'utf8').slice(0, 400)};`; } catch { files += ` no ${f};`; } }
    no(new Error(`stuck for ${s} s at: ${what}; Hive processes: ${procs};${files}`));
  }, s * 1000); });
  return Promise.race([p, late]).finally(() => clearTimeout(t));
};
const entry = () => {
  const out = ps(`Get-ChildItem HKCU:\\Software\\Microsoft\\Windows\\CurrentVersion\\Uninstall -ErrorAction SilentlyContinue | Get-ItemProperty | Where-Object { $_.DisplayName -like 'Hive*' -and $_.UninstallString } | Select-Object DisplayName,DisplayVersion,InstallLocation,UninstallString,DisplayIcon | ConvertTo-Json -Compress`);
  if (!out) return null;
  const e = JSON.parse(out);
  return Array.isArray(e) ? e : [e];
};
const links = () => {
  const out = ps(`$s = New-Object -ComObject WScript.Shell; @(foreach ($p in @((Join-Path ([Environment]::GetFolderPath('Programs')) 'Hive.lnk'), (Join-Path ([Environment]::GetFolderPath('Desktop')) 'Hive.lnk'))) { if (Test-Path -LiteralPath $p) { $l = $s.CreateShortcut($p); [pscustomobject]@{ Path = $p; Target = $l.TargetPath; Args = $l.Arguments } } }) | ConvertTo-Json -Compress`);
  if (!out) return [];
  const l = JSON.parse(out);
  return Array.isArray(l) ? l : [l];
};
const marked = file => {
  const copy = path.join(fs.mkdtempSync(path.join(os.tmpdir(), 'hive-dl-')), 'Hive-Setup.exe');
  fs.copyFileSync(file, copy);
  fs.writeFileSync(copy + ':Zone.Identifier', '[ZoneTransfer]\r\nZoneId=3\r\nReferrerUrl=https://hivelab.ca/\r\nHostUrl=https://hivelab.ca/Hive-Setup.exe\r\n');
  const mark = fs.readFileSync(copy + ':Zone.Identifier', 'utf8');
  if (!/ZoneId=3/.test(mark)) throw new Error('the download mark did not stick: ' + mark);
  console.log(`ok download mark: ${copy} carries ZoneId=3 (${fs.statSync(copy).size} bytes)`);
  return copy;
};
// The app may open the Hive at /login#key, which signs in and goes on; only a page past /login is the Hive signed in.
const hiveAt = /^http:\/\/(localhost|127\.0\.0\.1):\d+\/(?!login\b)/;
// Started the way a person does, from its Start menu shortcut; resolves once the Hive or the setup screen shows.
// The app's own page (setup.html) is the window's; each Hive shows in a view over it (DESIGN-many-hives.md), so what the
// window shows is its top view's address, and the Hive's page is the Playwright page at that address.
async function ownPage(app) {
  for (const end = Date.now() + 60000; Date.now() < end; await new Promise(r => setTimeout(r, 200))) {
    const p = app.windows().find(w => w.url().includes('setup.html'));
    if (p) return p;
  }
  return app.firstWindow();
}
const frontPage = (app, at) => app.windows().find(p => at && p.url().split('#')[0] === at);
async function openFromStart(start, shot) {
  const app = await within(`${shot}: starting ${start.Target}`, _electron.launch({ executablePath: start.Target, args: start.Args ? start.Args.split(/\s+/) : [], env: { ...process.env, HIVE_APP_TEST: '1' } }), 120);
  const page = await ownPage(app);
  const where = () => within(`${shot}: asking the app what its window shows`, app.evaluate(({ BrowserWindow }) => BrowserWindow.getAllWindows().map(w => (w.contentView.children.at(-1)?.webContents ?? w.webContents).getURL().split('#')[0])[0] || ''), 20).catch(() => '');
  const title = () => within(`${shot}: reading the setup page's title`, page.evaluate(() => document.querySelector('#title')?.textContent || ''), 20).catch(() => '');
  const end = Date.now() + 4 * 60000; let at = '', t = '';
  for (;;) {
    at = await where(); t = await title();
    if (hiveAt.test(at) || t === 'Set up the Hive' || Date.now() > end) break;
    await sleep(1000);
  }
  fs.mkdirSync('test/shots', { recursive: true });
  await within(`${shot}: screenshot`, (frontPage(app, at) || page).screenshot({ path: `test/shots/${shot}-${process.arch}.png` }), 30).catch(() => { });
  const reached = hiveAt.test(at) ? `the Hive at ${at}` : t === 'Set up the Hive' ? 'the setup screen (no Hive on this machine)' : '';
  if (!reached) { await within(`${shot}: closing the app`, quit(app), 60).catch(() => { }); throw new Error(`the installed app never showed the Hive or its setup screen (at "${at}", "${t}")`); }
  return { app, reached };
}
const bounds = app => app.evaluate(({ BrowserWindow }) => { const w = BrowserWindow.getAllWindows()[0]; return w ? w.getNormalBounds() : null; });
(async () => {
  // 1. The download mark a browser puts on the file.
  const copy = marked(exe);
  if (entry()) throw new Error('an app entry is already in Apps & features before the install: ' + JSON.stringify(entry()));

  // 2. Install, as one-click does, for this account only (no admin prompt).
  const t0 = Date.now();
  execFileSync(copy, ['/S'], { timeout: 5 * 60000, stdio: 'inherit' });
  let e = null;
  for (let i = 0; i < 60 && !(e = entry()); i++) await sleep(2000);
  if (!e) throw new Error('no Hive entry in Apps & features after the install');
  if (e.length !== 1) throw new Error('more than one Hive entry in Apps & features: ' + JSON.stringify(e));
  e = e[0];
  // electron-builder's per-user entry may leave InstallLocation empty; its uninstaller sits in the app's own folder.
  let unexe = (e.UninstallString.match(/^"([^"]+)"/) || [null, e.UninstallString.split(' /')[0]])[1];
  e.InstallLocation = e.InstallLocation || path.dirname(unexe);
  const bin = path.join(e.InstallLocation, 'Hive.exe');
  if (!fs.existsSync(bin)) throw new Error(`Apps & features points at ${e.InstallLocation}, but there is no Hive.exe in it: ` + JSON.stringify(e));
  const real = x => { try { return fs.realpathSync.native(x).toLowerCase(); } catch { return String(x).toLowerCase(); } }; // 8.3 names (RUNNER~1) vs long ones
  if (!process.env.LOCALAPPDATA || !real(e.InstallLocation).startsWith(real(process.env.LOCALAPPDATA))) throw new Error(`installed outside this account's folder: ${e.InstallLocation}`);
  if (e.DisplayVersion !== version) throw new Error(`Apps & features shows version ${e.DisplayVersion}, the app is ${version}`);
  const l = links();
  const start = l.find(x => /Programs\\Hive\.lnk$/i.test(x.Path));
  if (!start) throw new Error('no Hive shortcut in the Start menu: ' + JSON.stringify(l));
  for (const x of l) if (real(x.Target) !== real(bin)) throw new Error(`${x.Path} opens ${x.Target}, not the installed app`);
  console.log(`ok setup-exe install: ${Math.round((Date.now() - t0) / 1000)} s, "${e.DisplayName}" ${e.DisplayVersion} in ${e.InstallLocation}; shortcuts: ${l.map(x => x.Path).join(', ')}`);

  // 3. Start it the way a person does: the Start menu shortcut.
  let { app, reached } = await openFromStart(start, 'setupexe');
  let kept = null;
  if (newer) {
    // The person moves and sizes the window; the app keeps that in its data folder when it closes.
    await within('setupexe: moving and sizing the window', app.evaluate(({ BrowserWindow }) => { const w = BrowserWindow.getAllWindows()[0]; w.unmaximize(); w.setBounds({ x: 37, y: 23, width: 811, height: 577 }); }), 30);
    await sleep(1500);
    kept = await within('setupexe: reading the window bounds', bounds(app), 30);
  }
  await within('setupexe: closing the app', quit(app), 60);
  console.log(`ok setup-exe launch: the Start menu shortcut opens ${reached}`);

  if (newer) {
    // 3b. The update: the newer download, installed over the old one the same way.
    const copy2 = marked(newer);
    const t1 = Date.now();
    execFileSync(copy2, ['/S'], { timeout: 5 * 60000, stdio: 'inherit' });
    let e2 = null;
    for (let i = 0; i < 60; i++) { e2 = entry(); if (e2 && e2.some(x => x.DisplayVersion === newVersion)) break; await sleep(2000); }
    if (!e2) throw new Error('no Hive entry in Apps & features after the update');
    if (e2.length !== 1) throw new Error('after the update, more than one Hive entry in Apps & features: ' + JSON.stringify(e2));
    if (e2[0].DisplayVersion !== newVersion) throw new Error(`after the update, Apps & features shows ${e2[0].DisplayVersion}, not ${newVersion}`);
    const unexe2 = (e2[0].UninstallString.match(/^"([^"]+)"/) || [null, e2[0].UninstallString.split(' /')[0]])[1];
    if (real(path.dirname(unexe2)) !== real(e.InstallLocation)) throw new Error(`the update went to ${path.dirname(unexe2)}, not over ${e.InstallLocation}`);
    if (!fs.existsSync(bin)) throw new Error(`after the update there is no ${bin}`);
    const l2 = links();
    const start2 = l2.find(x => /Programs\\Hive\.lnk$/i.test(x.Path));
    if (!start2) throw new Error('after the update, no Hive shortcut in the Start menu: ' + JSON.stringify(l2));
    for (const x of l2) if (real(x.Target) !== real(bin)) throw new Error(`after the update, ${x.Path} opens ${x.Target}, not the installed app`);
    ({ app, reached } = await openFromStart(start2, 'setupexe-update'));
    const running = await within('setupexe-update: reading the version', app.evaluate(({ app: a }) => a.getVersion()), 30);
    const now = await within('setupexe-update: reading the window bounds', bounds(app), 30);
    await within('setupexe-update: closing the app', quit(app), 60);
    if (running !== newVersion) throw new Error(`the Start menu shortcut opens version ${running}, not ${newVersion}`);
    const off = kept && now ? Math.max(...['x', 'y', 'width', 'height'].map(k => Math.abs(kept[k] - now[k]))) : Infinity;
    if (off > 2) throw new Error(`the update lost the window's place: before ${JSON.stringify(kept)}, after ${JSON.stringify(now)}`);
    console.log(`ok setup-exe upgrade: ${version} to ${newVersion} in ${Math.round((Date.now() - t1) / 1000)} s; one Apps & features entry, "${e2[0].DisplayName}" ${e2[0].DisplayVersion}, same folder; window kept at ${now.width}x${now.height}+${now.x}+${now.y}; ${newVersion} opens ${reached}`);
    unexe = unexe2;
  }

  if (newest) {
    // 3c. The update the app's own notice leads to: its download page opens from the running app, so the newer
    // Hive-Setup.exe is run while the old version is still open and its files are in use. It must end with the old app
    // gone, one entry at the newest version in the same folder, and the Start menu shortcut reaching the Hive at it.
    let reached0;
    ({ app, reached: reached0 } = await openFromStart(links().find(x => /Programs\\Hive\.lnk$/i.test(x.Path)), 'setupexe-open'));
    const pid = app.process().pid;
    const from = await app.evaluate(({ app: a }) => a.getVersion());
    await app.evaluate(({ BrowserWindow }) => { const w = BrowserWindow.getAllWindows()[0]; w.setBounds({ x: 61, y: 41, width: 833, height: 599 }); });
    await sleep(1500);
    const kept3 = await bounds(app);
    const copy3 = marked(newest);
    const t2 = Date.now();
    console.log(`  with ${from} open (pid ${pid}), running the ${newestVersion} installer`);
    await runInstaller(copy3, ['/S'], 'setupexe-update-open');
    let e3 = null;
    for (let i = 0; i < 60; i++) { e3 = entry(); if (e3 && e3.some(x => x.DisplayVersion === newestVersion)) break; await sleep(2000); }
    await Promise.race([quit(app, 10000).catch(() => { }), sleep(11000)]); // its process is gone, so this only tidies up
    if (alive(pid)) throw new Error(`the installer finished but the open ${from} app (pid ${pid}) is still running`);
    if (!e3 || e3.length !== 1 || e3[0].DisplayVersion !== newestVersion) throw new Error(`after the update with the app open, Apps & features shows ${JSON.stringify(e3)}, not one entry at ${newestVersion}`);
    const unexe3 = (e3[0].UninstallString.match(/^"([^"]+)"/) || [null, e3[0].UninstallString.split(' /')[0]])[1];
    if (real(path.dirname(unexe3)) !== real(e.InstallLocation)) throw new Error(`the update went to ${path.dirname(unexe3)}, not over ${e.InstallLocation}`);
    // Did the installer start the new version itself (as it does after a normal install)? Said so, then closed, so the
    // shortcut below is what opens it.
    const started = hiveProcs();
    if (started.length) { ps(`Get-Process -Name Hive | Stop-Process`); await sleep(2000); }
    const start3 = links().find(x => /Programs\\Hive\.lnk$/i.test(x.Path));
    if (!start3 || real(start3.Target) !== real(bin)) throw new Error('after the update with the app open, the Start menu shortcut is missing or wrong: ' + JSON.stringify(links()));
    ({ app, reached } = await openFromStart(start3, 'setupexe-update-open'));
    const running3 = await app.evaluate(({ app: a }) => a.getVersion());
    const now3 = await bounds(app);
    await within('setupexe-update-open: closing the app', quit(app), 60);
    if (running3 !== newestVersion) throw new Error(`after the update with the app open, the Start menu shortcut opens ${running3}, not ${newestVersion}`);
    const off3 = kept3 && now3 ? Math.max(...['x', 'y', 'width', 'height'].map(k => Math.abs(kept3[k] - now3[k]))) : Infinity;
    console.log(`ok setup-exe upgrade while open: ${from} open at ${reached0.replace(/^the Hive at /, '')}, ${newestVersion} installed over it in ${Math.round((Date.now() - t2) / 1000)} s; the open app was closed by the installer; one Apps & features entry, "${e3[0].DisplayName}" ${e3[0].DisplayVersion}, same folder; ${started.length ? 'the installer started ' + newestVersion + ' itself; ' : ''}window ${off3 <= 2 ? 'kept' : 'not kept (' + JSON.stringify(kept3) + ' then ' + JSON.stringify(now3) + ')'}; ${newestVersion} opens ${reached}`);
    unexe = unexe3;
  }

  // 3d. The update notice as a Windows person sees it: a toast, which Windows ties to the Start menu shortcut's app ID,
  // so only the installed app can show one. Started from its shortcut with a stand-in site whose app is newer and
  // without HIVE_APP_TEST (which skips the toast); newer-seen.txt is written only on the toast's show event. A runner
  // session that can't show toasts is said in words, not a red build; an app that never noticed the newer one is.
  {
    const http = require('http');
    const body = '{"version":"9.9.9"}\n';
    const site = http.createServer((q, r) => { r.writeHead(q.url === '/app-version.json' ? 200 : 404, { 'Content-Type': 'application/json', 'Content-Length': Buffer.byteLength(body) }); r.end(body); });
    await new Promise(r => site.listen(0, '127.0.0.1', r));
    const data = path.join(process.env.APPDATA, 'Hive');
    const seenFile = path.join(data, 'newer-seen.txt'), logF = path.join(data, 'app.log');
    fs.rmSync(seenFile, { force: true });
    const before = fs.existsSync(logF) ? fs.statSync(logF).size : 0;
    const env = { ...process.env, HIVE_SITE: `http://127.0.0.1:${site.address().port}` }; delete env.HIVE_APP_TEST;
    const start4 = links().find(x => /Programs\\Hive\.lnk$/i.test(x.Path));
    spawn(start4.Target, start4.Args ? start4.Args.split(/\s+/) : [], { env, stdio: 'ignore', detached: true }).unref();
    const said = () => { try { const b = fs.readFileSync(logF); return b.subarray(Math.min(before, b.length)).toString('utf8'); } catch { return ''; } };
    const t3 = Date.now(); let seen = '';
    while (Date.now() - t3 < 30000) {
      try { seen = fs.readFileSync(seenFile, 'utf8').trim(); } catch { }
      if (seen || /notification failed/.test(said())) break;
      await sleep(1000);
    }
    await sleep(1000);
    const lines = said().split(/\r?\n/).filter(x => /update notice|notification /.test(x)).map(x => x.replace(/^.*?(update notice|notification )/, '$1'));
    ps(`Get-Process -Name Hive | Stop-Process`); await sleep(2000); site.close();
    if (!lines.some(x => x === 'update notice 9.9.9')) throw new Error(`the installed app never noticed the stand-in site's 9.9.9 in 30 s (log: ${lines.join('; ') || 'nothing about it'})`);
    if (seen === '9.9.9') console.log(`ok toast: the installed app showed "A newer Hive app is available" for 9.9.9 in ${Math.round((Date.now() - t3) / 1000)} s (log: ${lines.join('; ')})`);
    else console.log(`no toast on this runner: newer-seen.txt ${seen ? 'says ' + seen : 'not written'} after 30 s; log: ${lines.join('; ')}`);
  }

  // 4. Uninstall it from Apps & features' own command; the app and its shortcuts go.
  const un = unexe;
  execFileSync(un, ['/S', '/currentuser'], { timeout: 5 * 60000, stdio: 'inherit' });
  const end2 = Date.now() + 3 * 60000;
  while ((entry() || fs.existsSync(bin) || links().length) && Date.now() < end2) await sleep(2000);
  const left = [entry() && 'the Apps & features entry', fs.existsSync(bin) && bin, ...links().map(x => x.Path)].filter(Boolean);
  if (left.length) throw new Error('left after the uninstall: ' + left.join(', '));
  console.log('ok setup-exe uninstall: no app, no shortcut, no Apps & features entry');
  process.exit(0);
})().catch(err => { console.error('FAILED', err.message || err); process.exit(1); });
