// node test/macdl.js dist/Hive-mac-x64.zip : on a throwaway Mac (a CI runner) that has the Hive from test/real.js, the zip
// a Mac person downloads, used the way they use it. The zip is unpacked as a browser leaves it (every file carries the
// quarantine mark), Hive.app is moved into /Applications (the README's step; ~/Applications/Hive.app is the Hive's own
// launcher icon and is not touched), Gatekeeper's refusal of the unsigned app is recorded, then the README's Open Anyway
// is done the way the README gives for the command line (xattr -dr com.apple.quarantine, which the README run showed does
// the same as steps 1 and 2). Opened from /Applications, the app must go straight to the Hive, never showing setup.
// The Open Anyway button itself is outside a runner's reach (test/downloaded.sh records the refusal screens).
// With a second zip and its version (node test/macdl.js OLD.zip NEW.zip 0.1.1), the update a Mac person does: the window
// is moved and sized while the old version runs, the newer zip (download-marked too) is dragged over /Applications/Hive.app
// (the Finder's Replace: the old bundle goes, the new one takes its place), Open Anyway again, and opened from there it
// must be the new version, keep the window's place (its data folder is outside the bundle) and reach the Hive.
const fs = require('fs');
const os = require('os');
const path = require('path');
const { execFileSync } = require('child_process');
const { _electron } = require('playwright-core');
const [zip, newer, newVersion] = process.argv.slice(2);
if (process.platform !== 'darwin' || !zip || !fs.existsSync(zip)) { console.error('usage (Mac only): node test/macdl.js PATH-TO-Hive-mac-ARCH.zip'); process.exit(2); }
const sh = (cmd, args) => execFileSync(cmd, args, { encoding: 'utf8', timeout: 120000, stdio: ['ignore', 'pipe', 'pipe'] }).trim();
const sleep = ms => new Promise(r => setTimeout(r, ms));
const dest = '/Applications/Hive.app';
const hiveUrl = /^http:\/\/(localhost|127\.0\.0\.1):\d+\//;
// The app opens the Hive at /login#key, which signs in and goes on; only a page past /login is the Hive signed in.
const signedIn = u => hiveUrl.test(u) && !/^http:\/\/[^/]+\/login\b/.test(u);
if (newer && (!fs.existsSync(newer) || !newVersion)) { console.error('usage: node test/macdl.js OLD.zip [NEW.zip NEW-VERSION]'); process.exit(2); }
// Unpacked as the browser leaves it (the download mark on every file), then dragged into Applications (a move keeps the mark).
function download(file) {
  const dl = fs.mkdtempSync(path.join(os.tmpdir(), 'hive-dl-'));
  sh('ditto', ['-x', '-k', file, dl]);
  const unpacked = path.join(dl, 'Hive.app');
  if (!fs.existsSync(unpacked)) throw new Error('the zip has no Hive.app at its top: ' + fs.readdirSync(dl).join(', '));
  const mark = `0083;${Math.floor(Date.now() / 1000).toString(16)};Safari;${sh('uuidgen', [])}`;
  sh('xattr', ['-r', '-w', 'com.apple.quarantine', mark, unpacked]);
  if (fs.existsSync(dest)) fs.rmSync(dest, { recursive: true, force: true }); // the Finder's Replace
  sh('mv', [unpacked, dest]);
  const kept = sh('xattr', ['-p', 'com.apple.quarantine', dest]);
  if (!kept.startsWith('0083;')) throw new Error('the download mark did not survive the move: ' + kept);
  let verdict = '';
  try { verdict = sh('spctl', ['-a', '-vv', '-t', 'exec', dest]); } catch (e) { verdict = String(e.stderr || e.message); }
  console.log(`ok mac download mark: ${dest} carries the quarantine mark; Gatekeeper says: ${verdict.split('\n').map(s => s.trim()).filter(Boolean).join(' / ').slice(0, 200)}`);
  // The README's Open Anyway, in its command-line form.
  sh('xattr', ['-dr', 'com.apple.quarantine', dest]);
}
// Opened from Applications: it must reach the Hive without showing setup first.
async function open(shot) {
  const app = await _electron.launch({ executablePath: path.join(dest, 'Contents/MacOS/Hive'), env: { ...process.env, HIVE_APP_TEST: '1' } });
  const page = await app.firstWindow();
  const where = () => app.evaluate(({ BrowserWindow }) => BrowserWindow.getAllWindows().map(w => w.webContents.getURL().split('#')[0])[0] || '').catch(() => '');
  const title = () => page.evaluate(() => document.querySelector('#title')?.textContent || '').catch(() => '');
  const end = Date.now() + 4 * 60000; let at = '', t = '', sawSetup = false;
  for (;;) {
    at = await where(); t = await title();
    if (t === 'Set up the Hive' || t === 'Before it installs') sawSetup = true;
    if (signedIn(at) || Date.now() > end) break;
    await sleep(500);
  }
  fs.mkdirSync('test/shots', { recursive: true });
  await page.screenshot({ path: `test/shots/${shot}.png` }).catch(() => { });
  const fail = !signedIn(at) ? `the app from ${dest} never reached the Hive signed in (at "${at}", "${t}"); is the Hive from test/real.js there?`
    : sawSetup ? `the app from ${dest} showed the setup screen ("${t}") before the Hive` : '';
  if (fail) { await app.close().catch(() => { }); throw new Error(fail); }
  const name = await page.title().catch(() => '');
  return { app, at: `${at} (${name})` };
}
const bounds = app => app.evaluate(({ BrowserWindow }) => { const w = BrowserWindow.getAllWindows()[0]; return w ? w.getNormalBounds() : null; });
(async () => {
  if (fs.existsSync(dest)) throw new Error(`${dest} is already there before the test`);
  download(zip);
  const t0 = Date.now();
  let { app, at } = await open('macdl');
  console.log(`ok mac download launch: ${dest} opens straight to the Hive at ${at} in ${Math.round((Date.now() - t0) / 1000)} s`);
  let kept = null;
  if (newer) {
    // The person moves and sizes the window; the app keeps that in its data folder.
    await app.evaluate(({ BrowserWindow }) => { const w = BrowserWindow.getAllWindows()[0]; w.setBounds({ x: 37, y: 83, width: 811, height: 577 }); });
    await sleep(1500);
    kept = await bounds(app);
  }
  await app.close();
  if (newer) {
    const old = sh('plutil', ['-extract', 'CFBundleShortVersionString', 'raw', path.join(dest, 'Contents/Info.plist')]);
    const t1 = Date.now();
    download(newer);
    const bundle = sh('plutil', ['-extract', 'CFBundleShortVersionString', 'raw', path.join(dest, 'Contents/Info.plist')]);
    ({ app, at } = await open('macdl-update'));
    const running = await app.evaluate(({ app: a }) => a.getVersion());
    const now = await bounds(app);
    await app.close();
    if (bundle !== newVersion || running !== newVersion) throw new Error(`after the update, the bundle says ${bundle} and the app runs ${running}, not ${newVersion}`);
    const off = kept && now ? Math.max(...['x', 'y', 'width', 'height'].map(k => Math.abs(kept[k] - now[k]))) : Infinity;
    if (off > 2) throw new Error(`the update lost the window's place: before ${JSON.stringify(kept)}, after ${JSON.stringify(now)}`);
    console.log(`ok mac download upgrade: ${old} to ${newVersion} dragged over ${dest} in ${Math.round((Date.now() - t1) / 1000)} s; window kept at ${now.width}x${now.height}+${now.x}+${now.y}; ${newVersion} opens straight to the Hive at ${at}`);
  }
  fs.rmSync(dest, { recursive: true, force: true });
})().catch(err => { console.error('FAILED', err.message || err); try { fs.rmSync(dest, { recursive: true, force: true }); } catch { } process.exit(1); });
