// node test/setupexe.js dist/Hive-Setup.exe : on a throwaway Windows machine (a CI runner), the file a Windows person
// downloads, used the way they use it. The installer gets the browser's download mark (Zone.Identifier, internet zone),
// installs silently for this account (it is one-click, per user), and must leave the app, its Start menu and desktop
// shortcuts and its Apps & features entry. The app is then started by its Start menu shortcut and must reach the Hive
// (or, on a machine with no Hive, its setup screen, said so). Last, the uninstaller from Apps & features must take the
// app and its shortcuts away again. The SmartScreen prompt a double-click shows is outside a runner's reach (README).
// With a second installer and its version (node test/setupexe.js OLD.exe NEW.exe 0.1.1), the update a person does: the
// window is moved and sized while the old version runs, the newer installer (also download-marked) goes over it, and
// there must then be one Apps & features entry, at the new version, in the same folder; opened from the Start menu the
// new version must keep the window where it was and reach the Hive (or setup). The uninstall then runs on the new one.
const fs = require('fs');
const os = require('os');
const path = require('path');
const { execFileSync } = require('child_process');
const { _electron } = require('playwright-core');
const exe = process.argv[2];
if (process.platform !== 'win32' || !exe || !fs.existsSync(exe)) { console.error('usage (Windows only): node test/setupexe.js PATH-TO-Hive-Setup.exe'); process.exit(2); }
const version = require('../package.json').version;
const [newer, newVersion] = [process.argv[3], process.argv[4]];
if (newer && (!fs.existsSync(newer) || !newVersion)) { console.error('usage: node test/setupexe.js OLD.exe [NEW.exe NEW-VERSION]'); process.exit(2); }
// A fresh account may have no Uninstall key at all, and an unreadable subkey makes Get-ItemProperty complain: neither is a
// failure here, so errors are silenced and the exit is 0; anything PowerShell itself cannot run still shows its stderr.
const ps = cmd => {
  try {
    return execFileSync('powershell.exe', ['-NoProfile', '-NonInteractive', '-Command', `$ErrorActionPreference = 'SilentlyContinue'; ${cmd}; exit 0`], { encoding: 'utf8', timeout: 180000, stdio: ['ignore', 'pipe', 'pipe'] }).trim();
  } catch (err) { throw new Error(`PowerShell failed (${err.status}): ${(err.stderr || '').toString().trim().slice(0, 800) || err.message.slice(0, 300)}`); }
};
const sleep = ms => new Promise(r => setTimeout(r, ms));
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
const hiveAt = /^http:\/\/(localhost|127\.0\.0\.1):\d+\//;
// Started the way a person does, from its Start menu shortcut; resolves once the Hive or the setup screen shows.
async function openFromStart(start, shot) {
  const app = await _electron.launch({ executablePath: start.Target, args: start.Args ? start.Args.split(/\s+/) : [], env: { ...process.env, HIVE_APP_TEST: '1' } });
  const page = await app.firstWindow();
  const where = () => app.evaluate(({ BrowserWindow }) => BrowserWindow.getAllWindows().map(w => w.webContents.getURL().split('#')[0])[0] || '').catch(() => '');
  const title = () => page.evaluate(() => document.querySelector('#title')?.textContent || '').catch(() => '');
  const end = Date.now() + 4 * 60000; let at = '', t = '';
  for (;;) {
    at = await where(); t = await title();
    if (hiveAt.test(at) || t === 'Set up the Hive' || Date.now() > end) break;
    await sleep(1000);
  }
  fs.mkdirSync('test/shots', { recursive: true });
  await page.screenshot({ path: `test/shots/${shot}-${process.arch}.png` }).catch(() => { });
  const reached = hiveAt.test(at) ? `the Hive at ${at}` : t === 'Set up the Hive' ? 'the setup screen (no Hive on this machine)' : '';
  if (!reached) { await app.close().catch(() => { }); throw new Error(`the installed app never showed the Hive or its setup screen (at "${at}", "${t}")`); }
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
    await app.evaluate(({ BrowserWindow }) => { const w = BrowserWindow.getAllWindows()[0]; w.unmaximize(); w.setBounds({ x: 37, y: 23, width: 811, height: 577 }); });
    await sleep(1500);
    kept = await bounds(app);
  }
  await app.close();
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
    const running = await app.evaluate(({ app: a }) => a.getVersion());
    const now = await bounds(app);
    await app.close();
    if (running !== newVersion) throw new Error(`the Start menu shortcut opens version ${running}, not ${newVersion}`);
    const off = kept && now ? Math.max(...['x', 'y', 'width', 'height'].map(k => Math.abs(kept[k] - now[k]))) : Infinity;
    if (off > 2) throw new Error(`the update lost the window's place: before ${JSON.stringify(kept)}, after ${JSON.stringify(now)}`);
    console.log(`ok setup-exe upgrade: ${version} to ${newVersion} in ${Math.round((Date.now() - t1) / 1000)} s; one Apps & features entry, "${e2[0].DisplayName}" ${e2[0].DisplayVersion}, same folder; window kept at ${now.width}x${now.height}+${now.x}+${now.y}; ${newVersion} opens ${reached}`);
    unexe = unexe2;
  }

  // 4. Uninstall it from Apps & features' own command; the app and its shortcuts go.
  const un = unexe;
  execFileSync(un, ['/S', '/currentuser'], { timeout: 5 * 60000, stdio: 'inherit' });
  const end2 = Date.now() + 3 * 60000;
  while ((entry() || fs.existsSync(bin) || links().length) && Date.now() < end2) await sleep(2000);
  const left = [entry() && 'the Apps & features entry', fs.existsSync(bin) && bin, ...links().map(x => x.Path)].filter(Boolean);
  if (left.length) throw new Error('left after the uninstall: ' + left.join(', '));
  console.log('ok setup-exe uninstall: no app, no shortcut, no Apps & features entry');
})().catch(err => { console.error('FAILED', err.message || err); process.exit(1); });
