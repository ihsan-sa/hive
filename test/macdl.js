// node test/macdl.js dist/Hive-mac-x64.zip : on a throwaway Mac (a CI runner) that has the Hive from test/real.js, the zip
// a Mac person downloads, used the way they use it. The zip is unpacked as a browser leaves it (every file carries the
// quarantine mark), Hive.app is moved into /Applications (the README's step; ~/Applications/Hive.app is the Hive's own
// launcher icon and is not touched), Gatekeeper's refusal of the unsigned app is recorded, then the README's Open Anyway
// is done the way the README gives for the command line (xattr -dr com.apple.quarantine, which the README run showed does
// the same as steps 1 and 2). Opened from /Applications, the app must go straight to the Hive, never showing setup.
// The Open Anyway button itself is outside a runner's reach (test/downloaded.sh records the refusal screens).
const fs = require('fs');
const os = require('os');
const path = require('path');
const { execFileSync } = require('child_process');
const { _electron } = require('playwright-core');
const zip = process.argv[2];
if (process.platform !== 'darwin' || !zip || !fs.existsSync(zip)) { console.error('usage (Mac only): node test/macdl.js PATH-TO-Hive-mac-ARCH.zip'); process.exit(2); }
const sh = (cmd, args) => execFileSync(cmd, args, { encoding: 'utf8', timeout: 120000, stdio: ['ignore', 'pipe', 'pipe'] }).trim();
const sleep = ms => new Promise(r => setTimeout(r, ms));
const dest = '/Applications/Hive.app';
const hiveUrl = /^http:\/\/(localhost|127\.0\.0\.1):\d+\//;
(async () => {
  if (fs.existsSync(dest)) throw new Error(`${dest} is already there before the test`);
  // 1. Unpacked as the browser leaves it: the download mark on every file.
  const dl = fs.mkdtempSync(path.join(os.tmpdir(), 'hive-dl-'));
  sh('ditto', ['-x', '-k', zip, dl]);
  const unpacked = path.join(dl, 'Hive.app');
  if (!fs.existsSync(unpacked)) throw new Error('the zip has no Hive.app at its top: ' + fs.readdirSync(dl).join(', '));
  const mark = `0083;${Math.floor(Date.now() / 1000).toString(16)};Safari;${sh('uuidgen', [])}`;
  sh('xattr', ['-r', '-w', 'com.apple.quarantine', mark, unpacked]);
  // 2. Dragged into Applications: a move keeps the mark.
  sh('mv', [unpacked, dest]);
  const kept = sh('xattr', ['-p', 'com.apple.quarantine', dest]);
  if (!kept.startsWith('0083;')) throw new Error('the download mark did not survive the move: ' + kept);
  let verdict = '';
  try { verdict = sh('spctl', ['-a', '-vv', '-t', 'exec', dest]); } catch (e) { verdict = String(e.stderr || e.message); }
  console.log(`ok mac download mark: ${dest} carries the quarantine mark; Gatekeeper says: ${verdict.split('\n').map(s => s.trim()).filter(Boolean).join(' / ').slice(0, 200)}`);
  // 3. The README's Open Anyway, in its command-line form.
  sh('xattr', ['-dr', 'com.apple.quarantine', dest]);
  // 4. Opened from Applications: straight to the Hive.
  const t0 = Date.now();
  const app = await _electron.launch({ executablePath: path.join(dest, 'Contents/MacOS/Hive'), env: { ...process.env, HIVE_APP_TEST: '1' } });
  const page = await app.firstWindow();
  const where = () => app.evaluate(({ BrowserWindow }) => BrowserWindow.getAllWindows().map(w => w.webContents.getURL().split('#')[0])[0] || '').catch(() => '');
  const title = () => page.evaluate(() => document.querySelector('#title')?.textContent || '').catch(() => '');
  const end = Date.now() + 4 * 60000; let at = '', t = '', sawSetup = false;
  for (;;) {
    at = await where(); t = await title();
    if (t === 'Set up the Hive' || t === 'Before it installs') sawSetup = true;
    if (hiveUrl.test(at) || Date.now() > end) break;
    await sleep(500);
  }
  fs.mkdirSync('test/shots', { recursive: true });
  await page.screenshot({ path: 'test/shots/macdl.png' }).catch(() => { });
  await app.close();
  fs.rmSync(dest, { recursive: true, force: true });
  if (!hiveUrl.test(at)) throw new Error(`the app from ${dest} never reached the Hive (at "${at}", "${t}"); is the Hive from test/real.js there?`);
  if (sawSetup) throw new Error(`the app from ${dest} showed the setup screen ("${t}") before the Hive`);
  console.log(`ok mac download launch: ${dest} opens straight to the Hive at ${at} in ${Math.round((Date.now() - t0) / 1000)} s`);
})().catch(err => { console.error('FAILED', err.message || err); try { fs.rmSync(dest, { recursive: true, force: true }); } catch { } process.exit(1); });
