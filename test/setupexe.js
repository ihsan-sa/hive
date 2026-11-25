// node test/setupexe.js dist/Hive-Setup.exe : on a throwaway Windows machine (a CI runner), the file a Windows person
// downloads, used the way they use it. The installer gets the browser's download mark (Zone.Identifier, internet zone),
// installs silently for this account (it is one-click, per user), and must leave the app, its Start menu and desktop
// shortcuts and its Apps & features entry. The app is then started by its Start menu shortcut and must reach the Hive
// (or, on a machine with no Hive, its setup screen, said so). Last, the uninstaller from Apps & features must take the
// app and its shortcuts away again. The SmartScreen prompt a double-click shows is outside a runner's reach (README).
const fs = require('fs');
const os = require('os');
const path = require('path');
const { execFileSync } = require('child_process');
const { _electron } = require('playwright-core');
const exe = process.argv[2];
if (process.platform !== 'win32' || !exe || !fs.existsSync(exe)) { console.error('usage (Windows only): node test/setupexe.js PATH-TO-Hive-Setup.exe'); process.exit(2); }
const version = require('../package.json').version;
// A fresh account may have no Uninstall key at all, and an unreadable subkey makes Get-ItemProperty complain: neither is a
// failure here, so errors are silenced and the exit is 0; anything PowerShell itself cannot run still shows its stderr.
const ps = cmd => {
  try {
    return execFileSync('powershell.exe', ['-NoProfile', '-NonInteractive', '-Command', `$ErrorActionPreference = 'SilentlyContinue'; ${cmd}; exit 0`], { encoding: 'utf8', timeout: 180000, stdio: ['ignore', 'pipe', 'pipe'] }).trim();
  } catch (err) { throw new Error(`PowerShell failed (${err.status}): ${(err.stderr || '').toString().trim().slice(0, 800) || err.message.slice(0, 300)}`); }
};
const sleep = ms => new Promise(r => setTimeout(r, ms));
const entry = () => {
  const out = ps(`Get-ChildItem HKCU:\\Software\\Microsoft\\Windows\\CurrentVersion\\Uninstall -ErrorAction SilentlyContinue | Get-ItemProperty | Where-Object { $_.DisplayName -like 'Hive*' -and $_.UninstallString } | Select-Object DisplayName,DisplayVersion,InstallLocation,UninstallString | ConvertTo-Json -Compress`);
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
(async () => {
  // 1. The download mark a browser puts on the file.
  const copy = path.join(fs.mkdtempSync(path.join(os.tmpdir(), 'hive-dl-')), 'Hive-Setup.exe');
  fs.copyFileSync(exe, copy);
  fs.writeFileSync(copy + ':Zone.Identifier', '[ZoneTransfer]\r\nZoneId=3\r\nReferrerUrl=https://hivelab.ca/\r\nHostUrl=https://hivelab.ca/Hive-Setup.exe\r\n');
  const mark = fs.readFileSync(copy + ':Zone.Identifier', 'utf8');
  if (!/ZoneId=3/.test(mark)) throw new Error('the download mark did not stick: ' + mark);
  console.log(`ok download mark: ${copy} carries ZoneId=3 (${fs.statSync(copy).size} bytes)`);
  if (entry()) throw new Error('an app entry is already in Apps & features before the install: ' + JSON.stringify(entry()));

  // 2. Install, as one-click does, for this account only (no admin prompt).
  const t0 = Date.now();
  execFileSync(copy, ['/S'], { timeout: 5 * 60000, stdio: 'inherit' });
  let e = null;
  for (let i = 0; i < 60 && !(e = entry()); i++) await sleep(2000);
  if (!e) throw new Error('no Hive entry in Apps & features after the install');
  if (e.length !== 1) throw new Error('more than one Hive entry in Apps & features: ' + JSON.stringify(e));
  e = e[0];
  const bin = path.join(e.InstallLocation, 'Hive.exe');
  if (!fs.existsSync(bin)) throw new Error(`Apps & features says ${e.InstallLocation}, but there is no Hive.exe in it`);
  if (!e.InstallLocation.toLowerCase().startsWith((process.env.LOCALAPPDATA || '').toLowerCase())) throw new Error(`installed outside this account's folder: ${e.InstallLocation}`);
  if (e.DisplayVersion !== version) throw new Error(`Apps & features shows version ${e.DisplayVersion}, the app is ${version}`);
  const l = links();
  const start = l.find(x => /Programs\\Hive\.lnk$/i.test(x.Path));
  if (!start) throw new Error('no Hive shortcut in the Start menu: ' + JSON.stringify(l));
  for (const x of l) if (path.resolve(x.Target).toLowerCase() !== path.resolve(bin).toLowerCase()) throw new Error(`${x.Path} opens ${x.Target}, not the installed app`);
  console.log(`ok setup-exe install: ${Math.round((Date.now() - t0) / 1000)} s, "${e.DisplayName}" ${e.DisplayVersion} in ${e.InstallLocation}; shortcuts: ${l.map(x => x.Path).join(', ')}`);

  // 3. Start it the way a person does: the Start menu shortcut.
  const app = await _electron.launch({ executablePath: start.Target, args: start.Args ? start.Args.split(/\s+/) : [], env: { ...process.env, HIVE_APP_TEST: '1' } });
  const page = await app.firstWindow();
  const where = () => app.evaluate(({ BrowserWindow }) => BrowserWindow.getAllWindows().map(w => w.webContents.getURL().split('#')[0])[0] || '').catch(() => '');
  const title = () => page.evaluate(() => document.querySelector('#title')?.textContent || '').catch(() => '');
  const end = Date.now() + 4 * 60000; let at = '', t = '';
  for (;;) {
    at = await where(); t = await title();
    if (/^http:\/\/(localhost|127\.0\.0\.1):\d+\//.test(at) || t === 'Set up the Hive' || Date.now() > end) break;
    await sleep(1000);
  }
  fs.mkdirSync('test/shots', { recursive: true });
  await page.screenshot({ path: `test/shots/setupexe-${process.arch}.png` }).catch(() => { });
  await app.close();
  if (/^http:\/\/(localhost|127\.0\.0\.1):\d+\//.test(at)) console.log(`ok setup-exe launch: the Start menu shortcut opens the Hive at ${at}`);
  else if (t === 'Set up the Hive') console.log('ok setup-exe launch: the Start menu shortcut opens the setup screen (no Hive on this machine)');
  else throw new Error(`the installed app never showed the Hive or its setup screen (at "${at}", "${t}")`);

  // 4. Uninstall it from Apps & features' own command; the app and its shortcuts go.
  const un = (e.UninstallString.match(/^"([^"]+)"/) || [null, e.UninstallString.split(' /')[0]])[1];
  execFileSync(un, ['/S', '/currentuser'], { timeout: 5 * 60000, stdio: 'inherit' });
  const end2 = Date.now() + 3 * 60000;
  while ((entry() || fs.existsSync(bin) || links().length) && Date.now() < end2) await sleep(2000);
  const left = [entry() && 'the Apps & features entry', fs.existsSync(bin) && bin, ...links().map(x => x.Path)].filter(Boolean);
  if (left.length) throw new Error('left after the uninstall: ' + left.join(', '));
  console.log('ok setup-exe uninstall: no app, no shortcut, no Apps & features entry');
})().catch(err => { console.error('FAILED', err.message || err); process.exit(1); });
