// node test/deb.js : on a throwaway Linux machine (a CI runner) where the .deb is installed with apt and a Hive is already installed:
// starts the app by the command in its menu entry (hive-app.desktop) and checks the window reaches the Hive signed in.
// With a newer .deb and its version (node test/deb.js NEW.deb 0.1.1), the update a Linux person does: the window is moved
// and sized while the installed version runs, the newer .deb goes over it with `sudo apt-get install ./NEW.deb`, and then
// there must be one hive-app package at the new version, and the menu entry's command must start the new version, keep
// the window's place (its data folder is outside the package) and reach the Hive.
// With a third .deb (... NEWEST.deb 0.1.2), the update with the app still open: apt puts the newest .deb in while the
// previous version runs, as a person does who updates from the app's notice without closing it. The open app must keep
// working (its window reloads the Hive and a new window opens, both needing files the update replaced under it), then
// there must be one package at the newest version and the menu command must open it at the Hive.
const fs = require('fs');
const path = require('path');
const { execFileSync } = require('child_process');
const { _electron } = require('playwright-core');
const [newer, newVersion, newest, newestVersion] = process.argv.slice(2, 6);
const entry = '/usr/share/applications/hive-app.desktop';
if (!fs.existsSync(entry)) { console.error('no menu entry at ' + entry + ': install the .deb first'); process.exit(2); }
if ((newer && (!fs.existsSync(newer) || !newVersion)) || (newest && (!newer || !fs.existsSync(newest) || !newestVersion))) { console.error('usage: node test/deb.js [NEW.deb NEW-VERSION [NEWEST.deb NEWEST-VERSION]]'); process.exit(2); }
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
  const said = []; for (const s of [app.process().stdout, app.process().stderr]) s?.on('data', d => said.push(...String(d).split('\n').filter(Boolean)));
  await app.firstWindow();
  const where = () => app.evaluate(({ BrowserWindow }) => BrowserWindow.getAllWindows().map(w => (w.contentView.children.at(-1)?.webContents ?? w.webContents).getURL().split('#')[0])[0] || '').catch(() => '');
  const end = Date.now() + 4 * 60000; let at = '';
  while (!signedIn(at = await where())) {
    if (Date.now() > end) {
      // What the app showed, for a run that can't be watched: every window and view, a picture, and what the app printed.
      const seen = await app.evaluate(({ BrowserWindow }) => BrowserWindow.getAllWindows().map(w => ({ title: w.getTitle(), shown: w.isVisible(),
        views: [w.webContents, ...w.contentView.children.map(v => v.webContents).filter(Boolean)].map(c => ({ url: c.getURL().split('#')[0], loading: c.isLoading(), crashed: c.isCrashed() })) }))).catch(e => 'no answer: ' + e.message);
      const win = await app.firstWindow().catch(() => null);
      if (win) await win.screenshot({ path: path.join(__dirname, 'shots', 'deb-not-signed-in.png') }).catch(() => { });
      await app.close().catch(() => { });
      throw new Error(`the window never reached the Hive signed in (at "${at}"); windows ${JSON.stringify(seen)}; the app said: ${said.slice(-20).join(' | ').replace(/#\S+/g, '#…') || 'nothing'}`);
    }
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
  if (!newest) return;
  // The update with the app open: apt replaces /opt/Hive under the running app.
  ({ app, exec, at } = await open());
  const pid = app.process().pid;
  const from = await app.evaluate(({ app: a }) => a.getVersion());
  const t2 = Date.now();
  sh('sudo', ['apt-get', 'install', '-y', path.resolve(newest)]);
  const took = Math.round((Date.now() - t2) / 1000);
  const alive = () => { try { process.kill(pid, 0); return true; } catch { return false; } };
  if (!alive()) throw new Error(`apt put ${newestVersion} in while ${from} was open, and the open app (pid ${pid}) is gone`);
  // Does the open app still work? Its window reloads the Hive (a fresh page load in its renderer) and a second window
  // opens (a new renderer process, started from the files apt just replaced).
  const answer = (async () => {
    await app.evaluate(({ BrowserWindow }) => { const w = BrowserWindow.getAllWindows()[0]; (w.contentView.children.at(-1)?.webContents ?? w.webContents).reload(); });
    const end = Date.now() + 60000; let u = '';
    while (Date.now() < end) {
      await sleep(1000);
      u = await app.evaluate(({ BrowserWindow }) => { const w = BrowserWindow.getAllWindows()[0], c = w && (w.contentView.children.at(-1)?.webContents ?? w.webContents); return c && !c.isLoading() ? c.getURL().split('#')[0] : ''; }).catch(() => '');
      if (signedIn(u)) break;
    }
    if (!signedIn(u)) throw new Error(`after the update the open ${from} window, reloaded, did not reach the Hive again (at "${u}")`);
    const second = await app.evaluate(async ({ BrowserWindow }, url) => {
      const w = new BrowserWindow({ show: true, width: 400, height: 300 });
      const gone = new Promise(r => w.webContents.once('render-process-gone', (_e, d) => r('renderer gone: ' + d.reason)));
      const loaded = w.loadURL(url).then(async () => { const t = await w.webContents.executeJavaScript('document.title'); return 'loaded: ' + t; }, e => 'load failed: ' + e.message);
      const r = await Promise.race([gone, loaded, new Promise(r => setTimeout(() => r('no answer in 30 s'), 30000))]);
      w.destroy(); return r;
    }, u);
    if (!/^loaded/.test(second)) throw new Error(`after the update a new window from the open ${from} app: ${second}`);
    return `its window reloaded the Hive at ${u} and a new window ${second.replace(/^loaded: /, 'loaded "') + '"'}`;
  })();
  const fine = await Promise.race([answer, sleep(120000).then(() => { throw new Error(`after the update the open ${from} app gave no answer in 2 min`); })]);
  await Promise.race([app.close().catch(() => { }), sleep(15000)]);
  const now2 = installed();
  if (now2.length !== 1 || now2[0].split(' ')[1] !== newestVersion) throw new Error(`after the update with the app open, the installed packages are ${JSON.stringify(now2)}, not one hive-app at ${newestVersion}`);
  ({ app, exec, at } = await open());
  const running2 = await app.evaluate(({ app: a }) => a.getVersion());
  await app.close();
  if (running2 !== newestVersion) throw new Error(`after the update with the app open, the menu command runs ${running2}, not ${newestVersion}`);
  console.log(`ok deb upgrade while open: ${from} open (pid ${pid}), ${newestVersion} put in with apt in ${took} s; the open app kept working: ${fine}; one package, "${now2[0].replace(/\s+ii\s*$/, '')}"; ${newestVersion} from the menu command reaches the Hive at ${at}`);
})().catch(e => { console.error('FAILED', e.message || e); process.exit(1); });
