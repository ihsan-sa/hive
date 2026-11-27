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
// With HIVE_DL_EXPECT=setup (the Apple silicon runner, where no Hive can run) the app must instead open on its setup
// screen, and never on a Hive page; everything else is the same.
// Last, the update notice: opened with open -a against a stand-in site with a newer app. ok mac notice says macOS took the
// notification, with the Hive's notification setting in words; if that is off, a second round with it turned on. ok mac
// banner only when a screenshot (read by macOS's own text recognition, test/macbanner.swift) shows the banner. Then a control
// notification from osascript: ok mac control banner if the runner draws one at all.
// Red only if the app never noticed the newer one.
const fs = require('fs');
const os = require('os');
const path = require('path');
const { execFileSync } = require('child_process');
const { _electron } = require('playwright-core');
const [zip, newer, newVersion] = process.argv.slice(2);
if (process.platform !== 'darwin' || !zip || !fs.existsSync(zip)) { console.error('usage (Mac only): [HIVE_DL_EXPECT=setup] node test/macdl.js PATH-TO-Hive-mac-ARCH.zip'); process.exit(2); }
const sh = (cmd, args) => execFileSync(cmd, args, { encoding: 'utf8', timeout: 120000, stdio: ['ignore', 'pipe', 'pipe'] }).trim();
const sleep = ms => new Promise(r => setTimeout(r, ms));
const dest = '/Applications/Hive.app';
const hiveUrl = /^http:\/\/(localhost|127\.0\.0\.1):\d+\//;
// The app opens the Hive at /login#key, which signs in and goes on; only a page past /login is the Hive signed in.
const signedIn = u => hiveUrl.test(u) && !/^http:\/\/[^/]+\/login\b/.test(u);
const wantSetup = process.env.HIVE_DL_EXPECT === 'setup';
// HIVE_DL_ROSETTA=1: the Intel zip on an Apple chip Mac, whose setup screen must say the Apple chip download opens faster.
const wantRosetta = process.env.HIVE_DL_ROSETTA === '1';
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
// Opened from Applications: it must reach the Hive without showing setup first (or, with HIVE_DL_EXPECT=setup, show setup).
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
async function open(shot) {
  const app = await _electron.launch({ executablePath: path.join(dest, 'Contents/MacOS/Hive'), env: { ...process.env, HIVE_APP_TEST: '1' } });
  const page = await ownPage(app);
  const where = () => app.evaluate(({ BrowserWindow }) => BrowserWindow.getAllWindows().map(w => (w.contentView.children.at(-1)?.webContents ?? w.webContents).getURL().split('#')[0])[0] || '').catch(() => '');
  const title = () => page.evaluate(() => document.querySelector('#title')?.textContent || '').catch(() => '');
  const end = Date.now() + 4 * 60000; let at = '', t = '', sawSetup = false;
  for (;;) {
    at = await where(); t = await title();
    if (t === 'Set up the Hive' || t === 'Before it installs') sawSetup = true;
    if ((wantSetup ? t === 'Set up the Hive' || hiveUrl.test(at) : signedIn(at)) || Date.now() > end) break;
    await sleep(500);
  }
  const rosetta = await app.evaluate(({ app: a }) => !!a.runningUnderARM64Translation).catch(() => null);
  const line = await page.evaluate(() => document.querySelector('#rosetta')?.textContent || '').catch(() => '');
  console.log(`mac rosetta: runningUnderARM64Translation is ${rosetta}; ${line ? `setup line: "${line}"` : 'no setup line'}`);
  const rosettaFail = rosetta !== wantRosetta ? `the app says runningUnderARM64Translation is ${rosetta}, not ${wantRosetta}`
    : wantSetup && !!line !== wantRosetta ? (line ? `the setup screen says "${line}" on a Mac where the app is native` : 'the setup screen under Rosetta has no line about the Apple chip download') : '';
  fs.mkdirSync('test/shots', { recursive: true });
  await (frontPage(app, at) || page).screenshot({ path: `test/shots/${shot}.png` }).catch(() => { });
  const fail = wantSetup ? (hiveUrl.test(at) ? `the app from ${dest} opened a Hive page (${at}) on a machine with no Hive`
      : t !== 'Set up the Hive' ? `the app from ${dest} never showed its setup screen (at "${at}", "${t}")` : rosettaFail)
    : !signedIn(at) ? `the app from ${dest} never reached the Hive signed in (at "${at}", "${t}"); is the Hive from test/real.js there?`
    : sawSetup ? `the app from ${dest} showed the setup screen ("${t}") before the Hive` : rosettaFail;
  if (fail) { await app.close().catch(() => { }); throw new Error(fail); }
  const name = await (frontPage(app, at) || page).title().catch(() => '');
  return { app, at: wantSetup ? `its setup screen ("${t}"; no Hive on this machine)` : `straight to the Hive at ${at} (${name})` };
}
const bounds = app => app.evaluate(({ BrowserWindow }) => { const w = BrowserWindow.getAllWindows()[0]; return w ? w.getNormalBounds() : null; });
(async () => {
  if (fs.existsSync(dest)) throw new Error(`${dest} is already there before the test`);
  download(zip);
  const t0 = Date.now();
  let { app, at } = await open('macdl');
  console.log(`ok mac download launch: ${dest} opens ${at} in ${Math.round((Date.now() - t0) / 1000)} s`);
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
    console.log(`ok mac download upgrade: ${old} to ${newVersion} dragged over ${dest} in ${Math.round((Date.now() - t1) / 1000)} s; window kept at ${now.width}x${now.height}+${now.x}+${now.y}; ${newVersion} opens ${at}`);
  }
  // The update notice as a Mac person sees it: the app opened the Finder's way (open -a, so no HIVE_APP_TEST, which skips the
  // notification) with a stand-in site whose app is newer. macOS asks once before an app may post notifications and this
  // app is only ad-hoc signed, so a notification that never shows is said in words (with a screenshot), not a red build;
  // an app that never noticed the newer one is.
  {
    const http = require('http');
    const body = '{"version":"9.9.9"}\n';
    const site = http.createServer((q, r) => { r.writeHead(q.url === '/app-version.json' ? 200 : 404, { 'Content-Type': 'application/json', 'Content-Length': Buffer.byteLength(body) }); r.end(body); });
    await new Promise(r => site.listen(0, '127.0.0.1', r));
    const data = path.join(os.homedir(), 'Library/Application Support/Hive');
    const seenFile = path.join(data, 'newer-seen.txt'), logF = path.join(data, 'app.log');
    const helper = (...a) => execFileSync('swift', [path.join(__dirname, 'macbanner.swift'), ...a], { encoding: 'utf8', timeout: 300000, stdio: ['ignore', 'pipe', 'pipe'] });
    // One round: open the app, wait for its notice, a screenshot each second for 10 s (a banner stays about 5 s), then
    // macOS's own word on the Hive's notification setting and the text its recognizer reads on each shot.
    const round = async (tag) => {
      fs.rmSync(seenFile, { force: true });
      const before = fs.existsSync(logF) ? fs.statSync(logF).size : 0;
      const said = () => { try { const b = fs.readFileSync(logF); return b.subarray(Math.min(before, b.length)).toString('utf8'); } catch { return ''; } };
      sh('open', ['-n', '-a', dest, '--env', `HIVE_SITE=http://127.0.0.1:${site.address().port}`]);
      const t3 = Date.now(); let seen = '';
      while (Date.now() - t3 < 30000) {
        try { seen = fs.readFileSync(seenFile, 'utf8').trim(); } catch { }
        if (seen || /notification (failed|not supported)/.test(said())) break;
        await sleep(1000);
      }
      const shots = [];
      for (let k = 0; k < 10; k++) {
        const f = `test/shots/macnotice${tag}${k ? '-' + k : ''}.png`;
        try { sh('screencapture', ['-x', f]); shots.push(f); } catch { }
        await sleep(1000);
      }
      let setting = 'not read', banner = '', seenText = [];
      try {
        for (const l of helper('cc.ihsan.hive', ...shots).split('\n')) {
          if (l.startsWith('ncprefs: ')) setting = l.slice(9);
          const m = l.match(/^ocr (\S+): (.*)$/);
          if (!m) continue;
          const parts = m[2].split(' | ');
          if (!banner && parts.some(x => /A newer Hive app is available|Click to open the download page/.test(x))) banner = m[1];
          for (const x of parts) if (/otification|Hive app|Allow/.test(x) && !seenText.includes(x)) seenText.push(x);
        }
      } catch (e) { setting = 'not read: ' + String(e.stderr || e.message).split('\n').filter(Boolean).slice(-2).join(' / ').slice(0, 200); }
      const lines = said().split(/\r?\n/).filter(x => /update notice|notification /.test(x)).map(x => x.replace(/^.*?(update notice|notification )/, '$1'));
      try { sh('pkill', ['-f', dest + '/Contents/MacOS/Hive']); } catch { }
      await sleep(2000);
      return { seen, shots, setting, banner, seenText, lines };
    };
    let r = await round('');
    if (!r.lines.some(x => x === 'update notice 9.9.9')) { site.close(); throw new Error(`the app from ${dest} never noticed the stand-in site's 9.9.9 in 30 s (log: ${r.lines.join('; ') || 'nothing about it'})`); }
    // Electron's show event on a Mac means macOS took the notification, not that a banner appeared: only a screenshot says that.
    if (r.seen === '9.9.9' && r.lines.includes('notification shown 9.9.9')) console.log(`ok mac notice: macOS took the notification "A newer Hive app is available" for 9.9.9 (log: ${r.lines.join('; ')})`);
    else console.log(`no mac notice on this runner: newer-seen.txt ${r.seen ? 'says ' + r.seen : 'not written'} after 30 s; log: ${r.lines.join('; ')}`);
    console.log(`  macOS's notification setting for the Hive: ${r.setting}`);
    console.log(`  text read on the screen about it: ${r.seenText.join(' / ').slice(0, 400) || 'none'}`);
    let how = 'as the Hive is set up after its first notice';
    // With notifications off for the Hive, a second round with them turned on, as a person would in System Settings.
    if (!r.banner && /allow notifications OFF/.test(r.setting)) {
      let turned = '';
      try { turned = helper('--allow', 'cc.ihsan.hive').trim(); } catch (e) { turned = 'failed: ' + String(e.stderr || e.message).split('\n').filter(Boolean).slice(-1)[0]; }
      for (const p of ['usernoted', 'NotificationCenter']) { try { sh('killall', [p]); } catch { } }
      await sleep(3000);
      console.log(`  notifications turned on for the Hive, as a person would in System Settings: ${turned}`);
      r = await round('-on');
      how = 'after notifications were turned on for the Hive';
      console.log(`  second notice: ${r.lines.join('; ') || 'nothing in the log'}; setting now: ${r.setting}`);
      console.log(`  text read on the screen about it: ${r.seenText.join(' / ').slice(0, 400) || 'none'}`);
    }
    site.close();
    if (r.banner) console.log(`ok mac banner: ${r.banner} shows the banner "A newer Hive app is available", ${how}`);
    else console.log(`no mac banner: none of the screenshots over 10 s shows it, ${how} (test/shots/macnotice*.png, ${r.shots.length} in the last round); setting: ${r.setting}`);
    // The control: a notification from macOS's own osascript (sent as Script Editor), the same 10 s of screenshots. A banner
    // here and none for the Hive means the app needs a fix; none here either means this runner draws no banners at all.
    const ctlId = 'com.apple.ScriptEditor2';
    const control = async (tag) => {
      let sent = 'sent';
      try { sh('osascript', ['-e', 'display notification "probe" with title "Hive control"']); } catch (e) { sent = 'not sent: ' + String(e.stderr || e.message).split('\n').filter(Boolean).slice(-1)[0]; }
      const shots = [];
      for (let k = 0; k < 10; k++) {
        const f = `test/shots/macctl${tag}${k ? '-' + k : ''}.png`;
        try { sh('screencapture', ['-x', f]); shots.push(f); } catch { }
        await sleep(1000);
      }
      let setting = 'not read', banner = '';
      try {
        for (const l of helper(ctlId, ...shots).split('\n')) {
          if (l.startsWith('ncprefs: ')) setting = l.slice(9);
          const m = l.match(/^ocr (\S+): (.*)$/);
          if (m && !banner && m[2].split(' | ').some(x => /Hive control/.test(x))) banner = m[1];
        }
      } catch (e) { setting = 'not read: ' + String(e.stderr || e.message).split('\n').filter(Boolean).slice(-2).join(' / ').slice(0, 200); }
      return { sent, shots, setting, banner };
    };
    let c = await control(''), chow = 'as the runner is set up';
    console.log(`  control notification from osascript: ${c.sent}; Script Editor's notification setting: ${c.setting}`);
    if (!c.banner && /allow notifications OFF/.test(c.setting)) {
      let turned = '';
      try { turned = helper('--allow', ctlId).trim(); } catch (e) { turned = 'failed: ' + String(e.stderr || e.message).split('\n').filter(Boolean).slice(-1)[0]; }
      for (const p of ['usernoted', 'NotificationCenter']) { try { sh('killall', [p]); } catch { } }
      await sleep(3000);
      c = await control('-on'); chow = 'after notifications were turned on for Script Editor';
      console.log(`  control again: ${turned}; ${c.sent}; setting now: ${c.setting}`);
    }
    if (c.banner) console.log(`ok mac control banner: ${c.banner} shows "Hive control", ${chow}; so this runner draws banners and the Hive's is missing`);
    else console.log(`no mac control banner: none of the screenshots over 10 s shows "Hive control", ${chow} (test/shots/macctl*.png); so this runner draws no banners, and the Hive's banner needs a real Mac`);
  }
  fs.rmSync(dest, { recursive: true, force: true });
})().catch(err => { console.error('FAILED', err.message || err); try { fs.rmSync(dest, { recursive: true, force: true }); } catch { } process.exit(1); });
