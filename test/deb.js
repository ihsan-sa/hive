// node test/deb.js : on a throwaway Linux machine (a CI runner) where the .deb is installed with apt and a Hive is already installed:
// starts the app by the command in its menu entry (hive-app.desktop) and checks the window reaches the Hive signed in.
// With a newer .deb and its version (node test/deb.js NEW.deb 0.1.1), the update a Linux person does: the window is moved
// and sized while the installed version runs, the newer .deb goes over it with `sudo apt-get install ./NEW.deb`, and then
// there must be one hive-app package at the new version, and the menu entry's command must start the new version, keep
// the window's place (its data folder is outside the package) and reach the Hive.
const fs = require('fs');
const path = require('path');
const { execFileSync } = require('child_process');
const { _electron } = require('playwright-core');
const [newer, newVersion] = process.argv.slice(2);
const entry = '/usr/share/applications/hive-app.desktop';
if (!fs.existsSync(entry)) { console.error('no menu entry at ' + entry + ': install the .deb first'); process.exit(2); }
if (newer && (!fs.existsSync(newer) || !newVersion)) { console.error('usage: node test/deb.js [NEW.deb NEW-VERSION]'); process.exit(2); }
const sh = (cmd, args) => execFileSync(cmd, args, { encoding: 'utf8', timeout: 300000, stdio: ['ignore', 'pipe', 'pipe'] }).trim();
const sleep = ms => new Promise(r => setTimeout(r, ms));
// The app opens the Hive at /login#key, which signs in and goes on; only a page past /login is the Hive signed in.
const signedIn = u => /^http:\/\/(localhost|127\.0\.0\.1):\d+\/(?!login\b)/.test(u);
const menuCommand = () => {
  const exec = (fs.readFileSync(entry, 'utf8').match(/^Exec=(.+)$/m) || [])[1];
  if (!exec) throw new Error('the menu entry has no Exec line');
  const words = exec.replace(/%[a-zA-Z]/g, '').trim().split(/\s+/).map(w => w.replace(/^"|"$/g, ''));
  if (!fs.existsSync(words[0])) throw new Error(`the menu entry starts ${words[0]}, which isn't there`);
  return { exec, words };
};
async function open() {
  const { exec, words } = menuCommand();
  const app = await _electron.launch({ executablePath: words[0], args: [...words.slice(1), '--no-sandbox'], env: { ...process.env, HIVE_APP_TEST: '1' } });
  await app.firstWindow();
  const where = () => app.evaluate(({ BrowserWindow }) => BrowserWindow.getAllWindows().map(w => w.webContents.getURL().split('#')[0])[0] || '').catch(() => '');
  const end = Date.now() + 4 * 60000; let at = '';
  while (!signedIn(at = await where())) {
    if (Date.now() > end) { await app.close().catch(() => { }); throw new Error(`the window never reached the Hive signed in (at "${at}")`); }
    await sleep(1000);
  }
  return { app, exec, at };
}
const bounds = app => app.evaluate(({ BrowserWindow }) => { const w = BrowserWindow.getAllWindows()[0]; return w ? w.getNormalBounds() : null; });
const installed = () => sh('dpkg-query', ['-W', '-f', '${Package} ${Version} ${db:Status-Abbrev}\\n', 'hive-app*']).split('\n').filter(l => /\bii\s*$/.test(l));
(async () => {
  let { app, exec, at } = await open();
  console.log(`ok deb launch: ${exec} -> ${at}`);
  if (!newer) return app.close();
  // The person moves and sizes the window; the app keeps that in its data folder.
  await app.evaluate(({ BrowserWindow }) => { BrowserWindow.getAllWindows()[0].setBounds({ x: 37, y: 83, width: 811, height: 577 }); });
  await sleep(1500);
  const kept = await bounds(app);
  await app.close();
  const old = installed();
  const t1 = Date.now();
  sh('sudo', ['apt-get', 'install', '-y', path.resolve(newer)]);
  const now = installed();
  if (now.length !== 1 || now[0].split(' ')[1] !== newVersion) throw new Error(`after the update, the installed packages are ${JSON.stringify(now)}, not one hive-app at ${newVersion}`);
  ({ app, exec, at } = await open());
  const running = await app.evaluate(({ app: a }) => a.getVersion());
  const after = await bounds(app);
  await app.close();
  if (running !== newVersion) throw new Error(`after the update the menu command runs ${running}, not ${newVersion}`);
  const off = kept && after ? Math.max(...['x', 'y', 'width', 'height'].map(k => Math.abs(kept[k] - after[k]))) : Infinity;
  if (off > 2) throw new Error(`the update lost the window's place: before ${JSON.stringify(kept)}, after ${JSON.stringify(after)}`);
  console.log(`ok deb upgrade: ${(old[0] || '?').split(' ')[1]} to ${newVersion} with apt in ${Math.round((Date.now() - t1) / 1000)} s; one package, "${now[0].replace(/\s+ii\s*$/, '')}"; window kept at ${after.width}x${after.height}+${after.x}+${after.y}; ${newVersion} from the menu command reaches the Hive at ${at}`);
})().catch(e => { console.error('FAILED', e.message || e); process.exit(1); });
